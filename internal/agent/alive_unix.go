//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// processAlive: signal 0 probes existence without side effects.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
