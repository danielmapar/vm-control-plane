// Package reconciler is the level-triggered control loop (ADR-0003): each
// pass claims dirty rows, computes ONE convergent transition from current
// desired + observed state, and commits every durable side effect of that
// transition inside the claim-guarded transaction (ADR-0002). Crash
// recovery is a rescan, not a replay.
package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sigtunnel/vm-control-plane/internal/clock"
	"github.com/sigtunnel/vm-control-plane/internal/faults"
	"github.com/sigtunnel/vm-control-plane/internal/scheduler"
	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Config tunes the loop.
type Config struct {
	Owner       string        // claim owner label (diagnostics only)
	Tick        time.Duration // scan interval
	Lease       time.Duration // claim lease
	ResyncWait  time.Duration // backoff when waiting on the agent
	PendingWait time.Duration // backoff when unschedulable
	RetryBudget int           // attempts before phase=Failed
	Clock       clock.Clock
	Log         *slog.Logger
}

func (c *Config) defaults() {
	if c.Tick <= 0 {
		c.Tick = 250 * time.Millisecond
	}
	if c.Lease <= 0 {
		c.Lease = 30 * time.Second
	}
	if c.ResyncWait <= 0 {
		c.ResyncWait = time.Second
	}
	if c.PendingWait <= 0 {
		c.PendingWait = 5 * time.Second
	}
	if c.RetryBudget <= 0 {
		c.RetryBudget = 10
	}
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Loop drives VMs toward their desired revisions.
type Loop struct {
	st  *store.Store
	cfg Config
}

func New(st *store.Store, cfg Config) *Loop {
	cfg.defaults()
	return &Loop{st: st, cfg: cfg}
}

// Run scans until ctx ends.
func (l *Loop) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.cfg.Clock.After(l.cfg.Tick):
		}
		if _, err := l.st.ExpireOperations(ctx, nil); err != nil && ctx.Err() == nil {
			l.cfg.Log.Warn("operation deadline sweep failed", "err", err)
		}
		claims, err := l.st.ClaimDirtyVMs(ctx, l.cfg.Owner, l.cfg.Lease, 10)
		if err != nil {
			if ctx.Err() == nil {
				l.cfg.Log.Warn("claim scan failed", "err", err)
			}
			continue
		}
		for _, claim := range claims {
			l.reconcile(ctx, claim)
		}
	}
}

// reconcile computes and commits one transition for one claimed VM.
func (l *Loop) reconcile(ctx context.Context, claim *store.Claim) {
	if err := faults.Hit(ctx, "controller.after-claim"); err != nil {
		return
	}
	vm := claim.VM
	log := l.cfg.Log.With("vm", vm.Name, "phase", vm.Phase, "revision", vm.DesiredRevision, "epoch", vm.PlacementEpoch)

	var err error
	switch {
	case vm.DeletedAt != nil:
		err = l.reconcileDeleting(ctx, claim)
	case vm.Phase == "PENDING":
		err = l.reconcilePending(ctx, claim)
	case vm.Phase == "PROVISIONING" || vm.Phase == "RUNNING" || vm.Phase == "STOPPED":
		err = l.reconcileConvergence(ctx, claim)
	default:
		// Nothing to do; release with a long backoff.
		err = l.completeNoop(ctx, claim, l.cfg.PendingWait)
	}

	switch {
	case err == nil:
	case errors.Is(err, store.ErrClaimLost):
		// Always safe: the rescan (or the new claim holder) redoes the work.
		log.Info("claim lost mid-transition (safe: transition rolled back)")
	case ctx.Err() != nil:
	default:
		log.Warn("transition failed; recording durable retry", "err", err)
		opID := l.findOpenOperationID(ctx, vm.ID, vm.DesiredRevision)
		failedNow, recErr := l.st.RecordFailure(ctx, vm.ID, claim.Token, "transient", err.Error(),
			l.cfg.ResyncWait, l.cfg.RetryBudget, opID)
		if recErr != nil && !errors.Is(recErr, store.ErrClaimLost) {
			log.Error("failure recording failed", "err", recErr)
		}
		if failedNow {
			log.Error("retry budget exhausted; vm parked FAILED with terminal operation")
		}
	}
}

// reconcilePending: schedule. Filter/score proposes; PlaceVM decides
// inside the claim-guarded transaction.
func (l *Loop) reconcilePending(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM
	nodes, err := l.st.ReadyNodes(ctx)
	if err != nil {
		return err
	}
	candidates := scheduler.Candidates(nodes, scheduler.FromSpec(vm.Spec))
	if len(candidates) == 0 {
		return l.markUnschedulable(ctx, claim)
	}

	for _, candidate := range candidates {
		tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token, 0)
		if err != nil {
			return err
		}
		if err := faults.Hit(ctx, "controller.before-complete"); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		_, err = l.st.PlaceVM(ctx, tx, vm.ID, candidate, scheduler.FromSpec(vm.Spec).Resources)
		if err != nil {
			_ = tx.Rollback(ctx)
			if errors.Is(err, store.ErrNoCapacity) {
				continue // next candidate; our claim is still live
			}
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		l.cfg.Log.Info("vm placed", "vm", vm.Name, "node", candidate.Name)
		return nil
	}
	return l.markUnschedulable(ctx, claim)
}

// markUnschedulable is a condition, not an error loop (ADR-0003): surface
// it in status and retry on resync.
func (l *Loop) markUnschedulable(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM
	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token, l.cfg.PendingWait)
	if err != nil {
		return err
	}
	status := vm.Status
	if status == nil {
		status = &vmcv1.VmStatus{}
	}
	setCondition(status, "Unschedulable", true, "NoCandidateNodes",
		"no ready node satisfies constraints and capacity")
	// Re-read version inside the tx for a clean CAS.
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, "PENDING", status); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// reconcileConvergence: compare evidence with desire; complete operations
// on EXACT revision matches (plan §6.5).
func (l *Loop) reconcileConvergence(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM

	wantRunning := vm.Spec.GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED
	wantState := "RUNNING"
	targetPhase := "RUNNING"
	if !wantRunning {
		wantState = "SHUTOFF"
		targetPhase = "STOPPED"
	}

	converged := vm.AppliedRevision == vm.DesiredRevision && vm.ObservedState == wantState
	if !converged {
		// The agent drives; we wait (level-triggered — evidence wakes us).
		return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
	}

	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token, l.cfg.PendingWait*6)
	if err != nil {
		return err
	}
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	status := fresh.Status
	if status == nil {
		status = &vmcv1.VmStatus{}
	}
	status.AppliedRevision = vm.AppliedRevision
	setCondition(status, "Unschedulable", false, "", "")
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, targetPhase, status); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.terminalizeOps(ctx, tx, vm.ID, vm.DesiredRevision); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	l.cfg.Log.Info("vm converged", "vm", vm.Name, "phase", targetPhase, "revision", vm.DesiredRevision)
	return nil
}

// reconcileDeleting: the agent drives teardown from the tombstone intent;
// once the placement ledger shows torn_down (teardown receipt) — or the VM
// was never placed — finalize: remove the row and terminalize the DELETE
// operation in one transaction.
func (l *Loop) reconcileDeleting(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM

	if vm.PlacementEpoch > 0 {
		p, err := l.st.GetPlacement(ctx, nil, vm.ID, vm.PlacementEpoch)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if p != nil && p.State != "torn_down" {
			// Teardown not yet proven; the tombstone intent keeps driving it.
			return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
		}
	}

	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token, 0)
	if err != nil {
		return err
	}
	if err := faults.Hit(ctx, "controller.before-finalize"); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	// Belt and braces: release is idempotent even after a receipt did it.
	if vm.PlacementEpoch > 0 {
		if err := l.st.ReleasePlacement(ctx, tx, vm.ID, vm.PlacementEpoch); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM vms WHERE id=$1`, vm.ID); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.terminalizeOps(ctx, tx, vm.ID, vm.DesiredRevision); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	l.cfg.Log.Info("vm finalized (row removed; delete operation done)", "vm", vm.Name)
	return nil
}

// terminalizeOps completes operations for exactly the realized revision as
// DONE, and any OLDER open operations as SUPERSEDED — every operation
// terminates (D3).
func (l *Loop) terminalizeOps(ctx context.Context, tx pgx.Tx, vmID uuid.UUID, realizedRevision int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE operations SET state='DONE', finished_at=now()
		WHERE resource_id=$1 AND target_revision=$2 AND state IN ('PENDING','RUNNING')`,
		vmID, realizedRevision); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE operations SET state='SUPERSEDED',
			error='a newer desired revision was realized first', finished_at=now()
		WHERE resource_id=$1 AND target_revision<$2 AND state IN ('PENDING','RUNNING')`,
		vmID, realizedRevision); err != nil {
		return err
	}
	return nil
}

// completeNoop releases the claim with a backoff (waiting states).
func (l *Loop) completeNoop(ctx context.Context, claim *store.Claim, backoff time.Duration) error {
	tx, err := l.st.CompleteClaimTx(ctx, claim.VM.ID, claim.Token, backoff)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (l *Loop) findOpenOperationID(ctx context.Context, vmID uuid.UUID, revision int64) *uuid.UUID {
	var id uuid.UUID
	err := l.st.Pool().QueryRow(ctx, `
		SELECT id FROM operations
		WHERE resource_id=$1 AND target_revision=$2 AND state IN ('PENDING','RUNNING')
		ORDER BY created_at DESC LIMIT 1`, vmID, revision).Scan(&id)
	if err != nil {
		return nil
	}
	return &id
}

func setCondition(st *vmcv1.VmStatus, condType string, active bool, reason, message string) {
	for _, c := range st.Conditions {
		if c.Type == condType {
			c.Active, c.Reason, c.Message = active, reason, message
			return
		}
	}
	if active {
		st.Conditions = append(st.Conditions, &vmcv1.Condition{
			Type: condType, Active: true, Reason: reason, Message: message,
		})
	}
}
