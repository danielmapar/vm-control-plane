//go:build linux

// Integration tests for the real libvirt driver. Gated behind VMC_LIBVIRT_IT
// because they need a live libvirtd + KVM + a seeded image cache — i.e. the
// Tier-1 substrate VM (deploy/vagrant). Run inside the VM:
//
//	./scripts/spike/seed-image.sh
//	VMC_LIBVIRT_IT=1 sudo -E env "PATH=$PATH" go test ./internal/driver/compute/libvirt/ -run TestIT -v -timeout 15m
//
// (sudo/service identity so /dev/kvm, the libvirt system socket, and the
// storage root are all writable.)
package libvirt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
)

func requireIT(t *testing.T) {
	t.Helper()
	if os.Getenv("VMC_LIBVIRT_IT") != "1" {
		t.Skip("set VMC_LIBVIRT_IT=1 (inside the substrate VM) to run libvirt integration tests")
	}
}

func testDriver(t *testing.T) (*Driver, string) {
	t.Helper()
	root := "/var/lib/vmc"
	// Ephemeral SSH keypair for this run.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	cache := FileImageCache{Root: root}
	d := New(Config{
		StorageRoot:      root,
		MgmtNetwork:      "vmc-mgmt-it",
		SSHAuthorizedKey: strings.TrimSpace(string(pub)),
		ResolveBacking:   cache.Resolve,
		StopDeadline:     30 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.EnsureMgmtNetwork(ctx); err != nil {
		t.Fatalf("ensure mgmt network: %v", err)
	}
	// A test that brings up a host-level libvirt network must take it back
	// down: left active, vmc-mgmt-it holds 192.168.221.0/24 and collides with
	// the demo's vmc-mgmt on the same subnet (surfaced by the capstone run).
	// Registered here so it runs last (LIFO) — after each test's domain
	// teardown, which is registered later.
	t.Cleanup(func() {
		_ = exec.Command("virsh", "-c", "qemu:///system", "net-destroy", "vmc-mgmt-it").Run()
		_ = exec.Command("virsh", "-c", "qemu:///system", "net-undefine", "vmc-mgmt-it").Run()
	})
	return d, keyPath
}

func vmConfig(name string, epoch int64) compute.VMConfig {
	return compute.VMConfig{
		VMID: uuid.NewString(), Name: name, Node: "node-a", Epoch: epoch,
		CPUs: 1, MemoryB: 1 << 30, Image: "ubuntu-24.04", DiskB: 4 << 30,
		Running: true,
	}
}

// TestITBootSSHTeardown is the flagship real-KVM proof: a real domain boots,
// gets a DHCP lease on the management network, answers key-only SSH after
// cloud-init, and tears down cleanly with the storage removed.
func TestITBootSSHTeardown(t *testing.T) {
	requireIT(t)
	d, keyPath := testDriver(t)
	cfg := vmConfig("vmc-it-boot", 1)
	ctx := context.Background()

	t.Cleanup(func() {
		tctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = d.Teardown(tctx, cfg.VMID, cfg.Epoch)
	})

	ectx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	state, err := d.Ensure(ectx, cfg)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if state != compute.StateRunning {
		t.Fatalf("state after ensure: %v", state)
	}

	// Ensure is idempotent: a second call converges to the same running VM.
	if _, err := d.Ensure(ectx, cfg); err != nil {
		t.Fatalf("ensure (idempotent replay): %v", err)
	}

	// Wait for a DHCP lease (proves the guest kernel booted + configured the
	// mgmt NIC via cloud-init).
	var ip string
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := d.MgmtIP(ctx, cfg.VMID)
		if err != nil {
			t.Fatalf("mgmt ip: %v", err)
		}
		if got != "" {
			ip = got
			break
		}
		time.Sleep(3 * time.Second)
	}
	if ip == "" {
		t.Fatal("no DHCP lease within timeout — guest did not boot/configure networking")
	}
	t.Logf("guest mgmt IP: %s", ip)

	// Key-only SSH to a cloud-init-configured guest.
	if err := waitSSH(t, keyPath, ip, 2*time.Minute); err != nil {
		t.Fatalf("ssh: %v", err)
	}

	// Teardown removes the domain and its storage.
	if err := d.Teardown(ctx, cfg.VMID, cfg.Epoch); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	obs, err := d.Observe(ctx, cfg.VMID, cfg.Epoch)
	if err != nil || obs != compute.StateAbsent {
		t.Fatalf("post-teardown observe: %v %v", obs, err)
	}
	diskDir := filepath.Join("/var/lib/vmc", cfg.Node, cfg.VMID)
	if _, err := os.Stat(filepath.Join(diskDir, "1")); !os.IsNotExist(err) {
		t.Fatalf("epoch storage not removed: %v", err)
	}
}

// TestITStartStop exercises the power ladder against a real domain.
func TestITStartStop(t *testing.T) {
	requireIT(t)
	d, _ := testDriver(t)
	cfg := vmConfig("vmc-it-power", 1)
	ctx := context.Background()
	t.Cleanup(func() {
		tctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = d.Teardown(tctx, cfg.VMID, cfg.Epoch)
	})

	if _, err := d.Ensure(ctx, cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Give the guest a moment so ACPI shutdown is honored.
	time.Sleep(20 * time.Second)

	cfg.Running = false
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	state, err := d.Ensure(sctx, cfg)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state != compute.StateShutoff {
		t.Fatalf("state after stop: %v", state)
	}
}

// TestITForeignDomainNotAdopted: a same-named domain we did not create is a
// conflict — Ensure refuses to touch it.
func TestITForeignDomainNotAdopted(t *testing.T) {
	requireIT(t)
	d, _ := testDriver(t)
	name := "vmc-it-foreign"

	// Define a foreign domain (no vmc ownership metadata) via virsh.
	foreignXML := fmt.Sprintf(`<domain type='kvm'><name>%s</name><memory unit='MiB'>128</memory><vcpu>1</vcpu><os><type arch='x86_64'>hvm</type></os></domain>`, name)
	tmp := filepath.Join(t.TempDir(), "foreign.xml")
	if err := os.WriteFile(tmp, []byte(foreignXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("virsh", "-c", "qemu:///system", "define", tmp).CombinedOutput(); err != nil {
		t.Fatalf("define foreign: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("virsh", "-c", "qemu:///system", "undefine", name).Run()
	})

	cfg := vmConfig(name, 1)
	_, err := d.Ensure(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("ensure must refuse a foreign same-named domain, got %v", err)
	}
}

func waitSSH(t *testing.T, keyPath, ip string, within time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		cmd := exec.Command("ssh",
			"-i", keyPath,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=5",
			"-o", "BatchMode=yes",
			"ubuntu@"+ip,
			"cloud-init status --wait >/dev/null 2>&1; echo vmc-ssh-ok")
		out, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "vmc-ssh-ok") {
			return nil
		}
		last = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("ssh never succeeded: %w", last)
}
