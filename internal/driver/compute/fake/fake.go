// Package fake is the in-memory compute driver: deterministic, hookable,
// and honest about identity — domains are keyed by (vm, epoch) exactly as
// real artifacts are ownership-stamped.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
)

type domain struct {
	cfg     compute.VMConfig
	running bool
}

// Driver is a fake hypervisor. Failure hooks inject errors per operation;
// DriftStop simulates out-of-band tampering (virsh destroy's analog).
type Driver struct {
	mu      sync.Mutex
	domains map[string]*domain // key: vmID/epoch

	// FailEnsure/FailTeardown, when non-nil, are consulted per call.
	FailEnsure   func(cfg compute.VMConfig) error
	FailTeardown func(vmID string) error
}

// New returns an empty fake driver.
func New() *Driver { return &Driver{domains: map[string]*domain{}} }

func key(vmID string, epoch int64) string { return fmt.Sprintf("%s/%d", vmID, epoch) }

func (d *Driver) Ensure(_ context.Context, cfg compute.VMConfig) (compute.State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailEnsure != nil {
		if err := d.FailEnsure(cfg); err != nil {
			return compute.StateAbsent, err
		}
	}
	k := key(cfg.VMID, cfg.Epoch)
	dom, ok := d.domains[k]
	if !ok {
		dom = &domain{cfg: cfg}
		d.domains[k] = dom
	}
	dom.running = cfg.Running
	if dom.running {
		return compute.StateRunning, nil
	}
	return compute.StateShutoff, nil
}

func (d *Driver) Observe(_ context.Context, vmID string, epoch int64) (compute.State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dom, ok := d.domains[key(vmID, epoch)]
	if !ok {
		return compute.StateAbsent, nil
	}
	if dom.running {
		return compute.StateRunning, nil
	}
	return compute.StateShutoff, nil
}

func (d *Driver) Teardown(_ context.Context, vmID string, epoch int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.FailTeardown != nil {
		if err := d.FailTeardown(vmID); err != nil {
			return err
		}
	}
	delete(d.domains, key(vmID, epoch))
	return nil
}

// DriftStop simulates `virsh destroy`: the domain stops out of band. The
// next Observe reports SHUTOFF; the reconcile loop repairs it through the
// same path that provisioned it.
func (d *Driver) DriftStop(vmID string, epoch int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	dom, ok := d.domains[key(vmID, epoch)]
	if !ok {
		return false
	}
	dom.running = false
	return true
}

// Count reports how many (vm, epoch) domains exist — the invariant
// auditor's substrate view.
func (d *Driver) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.domains)
}
