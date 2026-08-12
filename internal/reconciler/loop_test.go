package reconciler_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/reconciler"
	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func fixture(t *testing.T) (*store.Store, store.Session) {
	t.Helper()
	s := store.New(pgtest.NewDB(t))
	sessions, err := s.RegisterHost(context.Background(),
		store.HostCapacity{HostID: "h1", CPUs: 16, MemoryBytes: 32 << 30, DiskBytes: 500 << 30},
		[]store.NodeQuota{
			{Name: "node-a", CPUs: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30},
			{Name: "node-b", CPUs: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30},
		}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range sessions {
		if sess.NodeName == "node-a" {
			return s, sess
		}
	}
	t.Fatal("node-a session missing")
	return nil, store.Session{}
}

func createVM(t *testing.T, s *store.Store, name string) (*store.VM, *store.Operation) {
	t.Helper()
	ctx := context.Background()
	vm, err := s.CreateVM(ctx, nil, uuid.New(), name, &vmcv1.VmSpec{
		Cpus: 2, MemoryBytes: 2 << 30, Image: "ubuntu-24.04", RootDiskBytes: 10 << 30,
		Power: vmcv1.PowerState_POWER_STATE_RUNNING,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateOperation(ctx, nil, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID, ResourceName: name,
		Verb: "CREATE", TargetRevision: vm.DesiredRevision,
		Deadline: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return vm, op
}

func runLoop(t *testing.T, s *store.Store) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	loop := reconciler.New(s, reconciler.Config{
		Owner: "test-loop", Tick: 50 * time.Millisecond,
		ResyncWait: 50 * time.Millisecond, PendingWait: 100 * time.Millisecond,
		Log: quietLog(),
	})
	go loop.Run(ctx)
	return cancel
}

//nolint:unparam // timeout varies as slower scenarios land
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestLoopSchedulesAndConverges: the vertical slice at store level — the
// loop places, a simulated daemon grants + reports, the loop converges the
// phase and terminalizes the CREATE operation as DONE.
func TestLoopSchedulesAndConverges(t *testing.T) {
	s, sess := fixture(t)
	ctx := context.Background()
	vm, op := createVM(t, s, "loop-1")

	cancel := runLoop(t, s)
	defer cancel()

	// Loop places the VM.
	waitFor(t, "placement", 10*time.Second, func() bool {
		got, err := s.GetVM(ctx, nil, "loop-1")
		return err == nil && got.Phase == "PROVISIONING" && got.PlacementEpoch == 1
	})

	got, err := s.GetVM(ctx, nil, "loop-1")
	if err != nil {
		t.Fatal(err)
	}
	sessForNode := sess
	if *got.NodeName != "node-a" {
		// Placed on node-b; grab its session instead.
		t.Fatalf("expected least-allocated placement on node-a first, got %s", *got.NodeName)
	}

	// Simulated daemon: grant, then evidence.
	if err := s.GrantExecution(ctx, sessForNode, vm.ID, got.PlacementEpoch); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyReport(ctx, store.Report{
		Session: sessForNode, VMID: vm.ID, Epoch: got.PlacementEpoch,
		Seq: 1, AppliedRevision: vm.DesiredRevision, State: "RUNNING",
	}); err != nil {
		t.Fatal(err)
	}

	// Loop converges and terminalizes.
	waitFor(t, "convergence", 10*time.Second, func() bool {
		got, err := s.GetVM(ctx, nil, "loop-1")
		return err == nil && got.Phase == "RUNNING"
	})
	waitFor(t, "operation DONE", 10*time.Second, func() bool {
		final, err := s.GetOperation(ctx, nil, op.ID)
		return err == nil && final.State == "DONE"
	})
}

// TestLoopUnschedulableCondition: no candidates → condition surfaced, no
// hot loop (phase stays PENDING, operation stays open until its deadline).
func TestLoopUnschedulableCondition(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "huge", &vmcv1.VmSpec{
		Cpus: 64, MemoryBytes: 200 << 30, Image: "x", RootDiskBytes: 10 << 30,
		Power: vmcv1.PowerState_POWER_STATE_RUNNING,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = vm

	cancel := runLoop(t, s)
	defer cancel()

	waitFor(t, "Unschedulable condition", 10*time.Second, func() bool {
		got, err := s.GetVM(ctx, nil, "huge")
		if err != nil || got.Status == nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Unschedulable" && c.Active {
				return true
			}
		}
		return false
	})
	got, err := s.GetVM(ctx, nil, "huge")
	if err != nil || got.Phase != "PENDING" {
		t.Fatalf("unschedulable VM must stay PENDING: %v %s", err, got.Phase)
	}
}

// TestLoopDeletionFinalizes: tombstone → (simulated) teardown receipt →
// finalization removes the row and completes the DELETE operation, which
// remains queryable (matrix rows 15–16 foundation).
func TestLoopDeletionFinalizes(t *testing.T) {
	s, sess := fixture(t)
	ctx := context.Background()
	vm, _ := createVM(t, s, "del-loop")

	cancel := runLoop(t, s)
	defer cancel()

	waitFor(t, "placement", 10*time.Second, func() bool {
		got, err := s.GetVM(ctx, nil, "del-loop")
		return err == nil && got.PlacementEpoch == 1
	})
	if err := s.GrantExecution(ctx, sess, vm.ID, 1); err != nil {
		t.Fatal(err)
	}

	// Tombstone (as the API would).
	fresh, err := s.GetVM(ctx, nil, "del-loop")
	if err != nil {
		t.Fatal(err)
	}
	dead, err := s.TombstoneVM(ctx, nil, vm.ID, fresh.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	delOp, err := s.CreateOperation(ctx, nil, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID, ResourceName: "del-loop",
		Verb: "DELETE", TargetRevision: dead.DesiredRevision,
		Deadline: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The row persists while teardown is unproven.
	time.Sleep(300 * time.Millisecond)
	if _, err := s.GetVM(ctx, nil, "del-loop"); err != nil {
		t.Fatalf("Deleting row must persist until teardown is proven: %v", err)
	}

	// Simulated daemon teardown receipt.
	if err := s.MarkTeardownComplete(ctx, vm.ID, 1); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "finalization", 10*time.Second, func() bool {
		_, err := s.GetVM(ctx, nil, "del-loop")
		return err != nil // row gone
	})
	waitFor(t, "delete op DONE", 10*time.Second, func() bool {
		final, err := s.GetOperation(ctx, nil, delOp.ID)
		return err == nil && final.State == "DONE"
	})
	// Capacity released.
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 0 {
		t.Fatalf("capacity after finalize: %+v", n)
	}
}

// TestLoopSupersedesOlderOperations: a newer revision realized first moves
// older open operations to SUPERSEDED — op wait never hangs (D3).
func TestLoopSupersedesOlderOperations(t *testing.T) {
	s, sess := fixture(t)
	ctx := context.Background()
	vm, createOp := createVM(t, s, "sup-1")

	cancel := runLoop(t, s)
	defer cancel()

	waitFor(t, "placement", 10*time.Second, func() bool {
		got, err := s.GetVM(ctx, nil, "sup-1")
		return err == nil && got.PlacementEpoch == 1
	})
	if err := s.GrantExecution(ctx, sess, vm.ID, 1); err != nil {
		t.Fatal(err)
	}

	// Bump the desired revision before revision 1 was realized (simulating
	// a power update racing the create).
	if _, err := s.Pool().Exec(ctx, `
		UPDATE vms SET desired_revision = desired_revision + 1,
			spec_generation = spec_generation + 1,
			resource_version = resource_version + 1
		WHERE id=$1`, vm.ID); err != nil {
		t.Fatal(err)
	}

	// The daemon reports evidence for revision 2 (latest-wins executor).
	if err := s.ApplyReport(ctx, store.Report{
		Session: sess, VMID: vm.ID, Epoch: 1, Seq: 1,
		AppliedRevision: 2, State: "RUNNING",
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "create op SUPERSEDED", 10*time.Second, func() bool {
		final, err := s.GetOperation(ctx, nil, createOp.ID)
		return err == nil && final.State == "SUPERSEDED"
	})
}
