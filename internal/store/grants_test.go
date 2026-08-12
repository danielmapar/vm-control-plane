package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// grantFixture: registered host with two nodes, one placed VM.
func grantFixture(t *testing.T) (*store.Store, store.Session, *store.VM, int64) {
	t.Helper()
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	sessions, err := s.RegisterHost(ctx, host("h1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var sessA store.Session
	for _, sess := range sessions {
		if sess.NodeName == "node-a" {
			sessA = sess
		}
	}

	vm, err := s.CreateVM(ctx, nil, uuid.New(), "gr-1", spec())
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := placeOnce(ctx, s, vm.ID, node, store.Resources{CPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30})
	if err != nil {
		t.Fatal(err)
	}
	return s, sessA, vm, epoch
}

func TestGrantHappyPathAndReplay(t *testing.T) {
	s, sess, vm, epoch := grantFixture(t)
	ctx := context.Background()

	if err := s.GrantExecution(ctx, sess, vm.ID, epoch); err != nil {
		t.Fatalf("grant: %v", err)
	}
	p, err := s.GetPlacement(ctx, nil, vm.ID, epoch)
	if err != nil || p.State != "granted" {
		t.Fatalf("placement: %v %+v", err, p)
	}
	// Matrix row 6: the grant response was lost; replay is idempotent.
	if err := s.GrantExecution(ctx, sess, vm.ID, epoch); err != nil {
		t.Fatalf("grant replay: %v", err)
	}
}

// TestGrantAfterSessionReplacement: matrix row 7 — a delayed grant from
// the OLD daemon fails admission after a replacement registered.
func TestGrantAfterSessionReplacement(t *testing.T) {
	s, oldSess, vm, epoch := grantFixture(t)
	ctx := context.Background()

	if _, err := s.RegisterHost(ctx, host("h1"), twoNodes(), time.Minute); err != nil {
		t.Fatal(err)
	}

	err := s.GrantExecution(ctx, oldSess, vm.ID, epoch)
	var denied *store.ErrGrantDenied
	if !errors.As(err, &denied) || denied.Reason != store.DenyStaleSession {
		t.Fatalf("want stale-session denial, got %v", err)
	}
}

// TestGrantAfterLeaseExpiry: matrix row 7 — no heartbeat, no grant.
func TestGrantAfterLeaseExpiry(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()
	sessions, err := s.RegisterHost(ctx, host("h1"), twoNodes(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var sessA store.Session
	for _, sess := range sessions {
		if sess.NodeName == "node-a" {
			sessA = sess
		}
	}
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "gr-exp", spec())
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := placeOnce(ctx, s, vm.ID, node, store.Resources{CPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 5 << 30})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond) // lease lapses

	err = s.GrantExecution(ctx, sessA, vm.ID, epoch)
	var denied *store.ErrGrantDenied
	if !errors.As(err, &denied) || denied.Reason != store.DenyLeaseExpired {
		t.Fatalf("want lease-expired denial, got %v", err)
	}
}

// TestGrantVsDelete: matrix row 7/15 — a tombstone committed before the
// grant makes admission fail; the daemon never touches the substrate.
func TestGrantVsDelete(t *testing.T) {
	s, sess, vm, epoch := grantFixture(t)
	ctx := context.Background()

	fresh, err := s.GetVM(ctx, nil, vm.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TombstoneVM(ctx, nil, vm.ID, fresh.ResourceVersion); err != nil {
		t.Fatal(err)
	}

	err = s.GrantExecution(ctx, sess, vm.ID, epoch)
	var denied *store.ErrGrantDenied
	if !errors.As(err, &denied) || denied.Reason != store.DenyTombstoned {
		t.Fatalf("want tombstoned denial, got %v", err)
	}
}

// TestUnassignVsGrant: exactly one of {grant, unassign} wins, whichever
// commits first — the placements row serializes them (matrix rows 7–8).
func TestUnassignVsGrant(t *testing.T) {
	// Order 1: unassign first → grant must fail (placement torn down).
	s, sess, vm, epoch := grantFixture(t)
	ctx := context.Background()

	unassigned, err := s.UnassignIfUngranted(ctx, vm.ID, epoch)
	if err != nil || !unassigned {
		t.Fatalf("unassign of ungranted placement: %v %v", unassigned, err)
	}
	err = s.GrantExecution(ctx, sess, vm.ID, epoch)
	var denied *store.ErrGrantDenied
	if !errors.As(err, &denied) {
		t.Fatalf("grant after unassign must be denied, got %v", err)
	}

	// Order 2: grant first → unassign must refuse (exposure recorded).
	s2, sess2, vm2, epoch2 := grantFixture(t)
	if err := s2.GrantExecution(ctx, sess2, vm2.ID, epoch2); err != nil {
		t.Fatal(err)
	}
	unassigned, err = s2.UnassignIfUngranted(ctx, vm2.ID, epoch2)
	if err != nil {
		t.Fatal(err)
	}
	if unassigned {
		t.Fatal("a granted placement must NEVER be unassigned — two VMs is worse than one late VM")
	}
}

// TestUnassignReturnsVMToPending: the never-granted path releases capacity
// and requeues (matrix row 8).
func TestUnassignReturnsVMToPending(t *testing.T) {
	s, _, vm, epoch := grantFixture(t)
	ctx := context.Background()

	if _, err := s.UnassignIfUngranted(ctx, vm.ID, epoch); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetVM(ctx, nil, vm.Name)
	if err != nil || got.Phase != "PENDING" || got.NodeName != nil {
		t.Fatalf("vm after unassign: %v %+v", err, got)
	}
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 0 {
		t.Fatalf("capacity not released: %+v", n)
	}
	// Rescheduling bumps to epoch 2; the old epoch's ledger row stays
	// torn_down (fenced garbage is tracked, not orphaned).
	node, err := s.GetNode(ctx, "node-b")
	if err != nil {
		t.Fatal(err)
	}
	epoch2, err := placeOnce(ctx, s, vm.ID, node, store.Resources{CPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 5 << 30})
	if err != nil || epoch2 != epoch+1 {
		t.Fatalf("reschedule epoch: %d, %v", epoch2, err)
	}
}
