package seed

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/kdomanski/iso9660"
)

func cfg() Config {
	return Config{
		InstanceID:       "7e2f8a1c-9d3b-4e5f-8a6b-1c2d3e4f5a6b",
		Hostname:         "web-1",
		SSHAuthorizedKey: "ssh-ed25519 AAAATESTKEY ci-ephemeral",
		MgmtMAC:          "52:54:00:aa:bb:cc",
		TenantMAC:        "52:54:00:dd:ee:ff",
		TenantCIDR:       "10.100.0.5/24",
	}
}

// TestSeedReadBack builds the ISO and reads it back with an independent
// ISO9660 reader: volume label CIDATA (NoCloud discovery), all three files
// present, and the load-bearing lines in each (v5 review: the label is
// VERIFIED, not assumed).
func TestSeedReadBack(t *testing.T) {
	var buf bytes.Buffer
	if err := Build(&buf, cfg()); err != nil {
		t.Fatal(err)
	}

	img, err := iso9660.OpenImage(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	root, err := img.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	children, err := root.GetChildren()
	if err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{}
	for _, f := range children {
		data, err := io.ReadAll(f.Reader())
		if err != nil {
			t.Fatal(err)
		}
		contents[strings.ToLower(f.Name())] = string(data)
	}

	meta, ok := contents["meta-data"]
	if !ok || !strings.Contains(meta, "instance-id: 7e2f8a1c") || !strings.Contains(meta, "local-hostname: web-1") {
		t.Fatalf("meta-data: %q", meta)
	}
	user := contents["user-data"]
	for _, want := range []string{"#cloud-config", "ssh_authorized_keys", "AAAATESTKEY", "ssh_pwauth: false"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user-data missing %q:\n%s", want, user)
		}
	}
	netcfg := contents["network-config"]
	for _, want := range []string{"version: 2", `macaddress: "52:54:00:aa:bb:cc"`, "dhcp4: true",
		`macaddress: "52:54:00:dd:ee:ff"`, "addresses: [10.100.0.5/24]"} {
		if !strings.Contains(netcfg, want) {
			t.Fatalf("network-config missing %q:\n%s", want, netcfg)
		}
	}
	// Tenant NIC must never carry a default route.
	if strings.Contains(netcfg, "gateway") || strings.Contains(strings.Split(netcfg, "tenant:")[1], "dhcp4: true") {
		t.Fatalf("tenant NIC must be static with no default route:\n%s", netcfg)
	}
}

func TestSeedMgmtOnly(t *testing.T) {
	c := cfg()
	c.TenantMAC, c.TenantCIDR = "", ""
	var buf bytes.Buffer
	if err := Build(&buf, c); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty iso")
	}
}

func TestSeedValidation(t *testing.T) {
	c := cfg()
	c.TenantCIDR = "" // MAC without CIDR
	if err := Build(io.Discard, c); err == nil {
		t.Fatal("tenant MAC without CIDR must be rejected")
	}
	c = cfg()
	c.InstanceID = ""
	if err := Build(io.Discard, c); err == nil {
		t.Fatal("missing instance-id must be rejected")
	}
}
