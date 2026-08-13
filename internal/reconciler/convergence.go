package reconciler

import (
	"context"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

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
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	// Recompute convergence from the fresh row under the lock: evidence, power,
	// or a tombstone may have moved since the claim snapshot, and writing DONE
	// from a stale snapshot would overwrite it.
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		return err
	}
	freshWantState, targetPhase := desiredStates(fresh.Spec)
	if fresh.DeletedAt != nil ||
		fresh.DesiredRevision != vm.DesiredRevision ||
		fresh.AppliedRevision != fresh.DesiredRevision ||
		fresh.ObservedState != freshWantState {
		// Release the lock before completeNoop re-claims the row.
		_ = tx.Rollback(ctx)
		return l.completeNoop(ctx, claim, l.cfg.ResyncWait)
	}
	st := fresh.Status
	if st == nil {
		st = &vmcv1.VmStatus{}
	}
	st.AppliedRevision = fresh.AppliedRevision
	setCondition(st, "Unschedulable", false, "", "")
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, targetPhase, st); err != nil {
		return err
	}
	if err := l.st.TerminalizeRealizedOps(ctx, tx, vm.ID, fresh.DesiredRevision); err != nil {
		return err
	}
	if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, l.cfg.PendingWait*6); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	l.cfg.Log.Info("vm converged", "vm", vm.Name, "phase", targetPhase, "revision", vm.DesiredRevision)
	return nil
}
