package qcow2

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

var vmID = uuid.MustParse("7e2f8a1c-9d3b-4e5f-8a6b-1c2d3e4f5a6b")

func layout() Layout { return Layout{Root: "/var/lib/vmc"} }

func TestEpochQualifiedPaths(t *testing.T) {
	l := layout()
	disk := l.RootDisk("node-a", vmID, 3)
	want := "/var/lib/vmc/node-a/" + vmID.String() + "/3/root.qcow2"
	if disk != want {
		t.Fatalf("disk path: %s, want %s", disk, want)
	}
	// Epoch 4's artifacts are DIFFERENT paths — an old epoch's late
	// teardown lexically cannot name a new epoch's files.
	if l.RootDisk("node-a", vmID, 4) == disk {
		t.Fatal("epochs must not share paths")
	}
}

func TestContainment(t *testing.T) {
	l := layout()
	good := l.RootDisk("node-a", vmID, 1)
	if !l.Contains(good) {
		t.Fatalf("own artifact must be contained: %s", good)
	}
	for _, bad := range []string{
		"/etc/passwd",
		"/var/lib/vmc/../../etc/shadow",
		"/var/lib/vmc",      // the root itself is never a deletion target
		"/var/lib/vmc2/foo", // prefix confusion
	} {
		if l.Contains(bad) {
			t.Errorf("containment must reject %s", bad)
		}
	}
}

// TestOverlayArgsGolden: the two review-sourced sharp edges are pinned —
// explicit -F qcow2 and explicit byte size.
func TestOverlayArgsGolden(t *testing.T) {
	args, err := CreateOverlayArgs("/var/lib/vmc/cache/sha256-abc.qcow2", "/x/root.qcow2.tmp-unpublished", 10<<30)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "qemu-img create -f qcow2 -b /var/lib/vmc/cache/sha256-abc.qcow2 -F qcow2 /x/root.qcow2.tmp-unpublished 10737418240"
	if got != want {
		t.Fatalf("argv:\n got %s\nwant %s", got, want)
	}
}

func TestOverlaySizeMandatory(t *testing.T) {
	if _, err := CreateOverlayArgs("/b.qcow2", "/t.tmp", 0); err == nil {
		t.Fatal("size 0 must be rejected — omission silently inherits the backing size")
	}
}

func TestValidateEnsure(t *testing.T) {
	ok := Info{Format: "qcow2", VirtualSize: 10 << 30, BackingFile: "/cache/sha256-abc.qcow2"}
	if err := Validate(ok, "/cache/sha256-abc.qcow2", 10<<30); err != nil {
		t.Fatal(err)
	}
	// Wrong size: conflict, never convergence.
	if err := Validate(Info{Format: "qcow2", VirtualSize: 5 << 30}, "", 10<<30); err == nil {
		t.Fatal("size mismatch must be a conflict")
	}
	if err := Validate(Info{Format: "raw", VirtualSize: 10 << 30}, "", 10<<30); err == nil {
		t.Fatal("format mismatch must be a conflict")
	}
	if err := Validate(ok, "/cache/sha256-OTHER.qcow2", 10<<30); err == nil {
		t.Fatal("backing mismatch must be a conflict")
	}
}

func TestCacheContentAddressing(t *testing.T) {
	l := layout()
	p, err := l.CachePath(strings.Repeat("ab", 32))
	if err != nil || !strings.Contains(p, "sha256-"+strings.Repeat("ab", 32)) {
		t.Fatalf("cache path: %s %v", p, err)
	}
	if _, err := l.CachePath("short"); err == nil {
		t.Fatal("truncated digests must be rejected")
	}
}

func TestTempNaming(t *testing.T) {
	final := layout().RootDisk("node-a", vmID, 1)
	tmp := TempFor(final)
	if !strings.HasPrefix(tmp, final) || tmp == final {
		t.Fatalf("temp naming: %s", tmp)
	}
}
