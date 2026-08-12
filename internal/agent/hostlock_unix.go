//go:build !windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireHostLock takes an OS-held exclusive lock via flock on an open file
// descriptor (batch-review finding [33]): the lock releases automatically
// on process death — no PID-file races, no stale-lock stealing, no unlink
// of another process's replacement. One daemon per host, guaranteed by the
// kernel.
func acquireHostLock(stateDir string) (func(), error) {
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "vmc-host")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "host.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("host lock held by another process: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
