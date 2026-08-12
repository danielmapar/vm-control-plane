package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func spec() *vmcv1.VmSpec {
	return &vmcv1.VmSpec{
		Cpus:          2,
		MemoryBytes:   2 << 30,
		Image:         "ubuntu-24.04",
		RootDiskBytes: 10 << 30,
		Power:         vmcv1.PowerState_POWER_STATE_RUNNING,
	}
}

func TestCreateGetRoundTrip(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	created, err := s.CreateVM(ctx, nil, uuid.New(), "web-1", spec())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.SpecGeneration != 1 || created.ResourceVersion != 1 || created.DesiredRevision != 1 {
		t.Fatalf("fresh row versions wrong: %+v", created)
	}

	got, err := s.GetVM(ctx, nil, "web-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Cpus != 2 || got.Spec.Image != "ubuntu-24.04" {
		t.Fatalf("spec did not round-trip: %+v", got.Spec)
	}
	if got.Phase != "PENDING" {
		t.Fatalf("phase = %q, want PENDING", got.Phase)
	}
}

func TestDuplicateName(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	if _, err := s.CreateVM(ctx, nil, uuid.New(), "dup", spec()); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateVM(ctx, nil, uuid.New(), "dup", spec())
	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

// TestStatusCASOneWinner is the concurrency contract: two writers read the
// same resource_version; exactly one commit succeeds and the loser gets
// ErrStaleWrite — never a silent overwrite.
func TestStatusCASOneWinner(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	vm, err := s.CreateVM(ctx, nil, uuid.New(), "cas-1", spec())
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := &vmcv1.VmStatus{Phase: vmcv1.Phase_PHASE_SCHEDULING}
			_, errs[i] = s.UpdateVMStatus(ctx, nil, vm.ID, vm.ResourceVersion, "SCHEDULING", st)
		}(i)
	}
	wg.Wait()

	winners, stale := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, store.ErrStaleWrite):
			stale++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("want exactly one winner and one stale, got winners=%d stale=%d", winners, stale)
	}
}

func TestStaleVsMissingDisambiguation(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	_, err := s.UpdateVMStatus(ctx, nil, uuid.New(), 1, "RUNNING", &vmcv1.VmStatus{})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing row: want ErrNotFound, got %v", err)
	}

	vm, err := s.CreateVM(ctx, nil, uuid.New(), "stale-1", spec())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateVMStatus(ctx, nil, vm.ID, vm.ResourceVersion+99, "RUNNING", &vmcv1.VmStatus{})
	if !errors.Is(err, store.ErrStaleWrite) {
		t.Fatalf("stale version: want ErrStaleWrite, got %v", err)
	}
}

// TestTombstoneSemantics: set-once, revision bump exactly once, row persists.
func TestTombstoneSemantics(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	vm, err := s.CreateVM(ctx, nil, uuid.New(), "del-1", spec())
	if err != nil {
		t.Fatal(err)
	}

	dead, err := s.TombstoneVM(ctx, nil, vm.ID, vm.ResourceVersion)
	if err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	if dead.DeletedAt == nil {
		t.Fatalf("tombstone not applied: %+v", dead)
	}
	if dead.Phase != vm.Phase {
		t.Fatalf("tombstone must NOT touch reconciler-owned phase: %q -> %q", vm.Phase, dead.Phase)
	}
	if dead.DesiredRevision != vm.DesiredRevision+1 {
		t.Fatalf("desired_revision: got %d, want %d", dead.DesiredRevision, vm.DesiredRevision+1)
	}

	// Second tombstone: a TRUE no-op — same revision, same version, same
	// timestamp; no CAS conflict even with a stale expected version.
	again, err := s.TombstoneVM(ctx, nil, dead.ID, vm.ResourceVersion)
	if err != nil {
		t.Fatalf("second tombstone: %v", err)
	}
	if again.DesiredRevision != dead.DesiredRevision || again.ResourceVersion != dead.ResourceVersion {
		t.Fatalf("second tombstone mutated the row: %+v vs %+v", again, dead)
	}
	if !again.DeletedAt.Equal(*dead.DeletedAt) {
		t.Fatalf("deleted_at changed on second tombstone")
	}

	// The row is still readable: deletion is a state, not an absence.
	if _, err := s.GetVM(ctx, nil, "del-1"); err != nil {
		t.Fatalf("tombstoned row must remain readable: %v", err)
	}
}

func TestListKeysetPagination(t *testing.T) {
	s := store.New(pgtest.NewDB(t))
	ctx := context.Background()

	for _, n := range []string{"a-1", "b-1", "c-1"} {
		if _, err := s.CreateVM(ctx, nil, uuid.New(), n, spec()); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := s.ListVMs(ctx, nil, "", 2)
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1: %v len=%d", err, len(page1))
	}
	page2, err := s.ListVMs(ctx, nil, page1[1].Name, 2)
	if err != nil || len(page2) != 1 || page2[0].Name != "c-1" {
		t.Fatalf("page2: %v %+v", err, page2)
	}
}

// Guard: pgtest instances must be package-isolated — parallel packages get
// distinct ports/data dirs. This test simply asserts the harness booted with
// a live pool quickly (a shared-data-dir collision hangs or errors here).
func TestHarnessBootHealthy(t *testing.T) {
	pool := pgtest.NewDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("harness not healthy: %v", err)
	}
}
