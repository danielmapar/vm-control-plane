//go:build linux

package libvirt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/domainxml"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/seed"
	"github.com/sigtunnel/vm-control-plane/internal/driver/volume/qcow2"
)

// Config wires the real driver.
type Config struct {
	Socket           string // libvirt unix socket (default system socket)
	StorageRoot      string // /var/lib/vmc
	MgmtNetwork      string // libvirt NAT network for the management NIC
	TenantBridge     string // OVS integration bridge (empty = mgmt-only)
	SSHAuthorizedKey string // injected into every guest via cloud-init
	// CPUSet pins every guest's vCPU+emulator threads to these host cores
	// (libvirt cpuset, e.g. "8-15"). Empty = no pinning. On nested VirtualBox
	// this keeps L2 guests off the cores the control-plane stack runs on,
	// preventing the boot-time preemption that permanently wedges them.
	CPUSet string
	// ResolveBacking maps an image name (e.g. "ubuntu-24.04") to a cached,
	// verified backing qcow2 path. The image cache is pre-seeded by the
	// spike/demo; a missing image is an error, never a silent download.
	ResolveBacking func(ctx context.Context, image string) (path string, backingSize uint64, err error)
	// StopDeadline bounds the graceful ACPI shutdown before forced destroy.
	StopDeadline time.Duration
}

// Driver implements compute.Driver against real libvirt/KVM.
type Driver struct {
	cfg    Config
	sup    *supervisor
	runner *qcow2.Runner
}

// New builds the driver. The caller ensures the management network exists
// (EnsureMgmtNetwork) before Ensure is invoked.
func New(cfg Config) *Driver {
	if cfg.StopDeadline <= 0 {
		cfg.StopDeadline = 60 * time.Second
	}
	return &Driver{
		cfg:    cfg,
		sup:    newSupervisor(cfg.Socket),
		runner: qcow2.NewRunner(cfg.StorageRoot),
	}
}

var _ compute.Driver = (*Driver)(nil)

// Ensure converges the substrate for one placement: backing image → sized
// overlay → cloud-init seed → domain XML → define-if-absent (matched by name
// + owner metadata + epoch) → start/stop to match desired power. Every step
// is idempotent so redelivery converges.
func (d *Driver) Ensure(ctx context.Context, cfg compute.VMConfig) (compute.State, error) {
	vmID, err := uuid.Parse(cfg.VMID)
	if err != nil {
		return compute.StateAbsent, fmt.Errorf("libvirt: bad vm id: %w", err)
	}

	backing, backingSize, err := d.cfg.ResolveBacking(ctx, cfg.Image)
	if err != nil {
		return compute.StateAbsent, fmt.Errorf("libvirt: resolve image %q: %w", cfg.Image, err)
	}
	if cfg.DiskB < backingSize {
		return compute.StateAbsent, fmt.Errorf("libvirt: requested disk %d < backing size %d", cfg.DiskB, backingSize)
	}

	diskPath, err := d.runner.EnsureOverlay(ctx, cfg.Node, cfg.VMID, cfg.Epoch, backing, cfg.DiskB)
	if err != nil {
		return compute.StateAbsent, err
	}

	iso := &bytes.Buffer{}
	seedCfg := seed.Config{
		InstanceID:       cfg.VMID,
		Hostname:         cfg.Name,
		SSHAuthorizedKey: d.cfg.SSHAuthorizedKey,
		MgmtMAC:          domainxml.MAC(vmID, 0),
	}
	if cfg.Network != "" && d.cfg.TenantBridge != "" {
		seedCfg.TenantMAC = domainxml.MAC(vmID, 1)
		// A trivial deterministic tenant address for the demo; real IPAM
		// lands with the network resource.
		seedCfg.TenantCIDR = tenantAddr(vmID)
	}
	if err := seed.Build(iso, seedCfg); err != nil {
		return compute.StateAbsent, err
	}
	seedPath, err := d.runner.WriteSeed(cfg.Node, cfg.VMID, cfg.Epoch, iso.Bytes())
	if err != nil {
		return compute.StateAbsent, err
	}

	domCfg := domainxml.Config{
		Name: cfg.Name, VMID: vmID, Node: cfg.Node, Epoch: cfg.Epoch,
		CPUs: cfg.CPUs, MemoryB: cfg.MemoryB,
		DiskPath: diskPath, SeedPath: seedPath,
		MgmtNetwork:  d.cfg.MgmtNetwork,
		TenantBridge: firstNonEmpty(cfg.Network, ""),
		CPUSet:       d.cfg.CPUSet,
	}
	if cfg.Network != "" && d.cfg.TenantBridge != "" {
		domCfg.TenantBridge = d.cfg.TenantBridge
		domCfg.TenantVLAN = vlanFor(cfg.Network)
	} else {
		domCfg.TenantBridge = ""
	}
	xml, err := domainxml.Build(domCfg)
	if err != nil {
		return compute.StateAbsent, err
	}

	// Define-if-absent (matched by owner metadata + epoch), then start/stop.
	var state compute.State
	err = d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		dom, lookErr := l.DomainLookupByName(cfg.Name)
		if lookErr != nil {
			if !golibvirt.IsNotFound(lookErr) {
				return lookErr
			}
			defined, defErr := l.DomainDefineXML(xml)
			if defErr != nil {
				return defErr
			}
			dom = defined
		} else {
			// A domain with our name exists — confirm it is OURS at this
			// epoch before touching it (owner-scoped; never adopt foreign).
			if err := d.assertOwned(l, dom, vmID, cfg.Epoch); err != nil {
				return err
			}
		}
		// Never autostart (the XML omits it); ensure power matches.
		running, reason, stErr := l.DomainGetState(dom, 0)
		_ = reason
		if stErr != nil {
			return stErr
		}
		isRunning := golibvirt.DomainState(running) == golibvirt.DomainRunning
		switch {
		case cfg.Running && !isRunning:
			if err := l.DomainCreate(dom); err != nil {
				return err
			}
			state = compute.StateRunning
		case !cfg.Running && isRunning:
			if err := d.gracefulStop(ctx, l, dom); err != nil {
				return err
			}
			state = compute.StateShutoff
		case cfg.Running:
			state = compute.StateRunning
		default:
			state = compute.StateShutoff
		}
		return nil
	})
	if err != nil {
		return compute.StateAbsent, err
	}
	return state, nil
}

// Observe returns the current state of the domain, scoped to our ownership
// at the given epoch. A foreign or wrong-epoch domain reads as ABSENT
// (owner-scoped resync, plan §6.6).
func (d *Driver) Observe(ctx context.Context, vmID string, epoch int64) (compute.State, error) {
	id, err := uuid.Parse(vmID)
	if err != nil {
		return compute.StateAbsent, err
	}
	state := compute.StateAbsent
	err = d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		dom, err := domainByOwner(l, id, epoch)
		if err != nil {
			if errors.Is(err, errNotOurs) {
				return nil // ABSENT
			}
			return err
		}
		rState, _, err := l.DomainGetState(dom, 0)
		if err != nil {
			return err
		}
		switch golibvirt.DomainState(rState) {
		case golibvirt.DomainRunning:
			state = compute.StateRunning
		case golibvirt.DomainShutoff, golibvirt.DomainShutdown, golibvirt.DomainCrashed:
			state = compute.StateShutoff
		default:
			state = compute.StateShutoff
		}
		return nil
	})
	return state, err
}

// Teardown destroys and undefines our domain at this epoch, then removes
// its storage — durably, before any receipt is issued. Idempotent.
func (d *Driver) Teardown(ctx context.Context, vmID string, epoch int64) error {
	id, err := uuid.Parse(vmID)
	if err != nil {
		return err
	}
	err = d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		dom, err := domainByOwner(l, id, epoch)
		if err != nil {
			if errors.Is(err, errNotOurs) {
				return nil // already gone
			}
			return err
		}
		_ = l.DomainDestroy(dom) // may not be running
		if uerr := l.DomainUndefineFlags(dom, golibvirt.DomainUndefineManagedSave|golibvirt.DomainUndefineNvram); uerr != nil {
			if !golibvirt.IsNotFound(uerr) {
				return uerr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Storage teardown is durable and happens after the domain is gone.
	// We tear down by the node we own; the executor supplies node via a
	// separate call path — here we can only clean by (vm, epoch) under the
	// storage root prefix, which TeardownEpoch does per node internally.
	return d.teardownStorage(vmID, epoch)
}

func (d *Driver) gracefulStop(ctx context.Context, l *golibvirt.Libvirt, dom golibvirt.Domain) error {
	if err := l.DomainShutdown(dom); err != nil && !golibvirt.IsNotFound(err) {
		// Fall through to forced destroy.
		_ = err
	}
	deadline := time.Now().Add(d.cfg.StopDeadline)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		st, _, err := l.DomainGetState(dom, 0)
		if err != nil {
			return err
		}
		if golibvirt.DomainState(st) == golibvirt.DomainShutoff {
			return nil
		}
	}
	// Deadline passed: force.
	if err := l.DomainDestroy(dom); err != nil && !golibvirt.IsNotFound(err) {
		return err
	}
	return nil
}
