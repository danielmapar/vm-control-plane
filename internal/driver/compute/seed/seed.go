// Package seed builds cloud-init NoCloud seed ISOs in pure Go (plan D9):
// no genisoimage/cloud-localds dependency, unit-testable on Windows.
//
// NoCloud contract (cloudinit NoCloud datasource): a volume labeled
// "CIDATA" carrying user-data, meta-data, and optionally network-config.
// The pieces this package gets right on purpose:
//
//   - meta-data carries a STABLE instance-id (the VM UUID): cloud-init
//     re-runs first-boot config when the instance-id changes, so the id
//     must not change across reboots or re-ensures;
//   - user-data installs the SSH key and disables password auth — without
//     authorized keys the boot-to-SSH acceptance test has nothing to
//     authenticate with (v4 review finding);
//   - network-config v2 matches interfaces BY MAC (deterministic, from
//     domainxml.MAC) — enumeration order is not a safe identifier; the
//     management NIC uses DHCP and owns the default route, the tenant NIC
//     is static with NO default route (the management/tenant split).
package seed

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/kdomanski/iso9660"
)

// Config for one VM's seed.
type Config struct {
	InstanceID string // VM UUID — stable across reboots
	Hostname   string
	// SSH public key granted to the login user (ephemeral per CI run).
	SSHAuthorizedKey string
	LoginUser        string // default "ubuntu"

	MgmtMAC string // management NIC — DHCP + default route
	// Tenant NIC (all empty = management-only).
	TenantMAC  string
	TenantCIDR string // e.g. "10.100.0.5/24"
}

// VolumeLabel is the NoCloud discovery label; cloud-init probes for it.
const VolumeLabel = "CIDATA"

// Build renders the seed ISO into w.
func Build(w io.Writer, cfg Config) error {
	if cfg.InstanceID == "" || cfg.Hostname == "" || cfg.MgmtMAC == "" {
		return fmt.Errorf("seed: instance-id, hostname, and mgmt MAC are mandatory")
	}
	if (cfg.TenantMAC == "") != (cfg.TenantCIDR == "") {
		return fmt.Errorf("seed: tenant MAC and CIDR must be set together")
	}
	if cfg.LoginUser == "" {
		cfg.LoginUser = "ubuntu"
	}

	iw, err := iso9660.NewWriter()
	if err != nil {
		return fmt.Errorf("seed: iso writer: %w", err)
	}
	defer iw.Cleanup() //nolint:errcheck // best-effort temp cleanup

	files := map[string]string{
		"meta-data":      metaData(cfg),
		"user-data":      userData(cfg),
		"network-config": networkConfig(cfg),
	}
	for name, content := range files {
		if err := iw.AddFile(bytes.NewReader([]byte(content)), name); err != nil {
			return fmt.Errorf("seed: add %s: %w", name, err)
		}
	}
	if err := iw.WriteTo(w, VolumeLabel); err != nil {
		return fmt.Errorf("seed: write iso: %w", err)
	}
	return nil
}

func metaData(cfg Config) string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", cfg.InstanceID, cfg.Hostname)
}

func userData(cfg Config) string {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "hostname: %s\n", cfg.Hostname)
	fmt.Fprintf(&b, "users:\n  - name: %s\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n", cfg.LoginUser)
	if cfg.SSHAuthorizedKey != "" {
		fmt.Fprintf(&b, "    ssh_authorized_keys:\n      - %s\n", cfg.SSHAuthorizedKey)
	}
	b.WriteString("ssh_pwauth: false\n")
	return b.String()
}

func networkConfig(cfg Config) string {
	var b strings.Builder
	b.WriteString("version: 2\nethernets:\n")
	// Management: DHCP from our NAT network's dnsmasq; owns default route.
	b.WriteString("  mgmt:\n")
	fmt.Fprintf(&b, "    match: {macaddress: \"%s\"}\n", cfg.MgmtMAC)
	b.WriteString("    set-name: mgmt0\n    dhcp4: true\n")
	if cfg.TenantMAC != "" {
		// Tenant: static, NO default route — tenant networks carry tenant
		// traffic, never the guest's way out (D9).
		b.WriteString("  tenant:\n")
		fmt.Fprintf(&b, "    match: {macaddress: \"%s\"}\n", cfg.TenantMAC)
		b.WriteString("    set-name: tenant0\n    dhcp4: false\n")
		fmt.Fprintf(&b, "    addresses: [%s]\n", cfg.TenantCIDR)
	}
	return b.String()
}
