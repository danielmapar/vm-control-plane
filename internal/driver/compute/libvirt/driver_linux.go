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
	// Emulated runs guests under QEMU TCG (software) instead of hardware KVM —
	// no /dev/kvm, no nested VT-x, so it is stable on a substrate whose nested
	// virtualization is not (the VirtualBox case). Slower to boot.
	Emulated bool
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

// Ensure converges the substrate for one placement: backing image, sized
// overlay, cloud-init seed, domain XML, define-if-absent (matched by name,
// owner metadata, and epoch), then start/stop to match desired power. Every
// step is idempotent so redelivery converges.
func (d *Driver) Ensure(ctx context.Context, cfg compute.VMConfig) (compute.State, error) {
	vmID, err := uuid.Parse(cfg.VMID)
	if err != nil {
		return compute.StateAbsent, fmt.Errorf("libvirt: bad vm id: %w", err)
	}

	diskPath, seedPath, err := d.prepareStorage(ctx, vmID, cfg)
	if err != nil {
		return compute.StateAbsent, err
	}
	xml, err := domainxml.Build(d.buildDomainConfig(vmID, cfg, diskPath, seedPath))
	if err != nil {
		return compute.StateAbsent, err
	}

	var state compute.State
	err = d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		dom, err := d.defineOrAdopt(l, cfg.Name, vmID, cfg.Epoch, xml)
		if err != nil {
			return err
		}
		state, err = d.convergePower(ctx, l, dom, cfg.Running)
		return err
	})
	if err != nil {
		return compute.StateAbsent, err
	}
	return state, nil
}

// tenantAttached reports whether this placement wants a tenant NIC.
func (d *Driver) tenantAttached(cfg compute.VMConfig) bool {
	return cfg.Network != "" && d.cfg.TenantBridge != ""
}

// prepareStorage resolves the backing image, creates the sized root overlay,
// and writes the cloud-init seed ISO, returning their durable paths.
func (d *Driver) prepareStorage(ctx context.Context, vmID uuid.UUID, cfg compute.VMConfig) (diskPath, seedPath string, err error) {
	backing, backingSize, err := d.cfg.ResolveBacking(ctx, cfg.Image)
	if err != nil {
		return "", "", fmt.Errorf("libvirt: resolve image %q: %w", cfg.Image, err)
	}
	if cfg.RootDiskBytes < backingSize {
		return "", "", fmt.Errorf("libvirt: requested disk %d < backing size %d", cfg.RootDiskBytes, backingSize)
	}
	diskPath, err = d.runner.EnsureOverlay(ctx, cfg.Node, cfg.VMID, cfg.Epoch, backing, cfg.RootDiskBytes)
	if err != nil {
		return "", "", err
	}
	seedPath, err = d.buildSeed(vmID, cfg)
	if err != nil {
		return "", "", err
	}
	return diskPath, seedPath, nil
}

// buildSeed renders the cloud-init NoCloud ISO for this VM and persists it.
func (d *Driver) buildSeed(vmID uuid.UUID, cfg compute.VMConfig) (string, error) {
	seedCfg := seed.Config{
		InstanceID:       cfg.VMID,
		Hostname:         cfg.Name,
		SSHAuthorizedKey: d.cfg.SSHAuthorizedKey,
		MgmtMAC:          domainxml.MAC(vmID, 0),
	}
	if d.tenantAttached(cfg) {
		seedCfg.TenantMAC = domainxml.MAC(vmID, 1)
		// A deterministic tenant address for the demo; real IPAM lands with the
		// network resource.
		seedCfg.TenantCIDR = tenantAddr(vmID)
	}
	iso := &bytes.Buffer{}
	if err := seed.Build(iso, seedCfg); err != nil {
		return "", err
	}
	return d.runner.WriteSeed(cfg.Node, cfg.VMID, cfg.Epoch, iso.Bytes())
}

// buildDomainConfig assembles the domain definition inputs, including the
// optional tenant attachment.
func (d *Driver) buildDomainConfig(vmID uuid.UUID, cfg compute.VMConfig, diskPath, seedPath string) domainxml.Config {
	domCfg := domainxml.Config{
		Name: cfg.Name, VMID: vmID, Node: cfg.Node, Epoch: cfg.Epoch,
		CPUs: cfg.CPUs, MemoryBytes: cfg.MemoryBytes,
		DiskPath: diskPath, SeedPath: seedPath,
		MgmtNetwork: d.cfg.MgmtNetwork,
		CPUSet:      d.cfg.CPUSet,
		Emulated:    d.cfg.Emulated,
	}
	if d.tenantAttached(cfg) {
		domCfg.TenantBridge = d.cfg.TenantBridge
		domCfg.TenantVLAN = vlanFor(cfg.Network)
	}
	return domCfg
}

// defineOrAdopt returns the domain, defining it from xml if absent. An existing
// same-named domain is adopted only after its ownership metadata confirms it is
// ours at this epoch; a foreign domain is refused.
func (d *Driver) defineOrAdopt(l *golibvirt.Libvirt, name string, vmID uuid.UUID, epoch int64, xml string) (golibvirt.Domain, error) {
	dom, lookErr := l.DomainLookupByName(name)
	if lookErr != nil {
		if !golibvirt.IsNotFound(lookErr) {
			return dom, lookErr
		}
		return l.DomainDefineXML(xml)
	}
	if err := d.assertOwned(l, dom, vmID, epoch); err != nil {
		return dom, err
	}
	return dom, nil
}

// convergePower starts or gracefully stops the domain to match wantRunning and
// returns the resulting state. The domain is never autostarted (the XML omits
// it), so power is driven only here.
func (d *Driver) convergePower(ctx context.Context, l *golibvirt.Libvirt, dom golibvirt.Domain, wantRunning bool) (compute.State, error) {
	stateCode, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return compute.StateAbsent, err
	}
	isRunning := golibvirt.DomainState(stateCode) == golibvirt.DomainRunning
	switch {
	case wantRunning && !isRunning:
		if err := l.DomainCreate(dom); err != nil {
			return compute.StateAbsent, err
		}
		return compute.StateRunning, nil
	case !wantRunning && isRunning:
		if err := d.gracefulStop(ctx, l, dom); err != nil {
			return compute.StateAbsent, err
		}
		return compute.StateShutoff, nil
	case wantRunning:
		return compute.StateRunning, nil
	default:
		return compute.StateShutoff, nil
	}
}

// Observe returns the current state of the domain, scoped to our ownership at
// the given epoch. A foreign or wrong-epoch domain reads as absent.
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
	// Storage teardown is durable and runs after the domain is gone. The
	// compute.Driver.Teardown signature carries no node, so teardownStorage
	// scans the storage root and removes this (vm, epoch)'s artifacts under
	// whichever node directory holds them.
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
