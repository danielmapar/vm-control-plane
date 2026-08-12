// Package domainxml hand-builds libvirt domain XML — deliberately, because
// writing the XML is the hypervisor-layer lesson this project exists for
// (plan D8), and because every identity decision the fencing protocols
// need lives here:
//
//   - ownership metadata (node, VM UUID, placement epoch) in a custom
//     namespace — what owner-scoped resync and epoch-surgical teardown
//     filter on;
//   - DETERMINISTIC MAC and OVS interfaceid derived from VM UUID + NIC
//     index — libvirt would otherwise randomize them, and determinism is
//     what makes port verification and drift repair possible (D9);
//   - the management NIC rides our NAT network with <port isolated='yes'/>
//     (guests reach host + egress, never each other — the precise claim);
//   - the tenant NIC is <interface type='bridge'> + openvswitch virtualport
//   - VLAN tag: libvirt creates the tap and attaches the tagged port
//     atomically at domain start — taps do not exist earlier;
//   - autostart is NEVER set: an unfenced boot after host restart would
//     bypass every grant (autostart ownership is checked by drift).
//
// Pure Go, golden-tested on every platform: the builder is the one part of
// the compute driver Windows can fully verify.
package domainxml

import (
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Config is everything a domain definition needs. All fields are already
// validated upstream; the builder is deterministic and side-effect free.
type Config struct {
	Name    string
	VMID    uuid.UUID
	Node    string
	Epoch   int64
	CPUs    uint32
	MemoryB uint64
	// Root disk (qcow2) and cloud-init seed ISO, absolute paths.
	DiskPath string
	SeedPath string
	// Management network name (our NAT network, e.g. "vmc-mgmt").
	MgmtNetwork string
	// Tenant attachment; empty TenantBridge means management-only.
	TenantBridge string
	TenantVLAN   uint16
}

// MAC derives the deterministic, locally-administered MAC for a NIC.
// 52:54:00 is the QEMU/KVM prefix; the tail is content-addressed so the
// same (vm, nic) always yields the same address — cloud-init's
// network-config matches interfaces BY MAC.
func MAC(vmID uuid.UUID, nicIndex int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:nic-%d", vmID, nicIndex)))
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", sum[0], sum[1], sum[2])
}

// InterfaceID derives the stable OVS interfaceid for a NIC (uuid5 in the
// VM's namespace) — without it libvirt generates a random one per start,
// and port repair could never re-identify its own port.
func InterfaceID(vmID uuid.UUID, nicIndex int) string {
	return uuid.NewSHA1(vmID, []byte(fmt.Sprintf("nic-%d", nicIndex))).String()
}

// Build renders the domain XML.
func Build(cfg Config) (string, error) {
	if cfg.Name == "" || cfg.VMID == uuid.Nil || cfg.Node == "" || cfg.Epoch < 1 {
		return "", fmt.Errorf("domainxml: identity fields are mandatory (name/vmid/node/epoch)")
	}
	if cfg.CPUs < 1 || cfg.MemoryB < 1<<20 {
		return "", fmt.Errorf("domainxml: implausible resources (cpus=%d mem=%d)", cfg.CPUs, cfg.MemoryB)
	}
	if cfg.DiskPath == "" || cfg.MgmtNetwork == "" {
		return "", fmt.Errorf("domainxml: disk path and management network are mandatory")
	}

	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w(`<domain type='kvm'>`)
	w(`  <name>%s</name>`, esc(cfg.Name))
	w(`  <uuid>%s</uuid>`, cfg.VMID)
	// Ownership metadata: the filter for owner-scoped resync and the fence
	// for epoch-surgical teardown (plan §6.6).
	w(`  <metadata>`)
	w(`    <vmc:ownership xmlns:vmc="https://github.com/sigtunnel/vm-control-plane/ns/1">`)
	w(`      <vmc:node>%s</vmc:node>`, esc(cfg.Node))
	w(`      <vmc:vm-id>%s</vmc:vm-id>`, cfg.VMID)
	w(`      <vmc:placement-epoch>%d</vmc:placement-epoch>`, cfg.Epoch)
	w(`    </vmc:ownership>`)
	w(`  </metadata>`)
	w(`  <memory unit='bytes'>%d</memory>`, cfg.MemoryB)
	w(`  <vcpu placement='static'>%d</vcpu>`, cfg.CPUs)
	w(`  <os>`)
	w(`    <type arch='x86_64' machine='q35'>hvm</type>`)
	w(`    <boot dev='hd'/>`)
	w(`  </os>`)
	w(`  <features><acpi/><apic/></features>`)
	w(`  <on_poweroff>destroy</on_poweroff>`)
	w(`  <on_reboot>restart</on_reboot>`)
	w(`  <on_crash>destroy</on_crash>`)
	w(`  <devices>`)
	w(`    <disk type='file' device='disk'>`)
	w(`      <driver name='qemu' type='qcow2'/>`)
	w(`      <source file='%s'/>`, esc(cfg.DiskPath))
	w(`      <target dev='vda' bus='virtio'/>`)
	w(`    </disk>`)
	if cfg.SeedPath != "" {
		w(`    <disk type='file' device='cdrom'>`)
		w(`      <driver name='qemu' type='raw'/>`)
		w(`      <source file='%s'/>`, esc(cfg.SeedPath))
		w(`      <target dev='sda' bus='sata'/>`)
		w(`      <readonly/>`)
		w(`    </disk>`)
	}
	// NIC 0: management — NAT network, guest-to-guest isolated.
	w(`    <interface type='network'>`)
	w(`      <source network='%s'/>`, esc(cfg.MgmtNetwork))
	w(`      <mac address='%s'/>`, MAC(cfg.VMID, 0))
	w(`      <model type='virtio'/>`)
	w(`      <port isolated='yes'/>`)
	w(`    </interface>`)
	// NIC 1: tenant — OVS access port, tagged at attach by libvirt.
	if cfg.TenantBridge != "" {
		w(`    <interface type='bridge'>`)
		w(`      <source bridge='%s'/>`, esc(cfg.TenantBridge))
		w(`      <virtualport type='openvswitch'>`)
		w(`        <parameters interfaceid='%s'/>`, InterfaceID(cfg.VMID, 1))
		w(`      </virtualport>`)
		w(`      <vlan><tag id='%d'/></vlan>`, cfg.TenantVLAN)
		w(`      <mac address='%s'/>`, MAC(cfg.VMID, 1))
		w(`      <model type='virtio'/>`)
		w(`    </interface>`)
	}
	w(`    <console type='pty'/>`)
	w(`    <rng model='virtio'><backend model='random'>/dev/urandom</backend></rng>`)
	w(`  </devices>`)
	b.WriteString(`</domain>`)
	return b.String(), nil
}

// Ownership is the parsed metadata block — what resync extracts from live
// domains to answer "is this ours, and which epoch?"
type Ownership struct {
	Node  string
	VMID  uuid.UUID
	Epoch int64
}

// ParseOwnership extracts the vmc ownership block from domain XML.
// Domains without the block are FOREIGN: never adopted, never touched.
func ParseOwnership(domXML string) (*Ownership, error) {
	type meta struct {
		Node  string `xml:"node"`
		VMID  string `xml:"vm-id"`
		Epoch int64  `xml:"placement-epoch"`
	}
	type doc struct {
		Metadata struct {
			Ownership meta `xml:"ownership"`
		} `xml:"metadata"`
	}
	var d doc
	if err := xml.Unmarshal([]byte(domXML), &d); err != nil {
		return nil, fmt.Errorf("parse domain xml: %w", err)
	}
	o := d.Metadata.Ownership
	if o.VMID == "" {
		return nil, nil // foreign domain
	}
	id, err := uuid.Parse(o.VMID)
	if err != nil {
		return nil, fmt.Errorf("ownership vm-id: %w", err)
	}
	return &Ownership{Node: o.Node, VMID: id, Epoch: o.Epoch}, nil
}

func esc(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}
