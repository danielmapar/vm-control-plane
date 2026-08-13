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

// claimBatchSize is how many dirty rows one scan claims per tick.
const claimBatchSize = 10

// Run scans until ctx ends.
func (l *Loop) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.cfg.Clock.After(l.cfg.Tick):
		}
		l.sweep(ctx)
		claims, err := l.st.ClaimDirtyVMs(ctx, l.cfg.Owner, l.cfg.Lease, claimBatchSize)
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
	case err == nil, ctx.Err() != nil:
		return
	case errors.Is(err, store.ErrClaimLost):
		// Always safe: the rescan (or the new claim holder) redoes the work.
		log.Info("claim lost mid-transition (safe: transition rolled back)")
		return
	}

	// A genuine failure: record a durable retry.
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

// completeNoop releases the claim with a backoff (used by waiting states).
func (l *Loop) completeNoop(ctx context.Context, claim *store.Claim, backoff time.Duration) error {
	tx, err := l.st.CompleteClaimTx(ctx, claim.VM.ID, claim.Token)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := l.st.FinishClaim(ctx, tx, claim.VM.ID, claim.Token, backoff); err != nil {
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
