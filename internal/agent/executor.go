package agent

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// intentKey orders intents for one VM: a tombstone dominates everything;
// otherwise (epoch, revision) lexicographically. A newer or equal key is acted
// upon; a strictly older key is dropped.
type intentKey struct {
	deleted bool
	epoch   int64
	rev     int64
}

func keyOf(in *vmcv1.Intent) intentKey {
	return intentKey{deleted: in.GetDeleted(), epoch: in.GetPlacementEpoch(), rev: in.GetDesiredRevision()}
}

// supersedes reports whether a is strictly newer than b, so an incoming intent
// keyed b must be dropped. Equal keys do not supersede: drift repair
// legitimately re-dispatches the current revision.
func (a intentKey) supersedes(b intentKey) bool {
	if a == b {
		return false
	}
	if b.deleted != a.deleted {
		return a.deleted // a is a tombstone, b is not -> b is stale
	}
	if b.epoch != a.epoch {
		return b.epoch < a.epoch
	}
	return b.rev < a.rev
}

func (d *Daemon) pollLoop(ctx context.Context) {
	var sessions []*vmcv1.NodeSession
	for _, s := range d.sessions {
		sessions = append(sessions, s)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(d.cfg.PollInterval):
		}
		if d.halted.Load() || !d.leaseLive() {
			continue
		}
		resp, err := d.client.PollIntents(ctx, &vmcv1.PollIntentsRequest{Sessions: sessions})
		if err != nil {
			continue // transient; next tick retries
		}
		for _, intent := range resp.GetIntents() {
			d.dispatch(ctx, intent)
		}
	}
}

// dispatch hands the intent to the VM's serialized executor.
func (d *Daemon) dispatch(ctx context.Context, intent *vmcv1.Intent) {
	k := keyOf(intent)
	d.mu.Lock()
	// Drop only strictly older work — a stale resync or reordered poll cannot
	// replace newer work — while an equal key (drift repair of the current
	// revision) passes.
	if cur, seen := d.watermark[intent.GetVmId()]; seen && cur.supersedes(k) {
		d.mu.Unlock()
		return
	}
	d.watermark[intent.GetVmId()] = k // equal or advance keeps the max
	ch, ok := d.executors[intent.GetVmId()]
	if !ok {
		ch = make(chan *vmcv1.Intent, 1)
		d.executors[intent.GetVmId()] = ch
		go d.runExecutor(ctx, ch)
	}
	d.mu.Unlock()

	coalescingSend(ch, intent)
}

// coalescingSend delivers in on the VM's serialized-executor channel. The
// channel is latest-wins (capacity 1, drained before send): signals coalesce,
// and the executor only ever sees the newest revision. This is what makes
// "delete supersedes provision" a protocol.
func coalescingSend(ch chan *vmcv1.Intent, in *vmcv1.Intent) {
	for {
		select {
		case ch <- in:
			return
		default:
			select {
			case <-ch: // drop the superseded intent
			default:
			}
		}
	}
}

func (d *Daemon) runExecutor(ctx context.Context, ch chan *vmcv1.Intent) {
	for {
		select {
		case <-ctx.Done():
			return
		case intent := <-ch:
			d.handle(ctx, intent)
		}
	}
}

// handle applies one intent for one VM, in order. It reads as a short pipeline:
// gate, pace, then either tear down or grant-and-apply.
func (d *Daemon) handle(ctx context.Context, intent *vmcv1.Intent) {
	// Fail closed on both an explicit halt and a lapsed local lease: a
	// blackholed heartbeat must not leave us acting past our lease.
	if d.halted.Load() || !d.leaseLive() {
		return
	}
	sess := d.sessions[intent.GetNodeName()]
	if sess == nil {
		return
	}
	log := d.cfg.Log.With("vm", intent.GetVmName(), "epoch", intent.GetPlacementEpoch(), "revision", intent.GetDesiredRevision())

	if !d.waitUntilDue(ctx, intent) {
		return
	}
	if intent.GetDeleted() {
		d.handleTeardown(ctx, sess, intent, log)
		return
	}
	if !d.ensureGrant(ctx, sess, intent, log) {
		return
	}
	d.applyIntent(ctx, sess, intent, log)
}

// waitUntilDue honors the server-controlled pacing, then revalidates the
// watermark. It returns false when a delete or newer revision arrived while we
// waited (revalidate around every pause, not only external steps), or when ctx
// ended.
func (d *Daemon) waitUntilDue(ctx context.Context, intent *vmcv1.Intent) bool {
	if ms := intent.GetNotBeforeMs(); ms > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-d.cfg.Clock.After(time.Duration(ms) * time.Millisecond):
		}
	}
	d.mu.Lock()
	cur, ok := d.watermark[intent.GetVmId()]
	d.mu.Unlock()
	return !ok || cur == keyOf(intent)
}

// handleTeardown destroys this (vm, epoch)'s substrate and reports a receipt.
// Teardown is idempotent and ownership-scoped, so it needs no grant (destroying
// our own artifacts is always safe); the receipt is the proof it happened.
func (d *Daemon) handleTeardown(ctx context.Context, sess *vmcv1.NodeSession, intent *vmcv1.Intent, log *slog.Logger) {
	// Drop the resync working-set entry first so no concurrent resync can
	// redispatch this VM.
	d.mu.Lock()
	delete(d.lastApplied, intent.GetVmId())
	d.mu.Unlock()

	if err := d.cfg.Compute.Teardown(ctx, intent.GetVmId(), intent.GetPlacementEpoch()); err != nil {
		log.Warn("teardown failed", "err", err)
		d.actionFailed(ctx, sess, intent, err)
		return
	}
	if _, err := d.client.TeardownReceipt(ctx, &vmcv1.TeardownReceiptRequest{
		Session: sess, VmId: intent.GetVmId(),
		PlacementEpoch: intent.GetPlacementEpoch(), AttemptToken: intent.GetAttemptToken(),
	}); err != nil {
		log.Warn("teardown receipt failed", "err", err)
	}
}

// ensureGrant obtains (and caches) the execution grant that must commit before
// the first substrate action for this placement. It returns false when the
// grant is denied or transiently unavailable, in which case the caller does
// nothing and the next poll redelivers.
func (d *Daemon) ensureGrant(ctx context.Context, sess *vmcv1.NodeSession, intent *vmcv1.Intent, log *slog.Logger) bool {
	gkey := intent.GetVmId() + "/" + strconv.FormatInt(intent.GetPlacementEpoch(), 10)
	d.mu.Lock()
	haveGrant := d.granted[gkey]
	d.mu.Unlock()
	if haveGrant {
		return true
	}
	resp, err := d.client.RequestGrant(ctx, &vmcv1.RequestGrantRequest{
		Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
	})
	if err != nil {
		return false // transient; the next poll redelivers
	}
	if !resp.GetGranted() {
		log.Info("grant denied — placement is not ours to execute", "reason", resp.GetReason())
		return false
	}
	d.mu.Lock()
	d.granted[gkey] = true
	d.mu.Unlock()
	return true
}

// applyIntent converges the substrate to the intent and reports the observed
// state. It advances the resync working set only on accepted evidence.
func (d *Daemon) applyIntent(ctx context.Context, sess *vmcv1.NodeSession, intent *vmcv1.Intent, log *slog.Logger) {
	spec := intent.GetSpec()
	state, err := d.cfg.Compute.Ensure(ctx, compute.VMConfig{
		VMID: intent.GetVmId(), Name: intent.GetVmName(), Node: intent.GetNodeName(),
		Epoch: intent.GetPlacementEpoch(), CPUs: spec.GetCpus(),
		MemoryBytes: spec.GetMemoryBytes(), Image: spec.GetImage(), RootDiskBytes: spec.GetRootDiskBytes(),
		Network: spec.GetNetwork(),
		Running: spec.GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED,
	})
	if err != nil {
		log.Warn("ensure failed", "err", err)
		d.actionFailed(ctx, sess, intent, err)
		return
	}

	if _, err := d.client.Report(ctx, &vmcv1.ReportRequest{
		Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
		ReportSeq:       d.nextSeq(intent.GetVmId()),
		AppliedRevision: intent.GetDesiredRevision(),
		State:           observedStateProto(state),
	}); err != nil {
		// A server fence (FailedPrecondition) must not seed continued mutation
		// for rejected work: advance the working set only on accepted evidence.
		log.Warn("report rejected — not advancing resync state", "err", err)
		return
	}
	d.mu.Lock()
	// Guard against a tombstone that landed during Ensure: never re-seed a
	// deleted VM into the resync set.
	if cur, ok := d.watermark[intent.GetVmId()]; ok && !cur.deleted {
		d.lastApplied[intent.GetVmId()] = intent
	}
	d.mu.Unlock()
}
