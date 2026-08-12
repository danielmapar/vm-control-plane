// Package store is the single source of truth shared by the API and the
// reconciler — they never talk to each other directly (ADR-0001/0002). All
// row mutations compare-and-set resource_version: a stale writer loses
// cleanly instead of silently overwriting.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Sentinel errors callers branch on.
var (
	ErrNotFound   = errors.New("store: not found")
	ErrStaleWrite = errors.New("store: stale write (resource_version mismatch)")
	ErrDuplicate  = errors.New("store: duplicate")
)

// VM is the database view of a VirtualMachine row.
type VM struct {
	ID              uuid.UUID
	Name            string
	Spec            *vmcv1.VmSpec
	Status          *vmcv1.VmStatus
	Phase           string
	SpecGeneration  int64
	ResourceVersion int64
	DesiredRevision int64
	NodeName        *string
	PlacementEpoch  int64
	ObservedState   string
	AppliedRevision int64
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store wraps the connection pool. It is the only type that touches SQL.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool for transaction composition by higher
// layers that must combine repositories in one transaction.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// querier lets repository methods run inside or outside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	pj  = protojson.MarshalOptions{UseProtoNames: false}
	pju = protojson.UnmarshalOptions{DiscardUnknown: true}
)

const vmColumns = `id, name, spec, status, phase, spec_generation,
	resource_version, desired_revision, node_name, placement_epoch,
	observed_state, applied_revision, deleted_at, created_at, updated_at`

// CreateVM inserts a new VM row (phase PENDING, generation 1, revision 1)
// using the given querier (pass a tx to compose with operation insertion).
func (s *Store) CreateVM(ctx context.Context, q querier, id uuid.UUID, name string, spec *vmcv1.VmSpec) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	specJSON, err := pj.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	row := q.QueryRow(ctx, `
		INSERT INTO vms (id, name, spec, status)
		VALUES ($1, $2, $3, '{}')
		RETURNING `+vmColumns,
		id, name, specJSON)
	vm, err := scanVM(row)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: vm %q", ErrDuplicate, name)
	}
	return vm, err
}

// GetVM fetches by name. Tombstoned rows are returned (deletion is a state,
// not an absence, until teardown finalizes).
func (s *Store) GetVM(ctx context.Context, q querier, name string) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `SELECT `+vmColumns+` FROM vms WHERE name = $1`, name)
	return scanVM(row)
}

// GetVMByID fetches by UUID.
func (s *Store) GetVMByID(ctx context.Context, q querier, id uuid.UUID) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = $1`, id)
	return scanVM(row)
}

// ListVMs returns rows ordered by name after the given name (keyset
// pagination; empty after = first page).
func (s *Store) ListVMs(ctx context.Context, q querier, after string, limit int) ([]*VM, error) {
	if q == nil {
		q = s.pool
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.Query(ctx, `
		SELECT `+vmColumns+` FROM vms
		WHERE name > $1 ORDER BY name LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VM
	for rows.Next() {
		vm, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, vm)
	}
	return out, rows.Err()
}

// UpdateVMStatus writes status+phase under a resource_version CAS. The
// caller passes the version it read; a concurrent writer makes this return
// ErrStaleWrite, and the caller re-reads and recomputes (level-triggered
// loops make that safe by construction).
func (s *Store) UpdateVMStatus(ctx context.Context, q querier, id uuid.UUID, expectVersion int64, phase string, status *vmcv1.VmStatus) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	statusJSON, err := pj.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("marshal status: %w", err)
	}
	row := q.QueryRow(ctx, `
		UPDATE vms SET status=$3, phase=$4,
			resource_version = resource_version + 1, updated_at = now()
		WHERE id=$1 AND resource_version=$2
		RETURNING `+vmColumns,
		id, expectVersion, statusJSON, phase)
	vm, err := scanVM(row)
	if errors.Is(err, ErrNotFound) {
		return nil, staleOrMissing(ctx, q, id)
	}
	return vm, err
}

// TombstoneVM marks deletion: sets deleted_at and phase DELETING and bumps
// the desired revision — deletion is one more desired state, driven through
// the same reconciliation machinery (ADR-0003). Set-once: a second call is
// a no-op returning the current row.
func (s *Store) TombstoneVM(ctx context.Context, q querier, id uuid.UUID, expectVersion int64) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `
		UPDATE vms SET
			deleted_at = COALESCE(deleted_at, now()),
			phase = CASE WHEN deleted_at IS NULL THEN 'DELETING' ELSE phase END,
			desired_revision = CASE WHEN deleted_at IS NULL THEN desired_revision + 1 ELSE desired_revision END,
			resource_version = resource_version + 1,
			updated_at = now()
		WHERE id=$1 AND resource_version=$2
		RETURNING `+vmColumns,
		id, expectVersion)
	vm, err := scanVM(row)
	if errors.Is(err, ErrNotFound) {
		return nil, staleOrMissing(ctx, q, id)
	}
	return vm, err
}

// staleOrMissing disambiguates a zero-row CAS UPDATE: stale version vs
// genuinely absent row.
func staleOrMissing(ctx context.Context, q querier, id uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vms WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrStaleWrite
	}
	return ErrNotFound
}

func scanVM(row pgx.Row) (*VM, error) {
	var (
		vm         VM
		specJSON   []byte
		statusJSON []byte
	)
	err := row.Scan(&vm.ID, &vm.Name, &specJSON, &statusJSON, &vm.Phase,
		&vm.SpecGeneration, &vm.ResourceVersion, &vm.DesiredRevision,
		&vm.NodeName, &vm.PlacementEpoch, &vm.ObservedState, &vm.AppliedRevision,
		&vm.DeletedAt, &vm.CreatedAt, &vm.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	vm.Spec = &vmcv1.VmSpec{}
	if err := pju.Unmarshal(specJSON, vm.Spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	vm.Status = &vmcv1.VmStatus{}
	if len(statusJSON) > 0 && string(statusJSON) != "{}" {
		if err := pju.Unmarshal(statusJSON, vm.Status); err != nil {
			return nil, fmt.Errorf("unmarshal status: %w", err)
		}
	}
	return &vm, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
