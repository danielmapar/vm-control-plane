package scheduler

import (
	"testing"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

func node(name string, cpus, resCPU int64, labels map[string]string) *store.Node {
	return &store.Node{
		Name: name, CPUs: cpus, MemoryBytes: 32 << 30, DiskBytes: 500 << 30,
		ReservedCPUs: resCPU, Labels: labels,
	}
}

func req(constraints map[string]string) Request {
	return Request{
		Resources:   store.Resources{CPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 10 << 30},
		Constraints: constraints,
	}
}

func TestFilterCapacity(t *testing.T) {
	nodes := []*store.Node{node("full", 4, 4, nil), node("free", 4, 0, nil)}
	got := Candidates(nodes, req(nil))
	if len(got) != 1 || got[0].Name != "free" {
		t.Fatalf("capacity filter: %+v", names(got))
	}
}

func TestFilterConstraints(t *testing.T) {
	nodes := []*store.Node{
		node("plain", 8, 0, nil),
		node("ssd", 8, 0, map[string]string{"ssd": "true"}),
	}
	got := Candidates(nodes, req(map[string]string{"ssd": "true"}))
	if len(got) != 1 || got[0].Name != "ssd" {
		t.Fatalf("constraint filter: %+v", names(got))
	}
}

func TestLeastAllocatedSpread(t *testing.T) {
	nodes := []*store.Node{node("busy", 8, 6, nil), node("idle", 8, 1, nil)}
	got := Candidates(nodes, req(nil))
	if len(got) != 2 || got[0].Name != "idle" {
		t.Fatalf("spread order: %+v", names(got))
	}
}

// TestSaturatedDimensionDominates: one exhausted dimension cannot hide
// behind two idle ones — max-utilization scoring.
func TestSaturatedDimensionDominates(t *testing.T) {
	memHog := &store.Node{
		Name: "memhog", CPUs: 8, MemoryBytes: 8 << 30, DiskBytes: 500 << 30,
		ReservedMemory: 7 << 30, // 87% memory
	}
	balanced := &store.Node{
		Name: "balanced", CPUs: 8, MemoryBytes: 8 << 30, DiskBytes: 500 << 30,
		ReservedCPUs: 4, ReservedMemory: 4 << 30, // 50%
	}
	got := Candidates([]*store.Node{memHog, balanced}, req(nil))
	if got[0].Name != "balanced" {
		t.Fatalf("saturated dimension must dominate the score: %+v", names(got))
	}
}

func names(nodes []*store.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
