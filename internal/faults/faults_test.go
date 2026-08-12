package faults

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnarmedIsFree(t *testing.T) {
	_ = Load("")
	if err := Hit(context.Background(), "api.after-envelope"); err != nil {
		t.Fatalf("unarmed failpoint returned %v", err)
	}
}

func TestUnknownIDPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unknown failpoint ID must panic — the manifest cannot drift silently")
		}
	}()
	_ = Hit(context.Background(), "no.such.point")
}

func TestErrorInjection(t *testing.T) {
	mustLoad(t, "api.after-envelope=error:boom")
	t.Cleanup(func() { _ = Load("") })
	err := Hit(context.Background(), "api.after-envelope")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want injected boom, got %v", err)
	}
}

func TestHangRespectsContext(t *testing.T) {
	mustLoad(t, "controller.after-claim=hang:30s")
	t.Cleanup(func() { _ = Load("") })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Hit(ctx, "controller.after-claim")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("hang ignored context")
	}
}

func TestPauseAndRelease(t *testing.T) {
	mustLoad(t, "controller.before-complete=pause")
	t.Cleanup(func() { _ = Load("") })

	done := make(chan error, 1)
	go func() { done <- Hit(context.Background(), "controller.before-complete") }()

	select {
	case <-done:
		t.Fatal("pause returned before Release")
	case <-time.After(100 * time.Millisecond):
	}
	Release("controller.before-complete")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("released pause returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Release did not unblock the pause")
	}
	// Double release is a no-op.
	Release("controller.before-complete")
}

// TestCrashExits re-executes the test binary with the crash action armed
// and asserts exit code 137 — the subprocess harness pattern in miniature.
func TestCrashExits(t *testing.T) {
	if os.Getenv("FAULTS_CRASH_CHILD") == "1" {
		_ = Load("controller.after-claim=crash")
		_ = Hit(context.Background(), "controller.after-claim")
		t.Fatal("unreachable")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run", "TestCrashExits")
	cmd.Env = append(os.Environ(), "FAULTS_CRASH_CHILD=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 137 {
		t.Fatalf("want exit 137, got %v", err)
	}
}

// TestManifestCoversCatalog: every Catalog entry documents its cut and
// invariant — the rendered manifest (docs/failpoints.md) is generated from
// this table, so an empty field would ship an empty manifest row.
func TestManifestCoversCatalog(t *testing.T) {
	for id, p := range Catalog {
		if p.ID != id {
			t.Errorf("%s: ID mismatch %q", id, p.ID)
		}
		if p.Cut == "" || p.Invariant == "" {
			t.Errorf("%s: manifest fields incomplete", id)
		}
	}
}

func mustLoad(t *testing.T, spec string) {
	t.Helper()
	if err := Load(spec); err != nil {
		t.Fatalf("Load(%q): %v", spec, err)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	t.Cleanup(func() { _ = Load("") })
	for _, bad := range []string{"no.such.id=crash", "api.after-envelope=explode", "api.after-envelope=hang:notaduration", "malformed"} {
		if err := Load(bad); err == nil {
			t.Errorf("Load(%q) must error", bad)
		}
	}
}

// TestManifestMatchesDoc: the rendered manifest body must appear verbatim in
// docs/failpoints.md — the two cannot drift (finding [42]).
func TestManifestMatchesDoc(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "failpoints.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(Manifest(), "\n"), "\n") {
		if !strings.Contains(string(doc), line) {
			t.Errorf("docs/failpoints.md is missing manifest row:\n%s\n(regenerate it from faults.Manifest)", line)
		}
	}
}
