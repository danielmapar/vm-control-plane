package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrStaleReport: the report lost the lexicographic (session_generation,
// report_seq) comparison, or came from a superseded session/epoch.
var ErrStaleReport = errors.New("store: stale report rejected")

// Report is one piece of ordered evidence from a daemon.
type Report struct {
	Session         Session
	VMID            uuid.UUID
	Epoch           int64
	Seq             int64
	AppliedRevision int64
	State           ObservedState
	Detail          string
}

// ApplyReport stores observed evidence under the full fence: the session must
// be the node's current one, the epoch must be the VM's current placement, and
// (session_generation, seq) must exceed the stored watermark lexicographically.
// Everything is checked in one statement so a stale writer cannot interleave.
func (s *Store) ApplyReport(ctx context.Context, r Report) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE vms v SET
			observed_state = $4,
			applied_revision = $5,
			report_session_gen = $6,
			report_seq = $7,
			observed_detail = $8,
			next_attempt_at = clock_timestamp(),  -- evidence wakes the loop
			resource_version = resource_version + 1,
			updated_at = now()
		FROM nodes n
		WHERE v.id = $1
		  AND v.placement_epoch = $2
		  AND n.name = $3
		  AND n.name = v.node_name
		  AND n.session_id = $9 AND n.session_generation = $6
		  AND ( $6 > v.report_session_gen
		        OR ($6 = v.report_session_gen AND $7 > v.report_seq) )`,
		r.VMID, r.Epoch, r.Session.NodeName, r.State, r.AppliedRevision,
		r.Session.Generation, r.Seq, r.Detail, r.Session.SessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleReport
	}
	return nil
}

// NextActionAttempt returns (and mints if absent) the current attempt
// pacing for a (vm, epoch, revision): the daemon receives not_before and
// the attempt token in its intent and executes only when due.
func (s *Store) NextActionAttempt(ctx context.Context, vmID uuid.UUID, epoch, revision int64) (notBeforeMS int64, token uuid.UUID, err error) {
	err = s.pool.QueryRow(ctx, `
		INSERT INTO action_retries (vm_id, epoch, revision)
		VALUES ($1, $2, $3)
		ON CONFLICT (vm_id, epoch, revision, generation) DO UPDATE
			SET vm_id = action_retries.vm_id  -- no-op; makes RETURNING work
		RETURNING GREATEST(0, (EXTRACT(EPOCH FROM (not_before - clock_timestamp())) * 1000)::bigint),
		          attempt_token`,
		vmID, epoch, revision).Scan(&notBeforeMS, &token)
	return notBeforeMS, token, err
}

// RecordActionFailure bumps the durable attempt counter and pushes not_before,
// so an agent restart or duplicate intent cannot reset pacing. It is accepted
// idempotently by attempt token: a stale token is a no-op, and a fresh token is
// minted for the next attempt.
func (s *Store) RecordActionFailure(ctx context.Context, vmID uuid.UUID, epoch, revision int64, token uuid.UUID, backoffMS int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE action_retries SET
			attempts = attempts + 1,
			not_before = clock_timestamp() + ($5 * interval '1 millisecond'),
			attempt_token = gen_random_uuid()
		WHERE vm_id = $1 AND epoch = $2 AND revision = $3 AND attempt_token = $4`,
		vmID, epoch, revision, token, backoffMS)
	return err
}

// ErrTeardownNotPermitted: a receipt targeted a placement that is neither
// tombstoned nor superseded, so tearing down a live current placement is
// refused.
var ErrTeardownNotPermitted = errors.New("store: teardown receipt refused for a live current placement")

// MarkTeardownComplete records a teardown receipt under the VM→placement
// lock order. It is permitted only when teardown is legitimate: the VM is
// tombstoned, OR this epoch is no longer the VM's current placement (a
// superseded old epoch). A receipt can therefore never tear down the live
// current placement of a running VM. Idempotent; releases the reservation.
func (s *Store) MarkTeardownComplete(ctx context.Context, vmID uuid.UUID, epoch int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Lock order: VM row first (matches grants/deletion).
	var (
		deleted      bool
		currentEpoch int64
	)
	err = tx.QueryRow(ctx, `
		SELECT deleted_at IS NOT NULL, placement_epoch
		FROM vms WHERE id = $1 FOR UPDATE`, vmID).Scan(&deleted, &currentEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // VM already finalized: idempotent
	}
	if err != nil {
		return err
	}
	if !deleted && epoch == currentEpoch {
		return ErrTeardownNotPermitted
	}

	if err := s.ReleasePlacement(ctx, tx, vmID, epoch); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE vms SET next_attempt_at = clock_timestamp(),
			resource_version = resource_version + 1
		WHERE id = $1`, vmID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
