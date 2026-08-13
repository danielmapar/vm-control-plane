//go:build linux

package libvirt

import (
	"errors"
	"fmt"

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
