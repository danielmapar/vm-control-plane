package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// TestNodeLossPolicy in one scenario. Two VMs on a node
// whose lease expires: the never-granted one is unassigned back to Pending
// with capacity released; the granted one parks UNKNOWN and is never
// rescheduled.
func TestNodeLossPolicy(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	sessions, err := s.RegisterHost(ctx, host("h1"),
		[]store.NodeQuota{{Name: "node-a", CPUs: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30}},
		300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	sess := sessions[0]
	node, err := s.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}

	res := store.Resources{CPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30}
	granted, err := s.CreateVM(ctx, nil, uuid.New(), "nl-granted", spec())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := placeOnce(ctx, s, granted.ID, node, res); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantExecution(ctx, sess, granted.ID, 1); err != nil {
		t.Fatal(err)
	}

	ungranted, err := s.CreateVM(ctx, nil, uuid.New(), "nl-pending", spec())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := placeOnce(ctx, s, ungranted.ID, node, res); err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond) // node lease expires

	actions, err := s.ExpireNodeVMs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, a := range actions {
		outcomes[a.VMName] = a.Outcome
	}
	if outcomes["nl-pending"] != "rescheduled" || outcomes["nl-granted"] != "unknown" {
		t.Fatalf("outcomes: %+v", outcomes)
	}

	p, err := s.GetVM(ctx, nil, "nl-pending")
	if err != nil || p.Phase != "PENDING" || p.NodeName != nil {
		t.Fatalf("ungranted vm must return to Pending: %+v", p)
	}
	g, err := s.GetVM(ctx, nil, "nl-granted")
	if err != nil || g.Phase != "UNKNOWN" || g.NodeName == nil {
		t.Fatalf("granted vm must park UNKNOWN, keeping its node: %+v", g)
	}
	// Capacity: only the granted VM's reservation remains.
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 2 {
		t.Fatalf("reserved after sweep: %+v", n)
	}

	// Sweep is idempotent.
	again, err := s.ExpireNodeVMs(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second sweep must be quiet: %v %+v", err, again)
	}

	// The node returns: heartbeat + fresh evidence restores the UNKNOWN VM
	// through the normal convergence path (wake happens in the sweep).
	if err := s.Heartbeat(ctx, sess, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExpireNodeVMs(ctx); err != nil {
		t.Fatal(err)
	}
	claims, err := s.ClaimDirtyVMs(ctx, "w", time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range claims {
		if c.VM.ID == granted.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("returned node must wake its UNKNOWN VMs for re-convergence")
	}
}
