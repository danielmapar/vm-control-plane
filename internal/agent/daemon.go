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
	"os"
	"path/filepath"
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

	halted  atomic.Bool // stale session detected: no NEW substrate actions
	unlock  func()
	started chan struct{} // closed once registration succeeded (tests)
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
		sessions:  map[string]*vmcv1.NodeSession{},
		executors: map[string]chan *vmcv1.Intent{},
		granted:   map[string]bool{},
		seqs:      map[string]*int64{},
		started:   make(chan struct{}),
	}
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

	go d.heartbeatLoop(ctx)
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
	d.cfg.Log.Info("host registered", "host", d.cfg.HostID, "nodes", len(d.sessions))
	return nil
}

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	interval := d.cfg.Lease / 3
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(interval):
		}
		for _, sess := range d.sessions {
			_, err := d.client.Heartbeat(ctx, &vmcv1.HeartbeatRequest{
				Session: sess, Lease: durationpb.New(d.cfg.Lease),
			})
			if err != nil && isFailedPrecondition(err) {
				// A replacement daemon registered: HALT new substrate
				// actions immediately (§6.3, matrix row 10).
				if !d.halted.Swap(true) {
					d.cfg.Log.Error("session superseded — halting substrate actions", "node", sess.GetNodeName())
				}
			}
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
		if d.halted.Load() {
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
	d.mu.Lock()
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
	if d.halted.Load() {
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

	if intent.GetDeleted() {
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
		log.Warn("report rejected", "err", err)
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

// acquireHostLock enforces one daemon per host state dir. O_CREATE|O_EXCL
// is atomic on every platform we run on; a stale lock (dead pid) is stolen.
func acquireHostLock(stateDir string) (func(), error) {
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "vmc-host")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "host.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		// Steal only if the holder is provably dead.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("lock held and unreadable: %w", err)
		}
		pid, _ := strconv.Atoi(firstLine(string(data)))
		if pid > 0 && processAlive(pid) {
			return nil, fmt.Errorf("host lock held by live pid %d", pid)
		}
		_ = os.Remove(path)
	}
	return nil, fmt.Errorf("could not acquire host lock at %s", path)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}
