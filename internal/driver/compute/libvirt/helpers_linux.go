//go:build linux

package libvirt

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/domainxml"
)

// errNotOurs marks a domain that is absent, foreign, or at the wrong epoch —
// owner-scoped resync treats all three as "not present for this placement".
var errNotOurs = errors.New("libvirt: no owned domain for this placement")

func toLibvirtUUID(id uuid.UUID) golibvirt.UUID {
	var u golibvirt.UUID
	copy(u[:], id[:])
	return u
}

// domainByOwner looks a domain up by its VM UUID and confirms our ownership
// metadata at the given epoch. Returns errNotOurs for absent/foreign/
// wrong-epoch domains.
func domainByOwner(l *golibvirt.Libvirt, id uuid.UUID, epoch int64) (golibvirt.Domain, error) {
	dom, err := l.DomainLookupByUUID(toLibvirtUUID(id))
	if err != nil {
		if golibvirt.IsNotFound(err) {
			return golibvirt.Domain{}, errNotOurs
		}
		return golibvirt.Domain{}, err
	}
	xml, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return golibvirt.Domain{}, err
	}
	own, err := domainxml.ParseOwnership(xml)
	if err != nil {
		return golibvirt.Domain{}, err
	}
	if own == nil || own.VMID != id || own.Epoch != epoch {
		return golibvirt.Domain{}, errNotOurs
	}
	return dom, nil
}

// assertOwned confirms an existing same-named domain is ours at this epoch;
// a mismatch is a conflict (never adopt foreign resources).
func (d *Driver) assertOwned(l *golibvirt.Libvirt, dom golibvirt.Domain, id uuid.UUID, epoch int64) error {
	xml, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return err
	}
	own, err := domainxml.ParseOwnership(xml)
	if err != nil {
		return err
	}
	if own == nil {
		return fmt.Errorf("libvirt: a foreign domain named %q exists — refusing to adopt", dom.Name)
	}
	if own.VMID != id || own.Epoch != epoch {
		return fmt.Errorf("libvirt: domain %q owned by (%s, epoch %d), not (%s, epoch %d)",
			dom.Name, own.VMID, own.Epoch, id, epoch)
	}
	return nil
}

// teardownStorage removes the (vm, epoch) storage directory under whatever
// node owns it — scanned, because the Teardown interface has vm+epoch but
// not node. Contained and idempotent.
func (d *Driver) teardownStorage(vmID string, epoch int64) error {
	entries, err := os.ReadDir(d.cfg.StorageRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "cache" {
			continue
		}
		node := e.Name()
		dir := filepath.Join(d.cfg.StorageRoot, node, vmID, fmt.Sprintf("%d", epoch))
		if _, err := os.Stat(dir); err == nil {
			if err := d.runner.TeardownEpoch(node, vmID, epoch); err != nil {
				return err
			}
		}
	}
	return nil
}

// vlanFor maps a tenant network name to a stable VLAN id in [100, 4000).
func vlanFor(network string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(network))
	return uint16(100 + h.Sum32()%3900)
}

// tenantAddr derives a deterministic demo tenant IP (10.100.x.y/24) from
// the VM UUID. Real IPAM lands with the network resource.
func tenantAddr(id uuid.UUID) string {
	return fmt.Sprintf("10.100.%d.%d/24", id[14]%254+1, id[15]%253+2)
}
