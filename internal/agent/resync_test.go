package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sigtunnel/vm-control-plane/internal/clock"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/fake"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// TestResyncDetectsAndRepairsDrift: the fake-tier virsh-destroy analog.
// DriftStop stops the domain out of band; the next resync observes SHUTOFF,
// reports it (drift evidence), and re-ensures through the executor — the
// domain returns to running with a follow-up RUNNING report.
func TestResyncDetectsAndRepairsDrift(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	fk := clock.NewFake(time.Now())

	d := New(Config{
		HostID: "h-test", StateDir: t.TempDir(),
		Compute: drv, Clock: fk, PollInterval: 100 * time.Millisecond,
	}, fc)
	d.sessions["node-a"] = &vmcv1.NodeSession{NodeName: "node-a", SessionId: "6e9f9d5e-0000-4000-8000-000000000001", SessionGeneration: 1}
	d.extendLocalLease()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.resyncLoop(ctx)

	// Provision revision 1.
	d.dispatch(ctx, intent("vm1", 1, false))
	first := <-fc.reports
	if first.GetState() != vmcv1.ObservedState_OBSERVED_STATE_RUNNING {
		t.Fatalf("initial report: %+v", first)
	}

	// Out-of-band stop, then advance the fake clock to fire the resync.
	if !drv.DriftStop("vm1", 1) {
		t.Fatal("drift injection failed")
	}
	fk.Advance(time.Second)

	// The resync must (a) report the drift evidence, then (b) re-ensure,
	// which produces a RUNNING report from the executor.
	sawDrift, sawRepair := false, false
	deadline := time.After(10 * time.Second)
	for !sawDrift || !sawRepair {
		select {
		case r := <-fc.reports:
			if r.GetState() == vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF && r.GetDetail() == "drift-detected" {
				sawDrift = true
			}
			if sawDrift && r.GetState() == vmcv1.ObservedState_OBSERVED_STATE_RUNNING {
				sawRepair = true
			}
		case <-time.After(200 * time.Millisecond):
			fk.Advance(time.Second) // keep resync ticking
		case <-deadline:
			t.Fatalf("drift/repair not observed (drift=%v repair=%v)", sawDrift, sawRepair)
		}
	}

	// The domain is genuinely running again.
	state, err := drv.Observe(ctx, "vm1", 1)
	if err != nil || string(state) != "RUNNING" {
		t.Fatalf("post-repair observe: %v %s", err, state)
	}
}

// TestHeartbeatStaleSessionHalts: matrix row 10 — a FailedPrecondition
// heartbeat (replacement daemon registered) halts new substrate actions.
func TestHeartbeatStaleSessionHalts(t *testing.T) {
	fc := newFakeClient()
	fc.heartbeatStale = true
	fk := clock.NewFake(time.Now())
	d := New(Config{
		HostID: "h-test", StateDir: t.TempDir(),
		Compute: fake.New(), Clock: fk, Lease: 3 * time.Second,
	}, fc)
	d.sessions["node-a"] = &vmcv1.NodeSession{NodeName: "node-a", SessionId: "6e9f9d5e-0000-4000-8000-000000000001", SessionGeneration: 1}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.heartbeatLoop(ctx)

	deadline := time.After(10 * time.Second)
	for !d.Halted() {
		select {
		case <-deadline:
			t.Fatal("stale heartbeat did not halt the daemon")
		default:
			fk.Advance(time.Second)
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// TestTombstoneNotResurrectedByResync: batch-review finding [27] — after a
// delete, the resync loop must NEVER redispatch the old intent. The
// tombstone removes the working-set entry, so Observe sees ABSENT and does
// nothing; the domain stays gone across multiple resync periods.
func TestTombstoneNotResurrectedByResync(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	fk := clock.NewFake(time.Now())
	d := New(Config{
		HostID: "h-test", StateDir: t.TempDir(),
		Compute: drv, Clock: fk, PollInterval: 100 * time.Millisecond,
	}, fc)
	d.sessions["node-a"] = &vmcv1.NodeSession{NodeName: "node-a", SessionId: "6e9f9d5e-0000-4000-8000-000000000001", SessionGeneration: 1}
	d.extendLocalLease()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.resyncLoop(ctx)

	// Provision, then delete.
	d.dispatch(ctx, intent("vm1", 1, false))
	<-fc.reports
	if drv.Count() != 1 {
		t.Fatalf("domain not created: %d", drv.Count())
	}
	d.dispatch(ctx, intent("vm1", 2, true)) // tombstone (epoch 1, rev 2, deleted)
	<-fc.receipts
	if drv.Count() != 0 {
		t.Fatalf("teardown did not remove domain: %d", drv.Count())
	}

	// Drive many resync periods: the domain must stay absent.
	for i := 0; i < 5; i++ {
		fk.Advance(time.Second)
		time.Sleep(50 * time.Millisecond)
	}
	if drv.Count() != 0 {
		t.Fatalf("resync resurrected a deleted VM: %d domains", drv.Count())
	}
	// And a stale re-dispatch of the OLD (pre-delete) intent is dropped.
	d.dispatch(ctx, intent("vm1", 1, false))
	time.Sleep(200 * time.Millisecond)
	if drv.Count() != 0 {
		t.Fatal("a stale pre-delete intent must not resurrect the VM")
	}
}
