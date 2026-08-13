//go:build linux

package libvirt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// FileImageCache resolves image names to pre-seeded backing files under
// <root>/cache/<name>.qcow2. A missing image is an error — the driver never
// downloads silently (cache admission is pinned + verified,
// performed by the spike/demo, not the hot path).
type FileImageCache struct {
	Root string // storage root; images live under Root/cache
}

// Resolve returns the backing path and its virtual size, validating that the
// cached file is a standalone qcow2 (no backing of its own, no external data
// file) — a layered base would escape the one-layer refcount model.
func (c FileImageCache) Resolve(ctx context.Context, image string) (string, uint64, error) {
	path := filepath.Join(c.Root, "cache", image+".qcow2")
	if _, err := os.Stat(path); err != nil {
		return "", 0, fmt.Errorf("image %q not in cache at %s (seed it: qemu-img convert the cloud image): %w", image, path, err)
	}
	info, err := probeStandalone(ctx, path)
	if err != nil {
		return "", 0, err
	}
	return path, info.VirtualSize, nil
}

type imgInfo struct {
	Format         string `json:"format"`
	VirtualSize    uint64 `json:"virtual-size"`
	BackingFile    string `json:"backing-filename"`
	FormatSpecific *struct {
		Data *struct {
			DataFile string `json:"data-file"`
		} `json:"data"`
	} `json:"format-specific"`
}

func probeStandalone(ctx context.Context, path string) (imgInfo, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "qemu-img", "info", "-U", "--output=json", path)
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return imgInfo{}, fmt.Errorf("qemu-img info: %w", err)
	}
	var info imgInfo
	if err := json.Unmarshal(buf.Bytes(), &info); err != nil {
		return imgInfo{}, err
	}
	if info.Format != "qcow2" {
		return imgInfo{}, fmt.Errorf("cache image %s is %q, want qcow2", path, info.Format)
	}
	if info.BackingFile != "" {
		return imgInfo{}, fmt.Errorf("cache image %s has its own backing file — must be standalone", path)
	}
	if info.FormatSpecific != nil && info.FormatSpecific.Data != nil && info.FormatSpecific.Data.DataFile != "" {
		return imgInfo{}, fmt.Errorf("cache image %s uses an external data file — must be standalone", path)
	}
	return info, nil
}
