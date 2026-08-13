//go:build linux

package libvirt

import (
	"fmt"
	"os"
	"path/filepath"
)

// teardownStorage removes the (vm, epoch) storage directory under whatever
// node owns it — scanned, because the Teardown interface has vm+epoch but
// not node. Contained and idempotent.
func (d *Driver) teardownStorage(vmID string, epoch int64) error {
	entries, err := os.ReadDir(d.cfg.StorageRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "cache" {
			continue
		}
		node := e.Name()
		dir := filepath.Join(d.cfg.StorageRoot, node, vmID, fmt.Sprintf("%d", epoch))
		if _, err := os.Stat(dir); err == nil {
			if err := d.runner.TeardownEpoch(node, vmID, epoch); err != nil {
				return err
			}
		}
	}
	return nil
}
