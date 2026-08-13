package domainxml

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedID = uuid.MustParse("7e2f8a1c-9d3b-4e5f-8a6b-1c2d3e4f5a6b")

func fullConfig() Config {
	return Config{
		Name: "web-1", VMID: fixedID, Node: "node-a", Epoch: 3,
		CPUs: 2, MemoryBytes: 2 << 30,
		DiskPath:     "/var/lib/vmc/node-a/" + fixedID.String() + "/3/root.qcow2",
		SeedPath:     "/var/lib/vmc/node-a/" + fixedID.String() + "/3/seed.iso",
		MgmtNetwork:  "vmc-mgmt",
		TenantBridge: "vmc-int", TenantVLAN: 100,
	}
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (run with -update): %v", name, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch for %s:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestGoldenFull(t *testing.T) {
	got, err := Build(fullConfig())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "full.xml", got)
}

func TestGoldenMgmtOnly(t *testing.T) {
	cfg := fullConfig()
	cfg.TenantBridge = ""
	cfg.SeedPath = ""
	got, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "mgmt-only.xml", got)
}

// TestDeterministicIdentity: same (vm, nic) → same MAC and interfaceid,
// always — the property port verification and cloud-init matching stand on.
func TestDeterministicIdentity(t *testing.T) {
	// Two independent derivations (fresh UUID values, same content).
	sameVM := uuid.MustParse(fixedID.String())
	if MAC(fixedID, 0) != MAC(sameVM, 0) || MAC(fixedID, 0) == MAC(fixedID, 1) {
		t.Fatal("MAC determinism/distinctness broken")
	}
	if !strings.HasPrefix(MAC(fixedID, 0), "52:54:00:") {
		t.Fatalf("MAC prefix: %s", MAC(fixedID, 0))
	}
	if InterfaceID(fixedID, 1) != InterfaceID(sameVM, 1) {
		t.Fatal("interfaceid not deterministic")
	}
	otherVM := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	if MAC(fixedID, 0) == MAC(otherVM, 0) {
		t.Fatal("different VMs must get different MACs")
	}
}

// TestOwnershipRoundTrip: what Build stamps, ParseOwnership recovers —
// the owner-scoped resync contract.
func TestOwnershipRoundTrip(t *testing.T) {
	xmlStr, err := Build(fullConfig())
	if err != nil {
		t.Fatal(err)
	}
	own, err := ParseOwnership(xmlStr)
	if err != nil || own == nil {
		t.Fatalf("parse: %v %+v", err, own)
	}
	if own.Node != "node-a" || own.VMID != fixedID || own.Epoch != 3 {
		t.Fatalf("ownership round-trip: %+v", own)
	}
}

// TestForeignDomainIsNil: a domain without our namespace parses as foreign
// (nil, no error) — never adopted, never touched.
func TestForeignDomainIsNil(t *testing.T) {
	foreign := `<domain type='kvm'><name>someone-elses</name><metadata/></domain>`
	own, err := ParseOwnership(foreign)
	if err != nil || own != nil {
		t.Fatalf("foreign domain: %v %+v", err, own)
	}
}

// TestNoAutostartEverRendered: the XML must never contain autostart —
// an unfenced boot after host restart would bypass every grant.
func TestNoAutostartEverRendered(t *testing.T) {
	xmlStr, err := Build(fullConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(xmlStr, "autostart") {
		t.Fatal("autostart must never appear in domain XML")
	}
}

// TestIdentityValidation: identity-free configs are rejected loudly.
func TestIdentityValidation(t *testing.T) {
	bad := fullConfig()
	bad.Epoch = 0
	if _, err := Build(bad); err == nil {
		t.Fatal("epoch 0 must be rejected")
	}
	bad = fullConfig()
	bad.VMID = uuid.Nil
	if _, err := Build(bad); err == nil {
		t.Fatal("nil VM id must be rejected")
	}
}

// TestXMLEscaping: hostile names cannot break out of the document.
func TestXMLEscaping(t *testing.T) {
	cfg := fullConfig()
	cfg.DiskPath = `/var/lib/vmc/it's<a>&path.qcow2`
	xmlStr, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(xmlStr, "<a>&path") {
		t.Fatal("unescaped injection in source path")
	}
}
