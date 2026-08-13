// Package reconciler is the level-triggered control loop: each pass claims
// dirty rows, computes one convergent transition from current desired and
// observed state, and commits every durable side effect of that transition
// inside the claim-guarded transaction. Crash recovery is a rescan, not a
// replay.
package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"time"

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

// New returns a Loop with defaults applied to any unset Config field.
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
		l.sweep(ctx)
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

// sweep runs the two time-driven policies that make stalls terminate: the
// operation deadline sweep and the node-loss sweep.
func (l *Loop) sweep(ctx context.Context) {
	if _, err := l.st.ExpireOperations(ctx, nil); err != nil && ctx.Err() == nil {
		l.cfg.Log.Warn("operation deadline sweep failed", "err", err)
	}
	actions, err := l.st.ExpireNodeVMs(ctx)
	if err != nil && ctx.Err() == nil {
		l.cfg.Log.Warn("node-loss sweep failed", "err", err)
		return
	}
	for _, a := range actions {
		l.cfg.Log.Warn("node-loss action", "vm", a.VMName, "node", a.Node, "outcome", a.Outcome)
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
	case vm.Phase == store.PhasePending:
		err = l.reconcilePending(ctx, claim)
	case isConvergencePhase(vm.Phase):
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
		opID, _ := l.st.FindOpenOperationID(ctx, nil, vm.ID, vm.DesiredRevision)
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

// isConvergencePhase reports whether a phase is one the convergence handler
// drives. UNKNOWN is included so node-loss recovery flows through the ordinary
// path.
func isConvergencePhase(p store.Phase) bool {
	switch p {
	case store.PhaseProvisioning, store.PhaseRunning, store.PhaseStopped, store.PhaseUnknown:
		return true
	}
	return false
}

// desiredStates maps a spec's power to the observed state that means "in sync"
// and the phase to record once it is reached.
func desiredStates(spec *vmcv1.VmSpec) (store.ObservedState, store.Phase) {
	if spec.GetPower() == vmcv1.PowerState_POWER_STATE_STOPPED {
		return store.ObservedShutoff, store.PhaseStopped
	}
	return store.ObservedRunning, store.PhaseRunning
}

// reconcilePending schedules the VM. The filter/score stage proposes; PlaceVM
// decides inside the claim-guarded transaction.
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
		tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
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
			if errors.Is(err, store.ErrPlacementPreconditions) {
				// Tombstone or phase moved after the claim: requeue and let the
				// rescan recompute from fresh state.
				return l.completeNoop(ctx, claim, 0)
			}
			return err
		}
		if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, 0); err != nil {
			_ = tx.Rollback(ctx)
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

// markUnschedulable records a condition rather than looping on an error:
// surface it in status and retry on resync.
func (l *Loop) markUnschedulable(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM
	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
	if err != nil {
		return err
	}
	status := vm.Status
	if status == nil {
		status = &vmcv1.VmStatus{}
	}
	setCondition(status, "Unschedulable", true, "NoCandidateNodes",
		"no ready node satisfies constraints and capacity")
	// Re-read the version inside the tx for a clean CAS.
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, store.PhasePending, status); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, l.cfg.PendingWait); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// reconcileConvergence compares evidence with desire and completes operations
// on exact revision matches.
func (l *Loop) reconcileConvergence(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM

	// Cheap pre-check from the claim snapshot; the authoritative decision is
	// recomputed from the fresh row under the lock below.
	wantState, _ := desiredStates(vm.Spec)
	if vm.AppliedRevision != vm.DesiredRevision || vm.ObservedState != wantState {
		// The agent drives; we wait. Reported evidence wakes us.
		return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
	}

	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
	if err != nil {
		return err
	}
	// Recompute convergence from the fresh row under the lock: evidence, power,
	// or a tombstone may have moved since the claim snapshot, and writing DONE
	// from a stale snapshot would overwrite it.
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	freshWantState, targetPhase := desiredStates(fresh.Spec)
	if fresh.DeletedAt != nil ||
		fresh.DesiredRevision != vm.DesiredRevision ||
		fresh.AppliedRevision != fresh.DesiredRevision ||
		fresh.ObservedState != freshWantState {
		_ = tx.Rollback(ctx)
		return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
	}
	status := fresh.Status
	if status == nil {
		status = &vmcv1.VmStatus{}
	}
	status.AppliedRevision = fresh.AppliedRevision
	setCondition(status, "Unschedulable", false, "", "")
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, targetPhase, status); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.st.TerminalizeRealizedOps(ctx, tx, vm.ID, fresh.DesiredRevision); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, l.cfg.PendingWait*6); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	l.cfg.Log.Info("vm converged", "vm", vm.Name, "phase", targetPhase, "revision", vm.DesiredRevision)
	return nil
}

// reconcileDeleting finalizes a tombstoned VM. The agent drives teardown from
// the tombstone intent; once the placement ledger shows torn_down (a teardown
// receipt), or the VM was never placed, this removes the row and terminalizes
// the DELETE operation in one transaction.
func (l *Loop) reconcileDeleting(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM

	if vm.PlacementEpoch > 0 {
		p, err := l.st.GetPlacement(ctx, nil, vm.ID, vm.PlacementEpoch)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if errors.Is(err, store.ErrNotFound) {
			// A missing ledger row for a positive epoch is corruption, not
			// teardown proof: fail closed and park as cleanup debt.
			l.cfg.Log.Error("placement ledger row missing for tombstoned vm — refusing to finalize",
				"vm", vm.Name, "epoch", vm.PlacementEpoch)
			return l.completeNoop(ctx, claim, l.cfg.PendingWait*6)
		}
		if p.State != store.PlacementTornDown {
			// Teardown not yet proven; the tombstone intent keeps driving it.
			return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
		}
	}

	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
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
	if err := l.st.DeleteVMRow(ctx, tx, vm.ID); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := l.st.TerminalizeRealizedOps(ctx, tx, vm.ID, vm.DesiredRevision); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	// The row is gone; FinishClaim's guard would find nothing — the DELETE
	// released the claim with the row. Commit directly.
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	l.cfg.Log.Info("vm finalized (row removed; delete operation done)", "vm", vm.Name)
	return nil
}

// completeNoop releases the claim with a backoff (used by waiting states).
func (l *Loop) completeNoop(ctx context.Context, claim *store.Claim, backoff time.Duration) error {
	tx, err := l.st.CompleteClaimTx(ctx, claim.VM.ID, claim.Token)
	if err != nil {
		return err
	}
	if err := l.st.FinishClaim(ctx, tx, claim.VM.ID, claim.Token, backoff); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
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
