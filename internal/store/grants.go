package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GrantDenialReason enumerates why a grant was refused.
type GrantDenialReason string

const (
	DenyStaleSession    GrantDenialReason = "stale-session"
	DenyLeaseExpired    GrantDenialReason = "lease-expired"
	DenyNotAssigned     GrantDenialReason = "placement-not-assigned"
	DenyTombstoned      GrantDenialReason = "vm-tombstoned"
	DenyEpochSuperseded GrantDenialReason = "epoch-superseded"
)

// ErrGrantDenied carries the admission predicate that failed. A denied grant
// means: do not touch the substrate for this placement.
type ErrGrantDenied struct{ Reason GrantDenialReason }

func (e *ErrGrantDenied) Error() string { return "store: grant denied: " + string(e.Reason) }

// GrantExecution is the exposure proof: the daemon calls it before its first
// substrate action for (vm, epoch), and acts only after the grant commits. The
// transition runs under the documented lock order (VM row, then placement
// row), shared with tombstoning, so exactly one of grant, delete, or unassign
// wins on the same locks.
//
// Admission predicates, all checked under those locks:
//   - the VM is not tombstoned,
//   - the placement (vm, epoch) exists, is current (its epoch matches the VM
//     row), and is assigned (an already-granted placement is an idempotent
//     replay),
//   - the requesting session is the node's current session (id and generation)
//     with an unexpired lease, against the database clock.
//
// A delayed grant arriving after lease expiry, session replacement, or delete
// fails admission: it cannot convert a safely-unexposed placement into sticky
// exposure.
func (s *Store) GrantExecution(ctx context.Context, sess Session, vmID uuid.UUID, epoch int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Lock 1: the VM row.
	var (
		deleted      bool
		currentEpoch int64
	)
	err = tx.QueryRow(ctx, `
		SELECT deleted_at IS NOT NULL, placement_epoch
		FROM vms WHERE id=$1 FOR UPDATE`, vmID).Scan(&deleted, &currentEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deleted {
		return &ErrGrantDenied{DenyTombstoned}
	}
	if currentEpoch != epoch {
		return &ErrGrantDenied{DenyEpochSuperseded}
	}

	// Lock 2: the placement row.
	var (
		state    PlacementState
		nodeName string
	)
	err = tx.QueryRow(ctx, `
		SELECT state, node_name FROM placements
		WHERE vm_id=$1 AND epoch=$2 FOR UPDATE`, vmID, epoch).Scan(&state, &nodeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return &ErrGrantDenied{DenyNotAssigned}
	}
	if err != nil {
		return err
	}
	switch state {
	case PlacementGranted, PlacementAssigned:
	default:
		return &ErrGrantDenied{DenyNotAssigned}
	}
	if nodeName != sess.NodeName {
		return &ErrGrantDenied{DenyStaleSession}
	}

	// Session currency and lease, against the database clock. This is checked
	// for both the initial transition and the idempotent replay, so a wrong,
	// expired, or superseded daemon can never receive Granted=true even for an
	// already-granted placement.
	var current, live bool
	err = tx.QueryRow(ctx, `
		SELECT (session_id = $2 AND session_generation = $3),
		       (lease_expires_at >= clock_timestamp())
		FROM nodes WHERE name = $1`,
		sess.NodeName, sess.SessionID, sess.Generation).Scan(&current, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return &ErrGrantDenied{DenyStaleSession}
	}
	if err != nil {
		return err
	}
	if !current {
		return &ErrGrantDenied{DenyStaleSession}
	}
	if !live {
		return &ErrGrantDenied{DenyLeaseExpired}
	}

	if state == PlacementGranted {
		// Authorized replay after a lost grant response: the exposure fact is
		// durable and the requester is still the current live owner.
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE placements SET state='granted', granted_at=clock_timestamp()
		WHERE vm_id=$1 AND epoch=$2`, vmID, epoch); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UnassignIfUngranted reverses a placement that was never exposed: under the
// same lock order, it verifies the placement is still assigned (a granted
// placement is never unassigned — that is the reschedule-safety rule), releases
// the reservation, marks the ledger row torn_down, and returns the VM to
// Pending with no node.
//
// It returns false (and no error) when the placement turned out to be granted;
// the caller parks the VM in Unknown instead.
func (s *Store) UnassignIfUngranted(ctx context.Context, vmID uuid.UUID, epoch int64) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Lock order: VM row, then placement row (same as grants — no deadlock,
	// and exactly one of a concurrent grant/unassign pair wins).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM vms WHERE id=$1 FOR UPDATE`, vmID); err != nil {
		return false, err
	}
	var state PlacementState
	err = tx.QueryRow(ctx, `
		SELECT state FROM placements WHERE vm_id=$1 AND epoch=$2 FOR UPDATE`,
		vmID, epoch).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: placement (%s, %d)", ErrNotFound, vmID, epoch)
	}
	if err != nil {
		return false, err
	}
	if state != PlacementAssigned {
		return false, nil // granted (or already torn down): never unassign exposure
	}

	if err := s.ReleasePlacement(ctx, tx, vmID, epoch); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE vms SET node_name=NULL, phase='PENDING',
			next_attempt_at = clock_timestamp(),
			resource_version = resource_version + 1, updated_at = now()
		WHERE id=$1`, vmID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
