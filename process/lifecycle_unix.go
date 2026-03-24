//go:build !windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// signalProcessGroup sends a signal to the process group AND all descendants.
// This catches grandchildren that created new process groups/sessions.
func (pm *ProcessManager) signalProcessGroup(pid int, sig syscall.Signal) error {
	// Signal the process group
	pgid, err := syscall.Getpgid(pid)
	if err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	} else {
		_ = syscall.Kill(pid, sig)
	}

	// Also signal all descendants (catches escapees in different groups)
	for _, childPID := range findAllDescendants(pid) {
		_ = syscall.Kill(childPID, sig)
	}

	return nil
}

// cleanupProcessTree kills all descendants of a process, including those that
// escaped to a new session/process group (e.g., dotnet watch → dotnet app).
// First signals the process group, then walks the descendant tree via /proc
// or pgrep to catch escapees.
func cleanupProcessTree(pid int) {
	// 1. Kill the process group (catches most children)
	pgid, err := syscall.Getpgid(pid)
	if err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// 2. Walk the descendant tree to catch children in different groups
	descendants := findAllDescendants(pid)
	for _, childPID := range descendants {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	}
}

// findAllDescendants returns all descendant PIDs recursively.
// Tries /proc (Linux) first, falls back to pgrep (macOS/BSD).
func findAllDescendants(pid int) []int {
	// Try /proc/PID/task/PID/children (Linux)
	childrenFile := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	if data, err := os.ReadFile(childrenFile); err == nil {
		return parseProcChildren(data)
	}

	// Fallback: pgrep -P PID
	return pgrepChildren(pid)
}

func parseProcChildren(data []byte) []int {
	var result []int
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			continue
		}
		result = append(result, pid)
		// Recurse
		result = append(result, findAllDescendants(pid)...)
	}
	return result
}

func pgrepChildren(pid int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var result []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		childPID, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || childPID <= 0 {
			continue
		}
		result = append(result, childPID)
		result = append(result, pgrepChildren(childPID)...)
	}
	return result
}

// Backward compat alias
func cleanupProcessGroup(pgid int) {
	cleanupProcessTree(pgid)
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
