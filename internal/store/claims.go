package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Claim is a durable, token-based work claim on a VM row (ADR-0002).
type Claim struct {
	Token     uuid.UUID
	Owner     string
	ExpiresAt time.Time
	VM        *VM
}

// ErrClaimLost: the guarded commit found the claim gone, replaced, or its
// lease expired — the whole transition must roll back. This is the loser's
// error in every claim race, and it is always safe: level-triggered rescan
// redoes the work.
var ErrClaimLost = errors.New("store: claim lost (token replaced or lease expired)")

// ClaimDirtyVMs atomically claims up to limit dirty rows: unrealized
// desired revision or transitional phase, backoff elapsed, and no live
// claim. One statement mints the token and sets the lease — there is no
// window where a row is selected but unclaimed.
func (s *Store) ClaimDirtyVMs(ctx context.Context, owner string, lease time.Duration, limit int) ([]*Claim, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM vms
			WHERE next_attempt_at <= clock_timestamp()
			  AND (claim_expires_at IS NULL OR claim_expires_at < clock_timestamp())
			  AND (phase <> 'FAILED' OR deleted_at IS NOT NULL)
			ORDER BY next_attempt_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE vms v SET
			claim_token = gen_random_uuid(),
			claim_owner = $1,
			claim_expires_at = clock_timestamp() + $2,
			resource_version = resource_version + 1
		FROM candidates c WHERE v.id = c.id
		RETURNING v.claim_token, v.claim_owner, v.claim_expires_at,
			`+prefixedVMColumns("v"),
		owner, lease, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var claims []*Claim
	for rows.Next() {
		var c Claim
		vm, err := scanClaimRow(rows, &c)
		if err != nil {
			return nil, err
		}
		c.VM = vm
		claims = append(claims, &c)
	}
	return claims, rows.Err()
}

// RenewClaim extends the lease, guarded by the token. Long work renews;
// a renewal that returns ErrClaimLost means stop immediately.
func (s *Store) RenewClaim(ctx context.Context, vmID uuid.UUID, token uuid.UUID, lease time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE vms SET claim_expires_at = clock_timestamp() + $3
		WHERE id = $1 AND claim_token = $2
		  AND claim_expires_at >= clock_timestamp()`,
		vmID, token, lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrClaimLost
	}
	return nil
}

// CompleteClaimTx begins the transition transaction and takes the VM row
// lock WITHOUT releasing the claim: the lease-aware release is the LAST
// statement, via FinishClaim, immediately before commit — checking the
// lease in the first statement would leave a stall window in which an
// expired worker could still commit (batch-review finding [0]).
//
// Usage: tx, _ := CompleteClaimTx(...); ...transition work...;
// FinishClaim(ctx, tx, vmID, token, backoff); tx.Commit(ctx).
//
// The backoff parameter here is advisory documentation of intent only —
// the value that matters is the one passed to FinishClaim.
func (s *Store) CompleteClaimTx(ctx context.Context, vmID uuid.UUID, token uuid.UUID, backoff time.Duration) (pgx.Tx, error) {
	_ = backoff // release happens in FinishClaim; kept for call-site clarity
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Lock the row and verify the claim is still ours and live — but do
	// NOT release it yet; work happens under the held claim.
	var ok bool
	err = tx.QueryRow(ctx, `
		SELECT claim_token = $2 AND claim_expires_at >= clock_timestamp()
		FROM vms WHERE id = $1 FOR UPDATE`, vmID, token).Scan(&ok)
	if err != nil || !ok {
		_ = tx.Rollback(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, ErrClaimLost
	}
	return tx, nil
}

// FinishClaim is the transition's FINAL statement: it releases the claim
// under the full guard (token + lease unexpired AT THIS MOMENT). Zero rows
// means the lease lapsed during the work — the caller must roll back;
// nothing durable escapes.
func (s *Store) FinishClaim(ctx context.Context, tx pgx.Tx, vmID uuid.UUID, token uuid.UUID, backoff time.Duration) error {
	tag, err := tx.Exec(ctx, `
		UPDATE vms SET
			claim_token = NULL, claim_owner = NULL, claim_expires_at = NULL,
			next_attempt_at = clock_timestamp() + $3,
			resource_version = resource_version + 1
		WHERE id = $1 AND claim_token = $2
		  AND claim_expires_at >= clock_timestamp()`,
		vmID, token, backoff)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrClaimLost
	}
	return nil
}

// RecordFailure durably records a failed attempt under the claim guard:
// bumps attempts, stores the error class, schedules the next attempt, and
// releases the claim. If budget is exhausted, parks the VM in FAILED and
// terminalizes the operation — in the SAME transaction (plan D3/D5).
func (s *Store) RecordFailure(ctx context.Context, vmID uuid.UUID, token uuid.UUID, errClass, errMsg string, nextBackoff time.Duration, budget int, opID *uuid.UUID) (failed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var attempts int
	err = tx.QueryRow(ctx, `
		UPDATE vms SET
			attempts = attempts + 1,
			last_error_class = $3, last_error = $4,
			claim_token = NULL, claim_owner = NULL, claim_expires_at = NULL,
			next_attempt_at = clock_timestamp() + $5,
			resource_version = resource_version + 1
		WHERE id = $1 AND claim_token = $2
		  AND claim_expires_at >= clock_timestamp()
		RETURNING attempts`,
		vmID, token, errClass, errMsg, nextBackoff).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrClaimLost
	}
	if err != nil {
		return false, err
	}

	if attempts >= budget {
		if _, err := tx.Exec(ctx, `
			UPDATE vms SET phase = 'FAILED', resource_version = resource_version + 1
			WHERE id = $1`, vmID); err != nil {
			return false, err
		}
		if opID != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE operations SET state='FAILED', error=$2, finished_at=now()
				WHERE id=$1 AND state IN ('PENDING','RUNNING')`,
				*opID, "retry budget exhausted: "+errMsg); err != nil {
				return false, err
			}
		}
		failed = true
	}
	return failed, tx.Commit(ctx)
}

// prefixedVMColumns returns the vm column list qualified with an alias.
func prefixedVMColumns(alias string) string {
	return alias + `.id, ` + alias + `.name, ` + alias + `.spec, ` + alias + `.status, ` +
		alias + `.phase, ` + alias + `.spec_generation, ` + alias + `.resource_version, ` +
		alias + `.desired_revision, ` + alias + `.node_name, ` + alias + `.placement_epoch, ` +
		alias + `.observed_state, ` + alias + `.applied_revision, ` +
		alias + `.deleted_at, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func scanClaimRow(rows pgx.Rows, c *Claim) (*VM, error) {
	var (
		vm         VM
		specJSON   []byte
		statusJSON []byte
	)
	err := rows.Scan(&c.Token, &c.Owner, &c.ExpiresAt,
		&vm.ID, &vm.Name, &specJSON, &statusJSON, &vm.Phase,
		&vm.SpecGeneration, &vm.ResourceVersion, &vm.DesiredRevision,
		&vm.NodeName, &vm.PlacementEpoch, &vm.ObservedState, &vm.AppliedRevision,
		&vm.DeletedAt, &vm.CreatedAt, &vm.UpdatedAt)
	if err != nil {
		return nil, err
	}
	vm.Spec = &vmcv1.VmSpec{}
	if err := pju.Unmarshal(specJSON, vm.Spec); err != nil {
		return nil, err
	}
	vm.Status = &vmcv1.VmStatus{}
	if len(statusJSON) > 0 && string(statusJSON) != "{}" {
		if err := pju.Unmarshal(statusJSON, vm.Status); err != nil {
			return nil, err
		}
	}
	return &vm, nil
}
