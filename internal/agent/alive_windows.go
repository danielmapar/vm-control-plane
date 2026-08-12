//go:build windows

package agent

import "os"

// processAlive on Windows: FindProcess succeeds only for live processes we
// can open. Good enough for stale-lock stealing in the fake tier; the real
// tier runs on Linux.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, a found handle means the process object exists.
	_ = p.Release()
	return true
}
