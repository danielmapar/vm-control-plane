// Package qcow2 is the real volume driver's PORTABLE core: command
// construction, path layout, ownership containment, and publication
// ordering are pure logic — golden-tested on every platform — while the
// actual qemu-img execution and fsync-durability live behind a small
// Runner interface whose production implementation is Linux-only.
//
// The sharp edges this package encodes (plan D10, all review-sourced):
//
//   - `qemu-img create -b` WITHOUT `-F` fails on modern qemu-img, and
//     WITHOUT an explicit size silently inherits the backing image's —
//     the requested 10GiB would be ignored;
//   - creation goes to a temp name in the destination directory and is
//     published with no-replace semantics, then the DIRECTORY is fsynced
//     (rename alone is not host-crash durable);
//   - artifact paths are epoch-qualified
//     (<root>/<node>/<vm>/<epoch>/root.qcow2) and every destructive path
//     re-verifies containment under the canonical storage root, rejecting
//     symlinks — an old epoch's late teardown cannot touch a new epoch's
//     files, and nothing outside the root is ever unlinked (D15);
//   - a pre-existing file is accepted only after `qemu-img info` validates
//     format, virtual size, and backing path — existence is not evidence.
package qcow2

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Layout computes epoch-qualified artifact paths under the canonical
// storage root. All paths are slash-normalized absolute.
type Layout struct {
	Root string // e.g. /var/lib/vmc
}

// VolumeDir is the per-placement directory: every artifact of (node, vm,
// epoch) lives under it, and teardown removes exactly this subtree.
func (l Layout) VolumeDir(node string, vmID uuid.UUID, epoch int64) string {
	return filepath.ToSlash(filepath.Join(l.Root, node, vmID.String(), fmt.Sprintf("%d", epoch)))
}

// RootDisk is the VM's root overlay path.
func (l Layout) RootDisk(node string, vmID uuid.UUID, epoch int64) string {
	return l.VolumeDir(node, vmID, epoch) + "/root.qcow2"
}

// SeedISO is the cloud-init seed path.
func (l Layout) SeedISO(node string, vmID uuid.UUID, epoch int64) string {
	return l.VolumeDir(node, vmID, epoch) + "/seed.iso"
}

// parseVMID parses a VM UUID string (shared by the Linux runner).
func parseVMID(vmID string) (uuid.UUID, error) {
	id, err := uuid.Parse(vmID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("qcow2: bad vm id %q: %w", vmID, err)
	}
	return id, nil
}

// TempFor returns the unpublished temp name for a final path. Only the
// final name counts as existing; anything with this suffix is scavenger
// garbage after a crash.
func TempFor(final string) string { return final + ".tmp-unpublished" }

// Contains reports whether path is inside the canonical root — the guard
// every unlink and every teardown MUST pass (D15). Purely lexical here;
// the Linux runner additionally rejects symlinked components at open time
// (O_NOFOLLOW).
func (l Layout) Contains(path string) bool {
	root := filepath.ToSlash(filepath.Clean(l.Root))
	p := filepath.ToSlash(filepath.Clean(path))
	return p != root && strings.HasPrefix(p, root+"/") && !strings.Contains(p, "/../")
}

// CreateOverlayArgs builds the qemu-img argv for a copy-on-write overlay.
// Both review-sourced requirements are load-bearing: explicit `-F qcow2`
// (modern qemu-img refuses backing files without it) and an explicit size
// in bytes (omitting it silently inherits the backing image's size — the
// user's requested capacity would be ignored).
func CreateOverlayArgs(backingPath, tempPath string, sizeBytes uint64) ([]string, error) {
	if sizeBytes == 0 {
		return nil, fmt.Errorf("qcow2: overlay size is mandatory (omission silently inherits the backing size)")
	}
	if backingPath == "" || tempPath == "" {
		return nil, fmt.Errorf("qcow2: backing and temp paths are mandatory")
	}
	return []string{
		"qemu-img", "create",
		"-f", "qcow2",
		"-b", backingPath,
		"-F", "qcow2",
		tempPath,
		fmt.Sprintf("%d", sizeBytes),
	}, nil
}

// CreateBlankArgs builds argv for an empty volume.
func CreateBlankArgs(tempPath string, sizeBytes uint64) ([]string, error) {
	if sizeBytes == 0 || tempPath == "" {
		return nil, fmt.Errorf("qcow2: size and path are mandatory")
	}
	return []string{"qemu-img", "create", "-f", "qcow2", tempPath, fmt.Sprintf("%d", sizeBytes)}, nil
}

// InfoArgs builds the validated-ensure probe: format, virtual size, and
// backing chain are checked against expectations before ANY pre-existing
// file is accepted (existence is not evidence — matrix row 11).
//
// -U (force-share) is REQUIRED here: on idempotent replay the overlay may be
// held by a running QEMU, and a plain `qemu-img info` would fail to get the
// lock (the QEMU image-locking case, plan D10). -U is safe because info is
// read-only — it is only unsafe for mutations, which this never performs.
func InfoArgs(path string) []string {
	return []string{"qemu-img", "info", "-U", "--output=json", "--backing-chain", path}
}

// Info is the subset of `qemu-img info` output the validator consumes.
type Info struct {
	Format      string `json:"format"`
	VirtualSize uint64 `json:"virtual-size"`
	BackingFile string `json:"backing-filename"`
}

// Validate checks a probed file against expectations.
func Validate(got Info, wantBacking string, wantSize uint64) error {
	if got.Format != "qcow2" {
		return fmt.Errorf("qcow2: format %q, want qcow2", got.Format)
	}
	if got.VirtualSize != wantSize {
		return fmt.Errorf("qcow2: virtual size %d, want %d (a mismatched pre-existing file is a conflict, not a convergence)", got.VirtualSize, wantSize)
	}
	if wantBacking != "" && filepath.ToSlash(got.BackingFile) != filepath.ToSlash(wantBacking) {
		return fmt.Errorf("qcow2: backing %q, want %q", got.BackingFile, wantBacking)
	}
	return nil
}

// CachePath is the content-addressed backing-image location: the digest IS
// the filename, so a verified download can be published with no-replace
// semantics and shared by every overlay that references it.
func (l Layout) CachePath(sha256Hex string) (string, error) {
	if len(sha256Hex) != 64 {
		return "", fmt.Errorf("qcow2: cache paths are content-addressed by full sha256")
	}
	return filepath.ToSlash(filepath.Join(l.Root, "cache", "sha256-"+sha256Hex+".qcow2")), nil
}
