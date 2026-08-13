//go:build linux

package libvirt

import (
	"context"
	"fmt"
	"hash/fnv"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/domainxml"
)

// mgmtNetworkXML is our management NAT network: guest-to-guest isolated via
// per-port isolation on the domain side; dnsmasq provides DHCP + host
// reachability for SSH. Deterministic UUID so it is idempotently
// identifiable across restarts.
func mgmtNetworkXML(name string) string {
	netUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("vmc-mgmt-network:"+name))
	return fmt.Sprintf(`<network>
  <name>%s</name>
  <uuid>%s</uuid>
  <forward mode='nat'/>
  <bridge name='%s' stp='on' delay='0'/>
  <ip address='192.168.221.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='192.168.221.10' end='192.168.221.240'/>
    </dhcp>
  </ip>
</network>`, name, netUUID, bridgeName(name))
}

func bridgeName(net string) string {
	// Linux interface names are capped at 15 chars (IFNAMSIZ-1). Derive a
	// short, stable, collision-resistant name: "vmcbr-" + 6 hex of a hash
	// = 12 chars.
	h := fnv.New32a()
	_, _ = h.Write([]byte(net))
	return fmt.Sprintf("vmcbr-%06x", h.Sum32()&0xffffff)
}

// EnsureMgmtNetwork defines and starts the management NAT network if absent.
// Idempotent. Note: golibvirt.IsNotFound matches domain-not-found only, so a
// network lookup that fails is treated as "define it" — and a define that
// loses a define-race is recovered by re-lookup. The supervisor already
// distinguishes transport failures (which it poisons on) from server errors
// reaching this closure.
func (d *Driver) EnsureMgmtNetwork(ctx context.Context) error {
	return d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		net, err := l.NetworkLookupByName(d.cfg.MgmtNetwork)
		if err != nil {
			// Assume not present; define it.
			net, err = l.NetworkDefineXML(mgmtNetworkXML(d.cfg.MgmtNetwork))
			if err != nil {
				// A concurrent define may have won; re-lookup.
				net2, lerr := l.NetworkLookupByName(d.cfg.MgmtNetwork)
				if lerr != nil {
					return fmt.Errorf("define management network: %w", err)
				}
				net = net2
			}
		}
		active, aerr := l.NetworkIsActive(net)
		if aerr != nil {
			return aerr
		}
		if active == 0 {
			return l.NetworkCreate(net)
		}
		return nil
	})
}

// MgmtIP returns the management-NIC address of a VM from the network's DHCP
// leases, matched by the deterministic management MAC. Empty if no lease yet.
func (d *Driver) MgmtIP(ctx context.Context, vmID string) (string, error) {
	id, err := uuid.Parse(vmID)
	if err != nil {
		return "", err
	}
	mac := domainxml.MAC(id, 0)
	var ip string
	err = d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		net, err := l.NetworkLookupByName(d.cfg.MgmtNetwork)
		if err != nil {
			return err
		}
		leases, _, err := l.NetworkGetDhcpLeases(net, golibvirt.OptString{mac}, 1, 0)
		if err != nil {
			return err
		}
		if len(leases) > 0 {
			ip = leases[0].Ipaddr
		}
		return nil
	})
	return ip, err
}
