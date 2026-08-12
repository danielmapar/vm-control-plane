package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Placement is one row of the grant/fencing ledger.
type Placement struct {
	VMID     uuid.UUID
	Epoch    int64
	NodeName string
	HostID   string
	State    string // assigned | granted | torn_down
}

// Resources is a reservation request.
type Resources struct {
	CPUs        int64
	MemoryBytes int64
	DiskBytes   int64
}

var (
	// ErrNoCapacity: the all-predicate conditional reservation matched zero
	// rows — capacity, labels, or lease changed between filter and commit.
	// The caller tries the next candidate (D6).
	ErrNoCapacity = errors.New("store: reservation predicates failed (capacity/lease)")
	// ErrActivePlacementExists: the partial unique index refused a second
	// active placement — the schema-level fence against double placement.
	ErrActivePlacementExists = errors.New("store: an active placement already exists for this vm")
)

// PlaceVM performs the placement transition INSIDE the caller's
// claim-guarded transaction (from CompleteClaimTx):
//
//  1. lock the VM row (epoch allocation under the row lock),
//  2. single-statement conditional reservation on the node — every hard
//     predicate (all three capacity dimensions AND an unexpired lease)
//     rechecked atomically; zero rows → ErrNoCapacity,
//  3. reservation row (unique per vm+epoch),
//  4. placement row state=assigned (partial unique: one active per VM),
//  5. VM row: node, new epoch, phase PROVISIONING.
//
// Everything commits or rolls back with the claim guard.
func (s *Store) PlaceVM(ctx context.Context, tx pgx.Tx, vmID uuid.UUID, node *Node, res Resources) (epoch int64, err error) {
	var currentEpoch int64
	if err := tx.QueryRow(ctx,
		`SELECT placement_epoch FROM vms WHERE id=$1 FOR UPDATE`, vmID,
	).Scan(&currentEpoch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	epoch = currentEpoch + 1

	tag, err := tx.Exec(ctx, `
		UPDATE nodes SET
			reserved_cpus   = reserved_cpus   + $2,
			reserved_memory = reserved_memory + $3,
			reserved_disk   = reserved_disk   + $4,
			updated_at = now()
		WHERE name = $1
		  AND reserved_cpus   + $2 <= cpus
		  AND reserved_memory + $3 <= memory_bytes
		  AND reserved_disk   + $4 <= disk_bytes
		  AND lease_expires_at >= clock_timestamp()`,
		node.Name, res.CPUs, res.MemoryBytes, res.DiskBytes)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrNoCapacity
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO reservations (vm_id, epoch, node_name, cpus, memory_bytes, disk_bytes)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		vmID, epoch, node.Name, res.CPUs, res.MemoryBytes, res.DiskBytes); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO placements (vm_id, epoch, node_name, host_id, state)
		VALUES ($1,$2,$3,$4,'assigned')`,
		vmID, epoch, node.Name, node.HostID); err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w (vm %s)", ErrActivePlacementExists, vmID)
		}
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE vms SET node_name=$2, placement_epoch=$3, phase='PROVISIONING',
			resource_version = resource_version + 1, updated_at = now()
		WHERE id=$1`,
		vmID, node.Name, epoch); err != nil {
		return 0, err
	}
	return epoch, nil
}

// ReleasePlacement tears down a placement's reservation and marks the
// ledger row torn_down — idempotent: the DELETE ... RETURNING drives the
// counter decrement, so a double release adjusts nothing (D6). Runs inside
// the caller's guarded transaction (unassign or finalization).
func (s *Store) ReleasePlacement(ctx context.Context, tx pgx.Tx, vmID uuid.UUID, epoch int64) error {
	var (
		node             string
		cpus, mem, disk  int64
		reservationFound = true
	)
	err := tx.QueryRow(ctx, `
		DELETE FROM reservations WHERE vm_id=$1 AND epoch=$2
		RETURNING node_name, cpus, memory_bytes, disk_bytes`,
		vmID, epoch).Scan(&node, &cpus, &mem, &disk)
	if errors.Is(err, pgx.ErrNoRows) {
		reservationFound = false
	} else if err != nil {
		return err
	}

	if reservationFound {
		if _, err := tx.Exec(ctx, `
			UPDATE nodes SET
				reserved_cpus   = reserved_cpus   - $2,
				reserved_memory = reserved_memory - $3,
				reserved_disk   = reserved_disk   - $4,
				updated_at = now()
			WHERE name = $1`,
			node, cpus, mem, disk); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE placements SET state='torn_down' WHERE vm_id=$1 AND epoch=$2`,
		vmID, epoch); err != nil {
		return err
	}
	return nil
}

// GetPlacement fetches one ledger row.
func (s *Store) GetPlacement(ctx context.Context, q querier, vmID uuid.UUID, epoch int64) (*Placement, error) {
	if q == nil {
		q = s.pool
	}
	var p Placement
	err := q.QueryRow(ctx, `
		SELECT vm_id, epoch, node_name, host_id, state
		FROM placements WHERE vm_id=$1 AND epoch=$2`, vmID, epoch).
		Scan(&p.VMID, &p.Epoch, &p.NodeName, &p.HostID, &p.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
