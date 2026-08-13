// Package agent implements the hypervisor daemon: one process per physical
// host (holding an exclusive host lock), advertising N logical nodes, pulling
// intent snapshots, requesting execution grants before any substrate action,
// executing through per-VM serialized executors, and reporting ordered
// evidence.
package agent

import (
	"context"
	"fmt"
	"log/slog"
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
	// injection on the fake tier). It refuses to bind to any non-loopback
	// address.
	DebugAddr string
}

// Daemon runs the agent loops. All substrate mutation is fenced by the host
// lock (one daemon per host), session currency (halt on stale), and
// per-placement execution grants.
type Daemon struct {
	cfg    Config
	client vmcv1.AgentServiceClient

	sessions map[string]*vmcv1.NodeSession // node name -> session

	mu        sync.Mutex
	executors map[string]chan *vmcv1.Intent // vm id -> latest-wins channel
	granted   map[string]bool               // vm/epoch -> grant confirmed
	seqs      map[string]*int64             // vm id -> report_seq counter
	// lastApplied is the newest non-deleted intent each executor completed:
	// the resync loop's working set for drift detection. A tombstone removes
	// the entry so resync can never redispatch a deleted VM.
	lastApplied map[string]*vmcv1.Intent
	// watermark is the highest (tombstone, epoch, revision) seen per VM. A
	// strictly older key is dropped; an equal key still runs (drift repair of
	// the current revision). Last-arrival-wins would instead let a stale resync
	// redispatch replace a newer power intent.
	watermark map[string]intentKey

	halted atomic.Bool // stale session detected: take no new substrate actions
	// localLeaseUnix is the conservative deadline (unix nanos) after which the
	// server lease is presumed expired unless a heartbeat renewed it. A
	// blackholed heartbeat therefore halts us before we would act past the
	// lease the server still honors.
	localLeaseUnix atomic.Int64
	unlock         func()
	started        chan struct{} // closed once registration succeeded (tests)
}

// New returns a Daemon with defaults applied to any unset Config field.
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

// Started closes once registration has succeeded (test synchronization).
func (d *Daemon) Started() <-chan struct{} { return d.started }

// Halted reports whether the daemon stopped taking new substrate actions
// (a stale session, meaning a replacement daemon registered).
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
// duration, minus a safety margin so we halt before the server would.
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
					// A replacement daemon registered: halt immediately.
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
