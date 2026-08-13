package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Node is a logical scheduling partition served by a host daemon.
type Node struct {
	Name                string
	HostID              string
	SessionID           *uuid.UUID
	SessionGeneration   int64
	LeaseExpiresAt      *time.Time
	CPUs                int64
	MemoryBytes         int64
	DiskBytes           int64
	ReservedCPUs        int64
	ReservedMemoryBytes int64
	ReservedDiskBytes   int64
	Labels              map[string]string
}

// HostCapacity is what the daemon holding the host lock reports.
type HostCapacity struct {
	HostID      string
	CPUs        int64
	MemoryBytes int64
	DiskBytes   int64
}

// NodeQuota describes one logical node a daemon advertises.
type NodeQuota struct {
	Name        string
	CPUs        int64
	MemoryBytes int64
	DiskBytes   int64
	Labels      map[string]string
}

// Session identifies one registration of one logical node.
type Session struct {
	NodeName   string
	SessionID  uuid.UUID
	Generation int64
}

var (
	// ErrQuotaExceedsHost: the sum of logical-node quotas does not fit the
	// host allocatable (no overcommit in v0.1).
	ErrQuotaExceedsHost = errors.New("store: logical node quotas exceed host allocatable")
	// ErrHostMismatch: a node identity tried to rebind to a different host.
	ErrHostMismatch = errors.New("store: node is bound to a different host_id")
	// ErrStaleSession: heartbeat or report from a superseded session.
	ErrStaleSession = errors.New("store: stale node session")
)

const nodeColumns = `name, host_id, session_id, session_generation, lease_expires_at,
	cpus, memory_bytes, disk_bytes, reserved_cpus, reserved_memory, reserved_disk, labels`

// RegisterHost registers the physical host and its logical nodes in one
// transaction, validating that quota sums fit host allocatable, minting a
// fresh session (generation+1) per node, and rejecting cross-host node
// rebinding. Returns the new sessions.
func (s *Store) RegisterHost(ctx context.Context, hc HostCapacity, quotas []NodeQuota, lease time.Duration) ([]Session, error) {
	var sumCPU, sumMem, sumDisk int64
	for _, q := range quotas {
		sumCPU += q.CPUs
		sumMem += q.MemoryBytes
		sumDisk += q.DiskBytes
	}
	if sumCPU > hc.CPUs || sumMem > hc.MemoryBytes || sumDisk > hc.DiskBytes {
		return nil, fmt.Errorf("%w: quotas cpu=%d mem=%d disk=%d vs host cpu=%d mem=%d disk=%d",
			ErrQuotaExceedsHost, sumCPU, sumMem, sumDisk, hc.CPUs, hc.MemoryBytes, hc.DiskBytes)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `
		INSERT INTO hosts (host_id, cpus, memory_bytes, disk_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (host_id) DO UPDATE
		SET cpus = $2, memory_bytes = $3, disk_bytes = $4, updated_at = now()`,
		hc.HostID, hc.CPUs, hc.MemoryBytes, hc.DiskBytes); err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(quotas))
	for _, q := range quotas {
		labels, err := json.Marshal(orEmpty(q.Labels))
		if err != nil {
			return nil, err
		}
		var (
			sid uuid.UUID
			gen int64
		)
		err = tx.QueryRow(ctx, `
			INSERT INTO nodes (name, host_id, session_id, session_generation,
				lease_expires_at, cpus, memory_bytes, disk_bytes, labels)
			VALUES ($1, $2, gen_random_uuid(), 1, clock_timestamp() + $3, $4, $5, $6, $7)
			ON CONFLICT (name) DO UPDATE SET
				session_id = gen_random_uuid(),
				session_generation = nodes.session_generation + 1,
				lease_expires_at = clock_timestamp() + $3,
				cpus = $4, memory_bytes = $5, disk_bytes = $6, labels = $7,
				updated_at = now()
			WHERE nodes.host_id = $2
			RETURNING session_id, session_generation`,
			q.Name, hc.HostID, lease, q.CPUs, q.MemoryBytes, q.DiskBytes, labels,
		).Scan(&sid, &gen)
		if errors.Is(err, pgx.ErrNoRows) {
			// The conditional upsert matched a row bound to another host.
			return nil, fmt.Errorf("%w: node %q", ErrHostMismatch, q.Name)
		}
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, Session{NodeName: q.Name, SessionID: sid, Generation: gen})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sessions, nil
}

// Heartbeat extends a node lease, guarded by the session. A superseded
// daemon's heartbeat is rejected, which is its signal to halt substrate
// actions.
func (s *Store) Heartbeat(ctx context.Context, sess Session, lease time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE nodes SET lease_expires_at = clock_timestamp() + $4, updated_at = now()
		WHERE name = $1 AND session_id = $2 AND session_generation = $3`,
		sess.NodeName, sess.SessionID, sess.Generation, lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleSession
	}
	return nil
}

// ReadyNodes returns nodes with unexpired leases (the scheduler's filter
// input; the reservation statement rechecks lease validity atomically).
func (s *Store) ReadyNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+nodeColumns+`
		FROM nodes WHERE lease_expires_at >= clock_timestamp()
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNode fetches one node regardless of lease state.
func (s *Store) GetNode(ctx context.Context, name string) (*Node, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+nodeColumns+`
		FROM nodes WHERE name = $1`, name)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

func scanNode(row pgx.Row) (*Node, error) {
	var (
		n      Node
		labels []byte
	)
	if err := row.Scan(&n.Name, &n.HostID, &n.SessionID, &n.SessionGeneration,
		&n.LeaseExpiresAt, &n.CPUs, &n.MemoryBytes, &n.DiskBytes,
		&n.ReservedCPUs, &n.ReservedMemoryBytes, &n.ReservedDiskBytes, &labels); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(labels, &n.Labels); err != nil {
		return nil, err
	}
	return &n, nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
