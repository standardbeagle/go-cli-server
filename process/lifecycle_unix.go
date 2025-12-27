//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// setProcAttr sets platform-specific process attributes for Unix systems.
// Each child process gets its own process group so we can signal it
// independently without affecting other processes or the parent daemon.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group for this process
	}
}

// signalProcessGroup sends a signal to the process group.
func (pm *ProcessManager) signalProcessGroup(pid int, sig syscall.Signal) error {
	pgid, err := syscall.Getpgid(pid)
	if err == nil && pgid > 0 {
		return syscall.Kill(-pgid, sig)
	}
	return syscall.Kill(pid, sig)
}

// signalTerm sends SIGTERM to the process.
func signalTerm(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// signalKill sends SIGKILL to the process.
func signalKill(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}

// isProcessAlive checks if a process is still running.
func isProcessAlive(pid int) bool {
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// isNoSuchProcess returns true if the error indicates the process doesn't exist.
func isNoSuchProcess(err error) bool {
	return err == syscall.ESRCH
}

// getProcessGroupID returns the process group ID for a given PID.
func getProcessGroupID(pid int) int {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return pid
	}
	return pgid
}

// SetupJobObject is a no-op on Unix.
func SetupJobObject(cmd *exec.Cmd) error {
	return nil
}

// CleanupJobObject is a no-op on Unix.
func CleanupJobObject(pid int) {
}
