// Package scheduler is the pure filter/score stage. It proposes
// candidates; the store's PlaceVM makes the decision real by rechecking
// every hard predicate atomically inside the claim-guarded transaction —
// this package can therefore be simple, and wrong-by-staleness safely.
package scheduler

import (
	"sort"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Request is what a VM needs from a node.
type Request struct {
	Resources   store.Resources
	Constraints map[string]string
}

// FromSpec derives the scheduling request from a VM spec.
func FromSpec(spec *vmcv1.VmSpec) Request {
	return Request{
		Resources: store.Resources{
			CPUs:        int64(spec.GetCpus()),
			MemoryBytes: int64(spec.GetMemoryBytes()),
			DiskBytes:   int64(spec.GetRootDiskBytes()),
		},
		Constraints: spec.GetConstraints(),
	}
}

// Candidates filters Ready nodes (capacity headroom, label constraints)
// and orders them by least-allocated spread. The returned order is a
// proposal: PlaceVM's conditional reservation is the authority, and a
// zero-row result there simply advances to the next candidate.
func Candidates(nodes []*store.Node, req Request) []*store.Node {
	var fit []*store.Node
	for _, n := range nodes {
		if !labelsMatch(n.Labels, req.Constraints) {
			continue
		}
		if n.ReservedCPUs+req.Resources.CPUs > n.CPUs ||
			n.ReservedMemoryBytes+req.Resources.MemoryBytes > n.MemoryBytes ||
			n.ReservedDiskBytes+req.Resources.DiskBytes > n.DiskBytes {
			continue
		}
		fit = append(fit, n)
	}
	sort.SliceStable(fit, func(i, j int) bool {
		return allocRatio(fit[i]) < allocRatio(fit[j])
	})
	return fit
}

// allocRatio is the least-allocated score: the max of the three dimension
// utilizations, so one saturated dimension cannot hide behind two idle ones.
func allocRatio(n *store.Node) float64 {
	r := ratio(n.ReservedCPUs, n.CPUs)
	if m := ratio(n.ReservedMemoryBytes, n.MemoryBytes); m > r {
		r = m
	}
	if d := ratio(n.ReservedDiskBytes, n.DiskBytes); d > r {
		r = d
	}
	return r
}

func ratio(used, total int64) float64 {
	if total <= 0 {
		return 1
	}
	return float64(used) / float64(total)
}

func labelsMatch(labels, constraints map[string]string) bool {
	for k, v := range constraints {
		if labels[k] != v {
			return false
		}
	}
	return true
}
