//go:build !windows

package process

import "syscall"

// killOrphanProcess kills an orphan process and its process group.
func killOrphanProcess(pid, pgid int) {
	// Try to kill the process group first (gets all children)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	// Also try direct kill in case process group fails
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
