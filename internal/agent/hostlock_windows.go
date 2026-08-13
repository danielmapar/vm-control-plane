//go:build windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireHostLock takes an OS-held exclusive lock via LockFileEx on an open
// handle: Windows releases it when the process
// dies, so there is no PID-file race. One daemon per host.
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
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY.
	if err := windows.LockFileEx(h, 0x00000002|0x00000001, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("host lock held by another process: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		_ = f.Close()
	}, nil
}
