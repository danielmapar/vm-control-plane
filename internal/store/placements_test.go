package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// placeOnce claims the VM and runs PlaceVM inside the claim-guarded
// transaction — the exact shape the controller uses.
func placeOnce(ctx context.Context, s *store.Store, vmID uuid.UUID, node *store.Node, res store.Resources) (int64, error) {
	claims, err := s.ClaimDirtyVMs(ctx, "sched-test", time.Minute, 50)
	if err != nil {
		return 0, err
	}
	var claim *store.Claim
	for _, c := range claims {
		if c.VM.ID == vmID {
			claim = c
			break
		}
	}
	if claim == nil {
		return 0, errors.New("vm not claimable")
	}
	tx, err := s.CompleteClaimTx(ctx, vmID, claim.Token, time.Hour)
	if err != nil {
		return 0, err
	}
	epoch, err := s.PlaceVM(ctx, tx, vmID, node, res)
	if err != nil {
		_ = tx.Rollback(ctx)
		return 0, err
	}
	if err := s.FinishClaim(ctx, tx, vmID, claim.Token, time.Hour); err != nil {
		_ = tx.Rollback(ctx)
		return 0, err
	}
	return epoch, tx.Commit(ctx)
}

func TestPlaceReservesAndBumpsEpoch(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	if _, err := s.RegisterHost(ctx, host("h1"), twoNodes(), time.Minute); err != nil {
		t.Fatal(err)
	}
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "pl-1", spec())
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}

	epoch, err := placeOnce(ctx, s, vm.ID, node, store.Resources{CPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if epoch != 1 {
		t.Fatalf("epoch = %d, want 1", epoch)
	}

	placed, err := s.GetVM(ctx, nil, "pl-1")
	if err != nil || placed.Phase != "PROVISIONING" || *placed.NodeName != "node-a" || placed.PlacementEpoch != 1 {
		t.Fatalf("vm after place: %v %+v", err, placed)
	}
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 2 {
		t.Fatalf("reservation counters: %v %+v", err, n)
	}
}

// TestConcurrentPlacementNoOversubscription: matrix row 5. Node capacity
// fits ONE of the two concurrent requests; the single-statement conditional
// reservation must admit exactly one.
func TestConcurrentPlacementNoOversubscription(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	quota := []store.NodeQuota{{Name: "small", CPUs: 3, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}}
	if _, err := s.RegisterHost(ctx, store.HostCapacity{HostID: "h1", CPUs: 4, MemoryBytes: 16 << 30, DiskBytes: 200 << 30}, quota, time.Minute); err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "small")
	if err != nil {
		t.Fatal(err)
	}

	vmA, err := s.CreateVM(ctx, nil, uuid.New(), "race-a", spec())
	if err != nil {
		t.Fatal(err)
	}
	vmB, err := s.CreateVM(ctx, nil, uuid.New(), "race-b", spec())
	if err != nil {
		t.Fatal(err)
	}

	_, _ = vmA, vmB
	res := store.Resources{CPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each worker claims ONE dirty row (SKIP LOCKED hands them
			// different rows) and places whatever it claimed on the same
			// small node — the contention is on the node counters.
			var claim *store.Claim
			for attempt := 0; attempt < 50; attempt++ {
				claims, err := s.ClaimDirtyVMs(ctx, "w", time.Minute, 1)
				if err != nil {
					errs[i] = err
					return
				}
				if len(claims) == 1 {
					claim = claims[0]
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if claim == nil {
				errs[i] = errors.New("no claimable row")
				return
			}
			tx, err := s.CompleteClaimTx(ctx, claim.VM.ID, claim.Token, time.Hour)
			if err != nil {
				errs[i] = err
				return
			}
			if _, err := s.PlaceVM(ctx, tx, claim.VM.ID, node, res); err != nil {
				_ = tx.Rollback(ctx)
				errs[i] = err
				return
			}
			if err := s.FinishClaim(ctx, tx, claim.VM.ID, claim.Token, time.Hour); err != nil {
				_ = tx.Rollback(ctx)
				errs[i] = err
				return
			}
			errs[i] = tx.Commit(ctx)
		}(i)
	}
	wg.Wait()

	placedCount, capacityFailures := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			placedCount++
		case errors.Is(err, store.ErrNoCapacity):
			capacityFailures++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if placedCount != 1 || capacityFailures != 1 {
		t.Fatalf("want exactly one placement and one capacity rejection, got %d/%d", placedCount, capacityFailures)
	}
	n, err := s.GetNode(ctx, "small")
	if err != nil || n.ReservedCPUs != 2 {
		t.Fatalf("no oversubscription allowed: reserved=%d", n.ReservedCPUs)
	}
}

// TestOneActivePlacementSchemaEnforced: the partial unique index — not
// code — forbids a second active placement.
func TestOneActivePlacementSchemaEnforced(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()
	pool := s.Pool()

	vmID := uuid.New()
	if _, err := s.CreateVM(ctx, nil, vmID, "pl-uniq", spec()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO placements (vm_id, epoch, node_name, host_id, state)
		VALUES ($1, 1, 'n1', 'h1', 'assigned')`, vmID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO placements (vm_id, epoch, node_name, host_id, state)
		VALUES ($1, 2, 'n2', 'h1', 'assigned')`, vmID)
	if err == nil {
		t.Fatal("second active placement must violate the partial unique index")
	}
	// After teardown, a new epoch is admissible.
	if _, err := pool.Exec(ctx, `UPDATE placements SET state='torn_down' WHERE vm_id=$1`, vmID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO placements (vm_id, epoch, node_name, host_id, state)
		VALUES ($1, 2, 'n2', 'h1', 'assigned')`, vmID); err != nil {
		t.Fatalf("post-teardown placement must be admissible: %v", err)
	}
}

// TestReleaseIdempotent: double release adjusts counters exactly once.
func TestReleaseIdempotent(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	if _, err := s.RegisterHost(ctx, host("h1"), twoNodes(), time.Minute); err != nil {
		t.Fatal(err)
	}
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "pl-rel", spec())
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	res := store.Resources{CPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30}
	epoch, err := placeOnce(ctx, s, vm.ID, node, res)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		tx, err := s.Pool().Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ReleasePlacement(ctx, tx, vm.ID, epoch); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 0 || n.ReservedMemory != 0 {
		t.Fatalf("double release must adjust exactly once: %+v", n)
	}
	p, err := s.GetPlacement(ctx, nil, vm.ID, epoch)
	if err != nil || p.State != "torn_down" {
		t.Fatalf("placement state: %v %+v", err, p)
	}
}

// TestExpiredLeaseRejectsReservation: the reservation statement itself
// enforces node liveness — a filter working from a stale snapshot cannot
// place onto a dead node.
func TestExpiredLeaseRejectsReservation(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	if _, err := s.RegisterHost(ctx, host("h1"), twoNodes(), 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "pl-dead", spec())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond) // lease expires

	_, err = placeOnce(ctx, s, vm.ID, node, store.Resources{CPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 5 << 30})
	if !errors.Is(err, store.ErrNoCapacity) {
		t.Fatalf("placement on expired-lease node: want ErrNoCapacity, got %v", err)
	}
}
