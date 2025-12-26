//go:build windows

package process

import "os"

// killOrphanProcess kills an orphan process on Windows.
// On Windows, we don't have process groups in the Unix sense,
// so we just kill the process directly.
func killOrphanProcess(pid, pgid int) {
	// Try to terminate the process
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}
