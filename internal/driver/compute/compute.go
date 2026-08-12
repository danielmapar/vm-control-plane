// Package compute defines the portable compute-driver contract. The real
// implementation (go-libvirt) is Linux-only behind build tags; the fake is
// first-class (plan D12): it powers the Windows-native demo, fast E2E, and
// most of the chaos matrix.
package compute

import "context"

// VMConfig is what a driver needs to realize one VM placement. Node, VM,
// and epoch identity ride along because every artifact a driver creates is
// ownership-stamped (plan §6.6).
type VMConfig struct {
	VMID    string
	Name    string
	Node    string
	Epoch   int64
	CPUs    uint32
	MemoryB uint64
	Image   string
	DiskB   uint64
	Network string
	Running bool // desired power state
}

// State is the driver-observed condition of a VM.
type State string

const (
	StateRunning State = "RUNNING"
	StateShutoff State = "SHUTOFF"
	StateAbsent  State = "ABSENT"
)

// Driver is the compute contract. Every method is ensure-style idempotent:
// redelivery and re-execution must converge, never duplicate.
type Driver interface {
	// Ensure converges the substrate to cfg (define-if-absent by identity,
	// start/stop to match cfg.Running) and returns the observed state.
	Ensure(ctx context.Context, cfg VMConfig) (State, error)
	// Observe reports the current state without mutating anything.
	Observe(ctx context.Context, vmID string, epoch int64) (State, error)
	// Teardown removes everything this (vm, epoch) owns. Idempotent;
	// returns nil when already absent.
	Teardown(ctx context.Context, vmID string, epoch int64) error
}
