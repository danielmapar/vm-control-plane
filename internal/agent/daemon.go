// Package agent implements the hypervisor daemon: ONE process per physical
// host (exclusive host lock), advertising N logical nodes, pulling intent
// snapshots, requesting execution grants BEFORE any substrate action,
// executing through per-VM serialized executors, and reporting ordered
// evidence (ADR-0004).
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/sigtunnel/vm-control-plane/internal/clock"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// NodeSpec is one logical node this daemon advertises.
type NodeSpec struct {
	Name        string
	CPUs        int64
	MemoryBytes uint64
	DiskBytes   uint64
	Labels      map[string]string
}

// Config wires a Daemon.
type Config struct {
	HostID       string
	StateDir     string // host lock lives here
	Nodes        []NodeSpec
	HostCPUs     int64
	HostMemory   uint64
	HostDisk     uint64
	Lease        time.Duration
	PollInterval time.Duration
	Clock        clock.Clock
	Compute      compute.Driver
	Log          *slog.Logger
	// DebugAddr, when set, serves the loopback-only debug surface (drift
	// injection for demo 3 on the fake tier). Never exposed beyond
	// 127.0.0.1; refuses non-loopback binds.
	DebugAddr string
}

// Daemon runs the agent loops. All substrate mutation is fenced by: the
// host lock (one daemon per host), session currency (halt on stale), and
// per-placement execution grants.
type Daemon struct {
	cfg    Config
	client vmcv1.AgentServiceClient

	sessions map[string]*vmcv1.NodeSession // node name → session

	mu        sync.Mutex
	executors map[string]chan *vmcv1.Intent // vm id → latest-wins channel
	granted   map[string]bool               // vm/epoch → grant confirmed
	seqs      map[string]*int64             // vm id → report_seq counter
	// lastApplied: the newest non-deleted intent each executor completed —
	// the resync loop's working set for drift detection (plan §4 ⑨). A
	// tombstone REMOVES the entry so resync can never redispatch a deleted
	// VM (batch-review finding [27]).
	lastApplied map[string]*vmcv1.Intent
	// watermark: the highest (tombstone, epoch, revision) seen per VM. The
	// executor acts only on a STRICT advance — last-arrival-wins would let
	// a stale resync redispatch replace a newer power intent (finding [28]).
	watermark map[string]intentKey

	halted atomic.Bool // stale session detected: no NEW substrate actions
	// localLeaseUnix is the conservative deadline (unix nanos) after which
	// the server lease is PRESUMED expired unless a heartbeat renewed it.
	// A blackholed heartbeat therefore halts us instead of letting us act
	// past our lease (batch-review finding [30]).
	localLeaseUnix atomic.Int64
	unlock         func()
	started        chan struct{} // closed once registration succeeded (tests)
}

func New(cfg Config, client vmcv1.AgentServiceClient) *Daemon {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 300 * time.Millisecond
	}
	if cfg.Lease <= 0 {
		cfg.Lease = 15 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Daemon{
		cfg: cfg, client: client,
		sessions:    map[string]*vmcv1.NodeSession{},
		executors:   map[string]chan *vmcv1.Intent{},
		granted:     map[string]bool{},
		seqs:        map[string]*int64{},
		lastApplied: map[string]*vmcv1.Intent{},
		watermark:   map[string]intentKey{},
		started:     make(chan struct{}),
	}
}

// intentKey orders intents for one VM: a tombstone dominates everything;
// otherwise (epoch, revision) lexicographically. Only a strict advance is
// acted upon.
type intentKey struct {
	deleted bool
	epoch   int64
	rev     int64
}

func keyOf(in *vmcv1.Intent) intentKey {
	return intentKey{deleted: in.GetDeleted(), epoch: in.GetPlacementEpoch(), rev: in.GetDesiredRevision()}
}

// staleAgainst reports whether b is STRICTLY OLDER than a (must be
// dropped). An equal key is NOT stale — drift repair legitimately
// re-dispatches the current revision.
func (a intentKey) staleAgainst(b intentKey) bool {
	if a == b {
		return false
	}
	if b.deleted != a.deleted {
		return a.deleted // a is a tombstone, b is not → b is stale
	}
	if b.epoch != a.epoch {
		return b.epoch < a.epoch
	}
	return b.rev < a.rev
}

// Started closes once registration has succeeded (test synchronization).
func (d *Daemon) Started() <-chan struct{} { return d.started }

// Halted reports whether the daemon stopped taking new substrate actions
// (stale session — a replacement daemon registered).
func (d *Daemon) Halted() bool { return d.halted.Load() }

// Run acquires the host lock, registers, and serves until ctx ends.
func (d *Daemon) Run(ctx context.Context) error {
	unlock, err := acquireHostLock(d.cfg.StateDir)
	if err != nil {
		return fmt.Errorf("host lock: %w", err)
	}
	d.unlock = unlock
	defer unlock()

	if err := d.register(ctx); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	close(d.started)

	if err := d.serveDebug(ctx); err != nil {
		return fmt.Errorf("debug surface: %w", err)
	}
	go d.heartbeatLoop(ctx)
	go d.resyncLoop(ctx)
	d.pollLoop(ctx)
	return ctx.Err()
}

func (d *Daemon) register(ctx context.Context) error {
	req := &vmcv1.RegisterHostRequest{
		HostId: d.cfg.HostID,
		Cpus:   d.cfg.HostCPUs, MemoryBytes: d.cfg.HostMemory, DiskBytes: d.cfg.HostDisk,
		Lease: durationpb.New(d.cfg.Lease),
	}
	for _, n := range d.cfg.Nodes {
		req.Nodes = append(req.Nodes, &vmcv1.NodeQuota{
			Name: n.Name, Cpus: n.CPUs, MemoryBytes: n.MemoryBytes,
			DiskBytes: n.DiskBytes, Labels: n.Labels,
		})
	}
	resp, err := d.client.RegisterHost(ctx, req)
	if err != nil {
		return err
	}
	for _, s := range resp.GetSessions() {
		d.sessions[s.GetNodeName()] = s
	}
	d.extendLocalLease()
	d.cfg.Log.Info("host registered", "host", d.cfg.HostID, "nodes", len(d.sessions))
	return nil
}

// extendLocalLease pushes the presumed-expiry deadline out by the lease
// duration, minus a safety margin so we halt BEFORE the server would.
func (d *Daemon) extendLocalLease() {
	margin := d.cfg.Lease / 5
	d.localLeaseUnix.Store(d.cfg.Clock.Now().Add(d.cfg.Lease - margin).UnixNano())
}

// leaseLive reports whether our conservative local lease is still valid.
func (d *Daemon) leaseLive() bool {
	return d.cfg.Clock.Now().UnixNano() < d.localLeaseUnix.Load()
}

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	interval := d.cfg.Lease / 3
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(interval):
		}
		allOK := true
		for _, sess := range d.sessions {
			_, err := d.client.Heartbeat(ctx, &vmcv1.HeartbeatRequest{
				Session: sess, Lease: durationpb.New(d.cfg.Lease),
			})
			if err != nil {
				allOK = false
				if isFailedPrecondition(err) {
					// A replacement daemon registered: HALT immediately.
					if !d.halted.Swap(true) {
						d.cfg.Log.Error("session superseded — halting substrate actions", "node", sess.GetNodeName())
					}
				}
			}
		}
		if allOK {
			d.extendLocalLease()
		}
	}
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

// dispatch hands the intent to the VM's serialized executor. The channel
// is latest-wins (capacity 1, drained before send): signals coalesce, and
// the executor only ever sees the newest revision — the mechanism that
// makes "delete supersedes provision" a protocol (§6.4).
func (d *Daemon) dispatch(ctx context.Context, intent *vmcv1.Intent) {
	k := keyOf(intent)
	d.mu.Lock()
	// Drop only STRICTLY OLDER work — a stale resync or reordered poll
	// cannot replace newer work (finding [28]) — while an equal key
	// (drift repair of the current revision) passes.
	if cur, seen := d.watermark[intent.GetVmId()]; seen && cur.staleAgainst(k) {
		d.mu.Unlock()
		return
	}
	// Record the newest key seen (equal or advance keeps the max).
	d.watermark[intent.GetVmId()] = k
	ch, ok := d.executors[intent.GetVmId()]
	if !ok {
		ch = make(chan *vmcv1.Intent, 1)
		d.executors[intent.GetVmId()] = ch
		go d.runExecutor(ctx, ch)
	}
	d.mu.Unlock()

	for {
		select {
		case ch <- intent:
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

func (d *Daemon) handle(ctx context.Context, intent *vmcv1.Intent) {
	// Fail closed on BOTH an explicit halt and a lapsed local lease: a
	// blackholed heartbeat must not leave us acting past our lease (finding [30]).
	if d.halted.Load() || !d.leaseLive() {
		return
	}
	sess := d.sessions[intent.GetNodeName()]
	if sess == nil {
		return
	}
	log := d.cfg.Log.With("vm", intent.GetVmName(), "epoch", intent.GetPlacementEpoch(), "revision", intent.GetDesiredRevision())

	// Honor server-controlled pacing (durable retry state lives there).
	if ms := intent.GetNotBeforeMs(); ms > 0 {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(time.Duration(ms) * time.Millisecond):
		}
	}
	// Revalidate the watermark AFTER the wait: a delete or newer revision
	// may have arrived while we slept (finding [28] — revalidate around
	// every pause, not just external steps).
	d.mu.Lock()
	if cur, ok := d.watermark[intent.GetVmId()]; ok && cur != keyOf(intent) {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	if intent.GetDeleted() {
		// A tombstone supersedes provisioning: drop the resync working-set
		// entry FIRST so no concurrent resync can redispatch this VM
		// (finding [27]).
		d.mu.Lock()
		delete(d.lastApplied, intent.GetVmId())
		d.mu.Unlock()
		// Teardown is idempotent and ownership-scoped; it needs no grant
		// (destroying our own artifacts is always safe), but the receipt
		// proves it happened.
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
		return
	}

	// Grant before FIRST substrate action for this placement (§6.2).
	gkey := intent.GetVmId() + "/" + strconv.FormatInt(intent.GetPlacementEpoch(), 10)
	d.mu.Lock()
	haveGrant := d.granted[gkey]
	d.mu.Unlock()
	if !haveGrant {
		resp, err := d.client.RequestGrant(ctx, &vmcv1.RequestGrantRequest{
			Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
		})
		if err != nil {
			return // transient; the next poll redelivers
		}
		if !resp.GetGranted() {
			log.Info("grant denied — placement is not ours to execute", "reason", resp.GetReason())
			return
		}
		d.mu.Lock()
		d.granted[gkey] = true
		d.mu.Unlock()
	}

	spec := intent.GetSpec()
	state, err := d.cfg.Compute.Ensure(ctx, compute.VMConfig{
		VMID: intent.GetVmId(), Name: intent.GetVmName(), Node: intent.GetNodeName(),
		Epoch: intent.GetPlacementEpoch(), CPUs: spec.GetCpus(),
		MemoryB: spec.GetMemoryBytes(), Image: spec.GetImage(), DiskB: spec.GetRootDiskBytes(),
		Network: spec.GetNetwork(),
		Running: spec.GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED,
	})
	if err != nil {
		log.Warn("ensure failed", "err", err)
		d.actionFailed(ctx, sess, intent, err)
		return
	}

	stateProto := map[compute.State]vmcv1.ObservedState{
		compute.StateRunning: vmcv1.ObservedState_OBSERVED_STATE_RUNNING,
		compute.StateShutoff: vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF,
		compute.StateAbsent:  vmcv1.ObservedState_OBSERVED_STATE_ABSENT,
	}[state]

	if _, err := d.client.Report(ctx, &vmcv1.ReportRequest{
		Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
		ReportSeq:       d.nextSeq(intent.GetVmId()),
		AppliedRevision: intent.GetDesiredRevision(),
		State:           stateProto,
	}); err != nil {
		// A server fence (FailedPrecondition) must NOT seed continued
		// mutation for rejected work — only advance the working set on
		// accepted evidence (finding [31]).
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

// resyncLoop is the drift detector (§4 ⑨): it periodically OBSERVES every
// VM this daemon has realized and (a) reports the evidence, (b) re-ensures
// through the executor when observation diverges from the applied intent —
// drift repair IS provisioning, one code path.
func (d *Daemon) resyncLoop(ctx context.Context) {
	interval := d.cfg.PollInterval * 4
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(interval):
		}
		if d.halted.Load() {
			continue
		}
		d.mu.Lock()
		snapshot := make([]*vmcv1.Intent, 0, len(d.lastApplied))
		for _, in := range d.lastApplied {
			snapshot = append(snapshot, in)
		}
		d.mu.Unlock()

		for _, intent := range snapshot {
			state, err := d.cfg.Compute.Observe(ctx, intent.GetVmId(), intent.GetPlacementEpoch())
			if err != nil {
				continue
			}
			wantRunning := intent.GetSpec().GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED
			drifted := (wantRunning && state != compute.StateRunning) ||
				(!wantRunning && state != compute.StateShutoff)
			sess := d.sessions[intent.GetNodeName()]
			if sess == nil {
				continue
			}
			stateProto := map[compute.State]vmcv1.ObservedState{
				compute.StateRunning: vmcv1.ObservedState_OBSERVED_STATE_RUNNING,
				compute.StateShutoff: vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF,
				compute.StateAbsent:  vmcv1.ObservedState_OBSERVED_STATE_ABSENT,
			}[state]
			_, _ = d.client.Report(ctx, &vmcv1.ReportRequest{
				Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
				ReportSeq:       d.nextSeq(intent.GetVmId()),
				AppliedRevision: intent.GetDesiredRevision(),
				State:           stateProto,
				Detail:          map[bool]string{true: "drift-detected", false: ""}[drifted],
			})
			if drifted {
				d.cfg.Log.Warn("drift detected — re-ensuring through the executor",
					"vm", intent.GetVmName(), "observed", string(state))
				d.dispatch(ctx, intent)
			}
		}
	}
}

func (d *Daemon) actionFailed(ctx context.Context, sess *vmcv1.NodeSession, intent *vmcv1.Intent, cause error) {
	_, _ = d.client.ActionFailed(ctx, &vmcv1.ActionFailedRequest{
		Session: sess, VmId: intent.GetVmId(),
		PlacementEpoch: intent.GetPlacementEpoch(), DesiredRevision: intent.GetDesiredRevision(),
		AttemptToken: intent.GetAttemptToken(), Error: cause.Error(), ErrorClass: "transient",
	})
}

func (d *Daemon) nextSeq(vmID string) int64 {
	d.mu.Lock()
	counter, ok := d.seqs[vmID]
	if !ok {
		var v int64
		counter = &v
		d.seqs[vmID] = counter
	}
	d.mu.Unlock()
	return atomic.AddInt64(counter, 1)
}
