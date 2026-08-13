//go:build linux

// Package qcow2 runner: the Linux executor behind the portable core. It
// turns the pure argv/layout/validation logic into real filesystem effects
// with the durability ordering the plan requires: create to a temp
// name, fdatasync the file, publish with no-replace semantics, then fsync
// the containing directory — rename alone does not survive a host crash.
// Teardown is symmetric: unlink, then fsync the directory, before any
// receipt is issued.
package qcow2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

// Runner executes qcow2 operations under a canonical storage root.
type Runner struct {
	Layout Layout
}

// NewRunner returns a runner rooted at root (e.g. /var/lib/vmc).
func NewRunner(root string) *Runner { return &Runner{Layout: Layout{Root: root}} }

// parseVMID parses a VM UUID string.
func parseVMID(vmID string) (uuid.UUID, error) {
	id, err := uuid.Parse(vmID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("qcow2: bad vm id %q: %w", vmID, err)
	}
	return id, nil
}

// EnsureOverlay converges the root overlay for (node, vm, epoch): if a valid
// file already exists (format/size/backing validated) it is a no-op; a
// mismatched pre-existing file is a conflict; otherwise it is created
// durably. Returns the published path.
func (r *Runner) EnsureOverlay(ctx context.Context, node, vmID string, epoch int64, backingPath string, sizeBytes uint64) (string, error) {
	id, err := parseVMID(vmID)
	if err != nil {
		return "", err
	}
	final := r.Layout.RootDisk(node, id, epoch)
	if !r.Layout.Contains(final) {
		return "", fmt.Errorf("qcow2: refusing to create outside the storage root: %s", final)
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0o2775); err != nil {
		return "", err
	}

	// Validated-ensure: accept a pre-existing final file only if it matches.
	if info, err := r.probe(ctx, final); err == nil {
		if verr := Validate(info, backingPath, sizeBytes); verr != nil {
			return "", fmt.Errorf("qcow2: existing file is a conflict: %w", verr)
		}
		return final, nil // already converged
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	tmp := TempFor(final)
	_ = os.Remove(tmp) // clear any crash garbage
	args, err := CreateOverlayArgs(backingPath, tmp, sizeBytes)
	if err != nil {
		return "", err
	}
	if err := run(ctx, args); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := publishDurable(tmp, final, dir); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// WriteSeed writes the cloud-init seed ISO durably into the epoch dir.
func (r *Runner) WriteSeed(node, vmID string, epoch int64, iso []byte) (string, error) {
	id, err := parseVMID(vmID)
	if err != nil {
		return "", err
	}
	final := r.Layout.SeedISO(node, id, epoch)
	if !r.Layout.Contains(final) {
		return "", fmt.Errorf("qcow2: refusing to write outside the storage root: %s", final)
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0o2775); err != nil {
		return "", err
	}
	// Idempotent: the seed is content-deterministic per (vm, epoch), so an
	// existing seed is the correct one — a redelivery must not fail on the
	// no-replace publish.
	if _, err := os.Stat(final); err == nil {
		return final, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp := TempFor(final)
	_ = os.Remove(tmp) // clear any crash garbage
	if err := os.WriteFile(tmp, iso, 0o644); err != nil {
		return "", err
	}
	if err := fsyncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := publishDurable(tmp, final, dir); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// TeardownEpoch removes the entire (node, vm, epoch) artifact directory,
// then fsyncs its parent — durable teardown, before any receipt is issued.
// Idempotent. Refuses to touch anything outside the storage
// root, re-verifying containment at the moment of deletion.
func (r *Runner) TeardownEpoch(node, vmID string, epoch int64) error {
	id, err := parseVMID(vmID)
	if err != nil {
		return err
	}
	dir := r.Layout.VolumeDir(node, id, epoch)
	if !r.Layout.StrictlyInside(dir) {
		return fmt.Errorf("qcow2: refusing to remove outside the storage root: %s", dir)
	}
	// Reject symlinked components: a swapped symlink must not redirect the
	// removal outside the root.
	if err := assertNoSymlink(r.Layout.Root, dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(dir))
}

// probe runs `qemu-img info` and returns the head of the backing chain.
func (r *Runner) probe(ctx context.Context, path string) (Info, error) {
	if _, err := os.Stat(path); err != nil {
		return Info{}, err
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, InfoArgs(path)[0], InfoArgs(path)[1:]...)
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return Info{}, fmt.Errorf("qemu-img info: %w", err)
	}
	var chain []Info
	if err := json.Unmarshal(buf.Bytes(), &chain); err != nil || len(chain) == 0 {
		return Info{}, fmt.Errorf("qemu-img info parse: %w", err)
	}
	return chain[0], nil
}

// --- durability primitives --------------------------------------------------

func publishDurable(tmp, final, dir string) error {
	if err := fsyncFile(tmp); err != nil {
		return err
	}
	// No-replace publish: link-then-remove fails (rather than clobbers) if
	// the final name already appeared — a concurrent publisher does not win
	// by overwriting.
	if err := os.Link(tmp, final); err != nil {
		return fmt.Errorf("qcow2: no-replace publish: %w", err)
	}
	if err := os.Remove(tmp); err != nil {
		return err
	}
	return fsyncDir(dir)
}

func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // explicit Close below; defer is a safety net
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func fsyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck // explicit Close below; defer is a safety net
	if err := d.Sync(); err != nil {
		return err
	}
	return d.Close()
}

// assertNoSymlink walks from root to target and fails if any component is a
// symlink — the removal path must be exactly what its name says.
func assertNoSymlink(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	cur := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // nothing there to delete
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("qcow2: refusing to follow symlink component: %s", cur)
		}
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Own process group so a killed parent takes the child with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
