package reconciler

import (
	"context"
	"errors"

	"github.com/sigtunnel/vm-control-plane/internal/faults"
	"github.com/sigtunnel/vm-control-plane/internal/scheduler"
	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

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
		placed, err := l.tryPlace(ctx, claim, candidate)
		if err != nil {
			if errors.Is(err, store.ErrPlacementPreconditions) {
				// Tombstone or phase moved after the claim: requeue and let the
				// rescan recompute from fresh state.
				return l.releaseClaim(ctx, claim, 0)
			}
			return err
		}
		if placed {
			return nil
		}
		// No capacity on this candidate; the claim is still live, try the next.
	}
	return l.markUnschedulable(ctx, claim)
}

// tryPlace attempts to place the claimed VM on one candidate node inside the
// claim-guarded transaction. It returns (true, nil) when a placement committed,
// (false, nil) when the node had no capacity (try the next candidate), and a
// non-nil error otherwise (ErrPlacementPreconditions means requeue).
func (l *Loop) tryPlace(ctx context.Context, claim *store.Claim, candidate *store.Node) (bool, error) {
	vm := claim.VM
	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := faults.Hit(ctx, "controller.before-complete"); err != nil {
		return false, err
	}
	if _, err := l.st.PlaceVM(ctx, tx, vm.ID, candidate, scheduler.FromSpec(vm.Spec).Resources); err != nil {
		if errors.Is(err, store.ErrNoCapacity) {
			return false, nil
		}
		return false, err
	}
	if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, 0); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	l.cfg.Log.Info("vm placed", "vm", vm.Name, "node", candidate.Name)
	return true, nil
}

// markUnschedulable records a condition rather than looping on an error:
// surface it in status and retry on resync.
func (l *Loop) markUnschedulable(ctx context.Context, claim *store.Claim) error {
	vm := claim.VM
	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	st := vm.Status
	if st == nil {
		st = &vmcv1.VmStatus{}
	}
	setCondition(st, "Unschedulable", true, "NoCandidateNodes",
		"no ready node satisfies constraints and capacity")
	// Re-read the version inside the tx for a clean CAS.
	fresh, err := l.st.GetVMByID(ctx, tx, vm.ID)
	if err != nil {
		return err
	}
	if _, err := l.st.UpdateVMStatus(ctx, tx, vm.ID, fresh.ResourceVersion, store.PhasePending, st); err != nil {
		return err
	}
	if err := l.st.FinishClaim(ctx, tx, vm.ID, claim.Token, l.cfg.PendingWait); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
