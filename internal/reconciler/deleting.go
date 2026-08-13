package reconciler

import (
	"context"
	"errors"

	"github.com/sigtunnel/vm-control-plane/internal/faults"
	"github.com/sigtunnel/vm-control-plane/internal/store"
)

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
			return l.releaseClaim(ctx, claim, l.cfg.PendingWait*6)
		}
		if p.State != store.PlacementTornDown {
			// Teardown not yet proven; the tombstone intent keeps driving it.
			return l.releaseClaim(ctx, claim, l.cfg.ResyncWait)
		}
	}

	tx, err := l.st.CompleteClaimTx(ctx, vm.ID, claim.Token)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := faults.Hit(ctx, "controller.before-finalize"); err != nil {
		return err
	}
	// Belt and braces: release is idempotent even after a receipt did it.
	if vm.PlacementEpoch > 0 {
		if err := l.st.ReleasePlacement(ctx, tx, vm.ID, vm.PlacementEpoch); err != nil {
			return err
		}
	}
	if err := l.st.DeleteVMRow(ctx, tx, vm.ID); err != nil {
		return err
	}
	if err := l.st.TerminalizeRealizedOps(ctx, tx, vm.ID, vm.DesiredRevision); err != nil {
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
