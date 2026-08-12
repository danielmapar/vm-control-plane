//go:build linux

package libvirt

import (
	"context"
	"fmt"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/domainxml"
)

// mgmtNetworkXML is our management NAT network: guest-to-guest isolated via
// per-port isolation on the domain side; dnsmasq provides DHCP + host
// reachability for SSH (plan D9). Deterministic UUID so it is idempotently
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
	// libvirt bridge names are limited; derive a short stable one.
	if len(net) > 10 {
		net = net[:10]
	}
	return "vmcbr-" + net
}

// EnsureMgmtNetwork defines and starts the management NAT network if absent.
// Idempotent; a foreign same-named network is a conflict, never adopted.
func (d *Driver) EnsureMgmtNetwork(ctx context.Context) error {
	return d.sup.call(ctx, func(l *golibvirt.Libvirt) error {
		if net, err := l.NetworkLookupByName(d.cfg.MgmtNetwork); err == nil {
			// Exists — confirm it is active; start if not.
			active, aerr := l.NetworkIsActive(net)
			if aerr != nil {
				return aerr
			}
			if active == 0 {
				return l.NetworkCreate(net)
			}
			return nil
		} else if !golibvirt.IsNotFound(err) {
			return err
		}
		net, err := l.NetworkDefineXML(mgmtNetworkXML(d.cfg.MgmtNetwork))
		if err != nil {
			return err
		}
		return l.NetworkCreate(net)
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
