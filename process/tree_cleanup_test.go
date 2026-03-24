package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTreeCleanup_DeepProcessTree verifies that stopping a process kills
// the entire process tree, including grandchildren that may have created
// their own process groups.
func TestTreeCleanup_DeepProcessTree(t *testing.T) {
	pm := NewProcessManager(DefaultManagerConfig())
	ctx := context.Background()

	// Start a script that spawns a grandchild in a new process group.
	// sh → sh -c "setsid sleep 60" creates a sleep in a new session/group.
	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:      "deep-tree",
		Command: "sh",
		Args:    []string{"-c", "sh -c 'setsid sleep 60 &' && sleep 60"},
	})
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Find the grandchild sleep process
	grandchildren := findDescendants(proc.cmd.Process.Pid)
	if len(grandchildren) == 0 {
		t.Skip("Could not detect grandchild processes (may be platform-specific)")
	}

	t.Logf("Process tree: pid=%d, descendants=%v", proc.cmd.Process.Pid, grandchildren)

	// Stop the process
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pm.StopProcess(stopCtx, proc)

	// Wait a moment for cleanup
	time.Sleep(500 * time.Millisecond)

	// ALL descendants should be dead
	for _, pid := range grandchildren {
		if isProcessAlive(pid) {
			t.Errorf("Grandchild PID %d is still alive after StopProcess", pid)
			// Clean up for test hygiene
			_ = signalKill(pid)
		}
	}
}

// TestTreeCleanup_SetsidGrandchild verifies cleanup of a grandchild that
// explicitly creates a new session (setsid), which is what dotnet does.
func TestTreeCleanup_SetsidGrandchild(t *testing.T) {
	pm := NewProcessManager(DefaultManagerConfig())
	ctx := context.Background()

	// Create a marker file that the grandchild will hold open
	markerFile := fmt.Sprintf("/tmp/tree-cleanup-test-%d", os.Getpid())
	defer os.Remove(markerFile)

	// Script: spawn a grandchild in a new session that writes to a marker file
	script := fmt.Sprintf(`
		setsid sh -c 'echo $$ > %s; sleep 60' &
		sleep 60
	`, markerFile)

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:      "setsid-test",
		Command: "sh",
		Args:    []string{"-c", script},
	})
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Wait for grandchild to write its PID
	var grandchildPID int
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		data, err := os.ReadFile(markerFile)
		if err == nil && len(data) > 0 {
			pidStr := strings.TrimSpace(string(data))
			grandchildPID, _ = strconv.Atoi(pidStr)
			break
		}
	}

	if grandchildPID == 0 {
		t.Skip("Grandchild didn't write PID (may be platform-specific)")
	}

	t.Logf("Grandchild PID (in new session): %d", grandchildPID)

	// Verify grandchild is alive
	if !isProcessAlive(grandchildPID) {
		t.Fatal("Grandchild should be alive before stop")
	}

	// Stop the parent
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pm.StopProcess(stopCtx, proc)

	time.Sleep(500 * time.Millisecond)

	// Grandchild should be dead (allow a moment for signal delivery)
	dead := false
	for i := 0; i < 10; i++ {
		if !isProcessAlive(grandchildPID) {
			dead = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !dead {
		t.Errorf("Setsid grandchild PID %d is still alive after StopProcess", grandchildPID)
		_ = signalKill(grandchildPID)
	}
}

// findDescendants returns all descendant PIDs for a given PID.
// Uses /proc on Linux, falls back to pgrep on macOS.
func findDescendants(pid int) []int {
	// Try /proc first (Linux)
	childrenFile := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	if data, err := os.ReadFile(childrenFile); err == nil {
		return parseDescendantsRecursive(pid, string(data))
	}

	// Fallback: pgrep -P (works on macOS and Linux)
	return pgrepDescendants(pid)
}

func parseDescendantsRecursive(parentPid int, childrenStr string) []int {
	var result []int
	for _, field := range strings.Fields(childrenStr) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			continue
		}
		result = append(result, pid)
		// Recurse into children's children
		childFile := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
		if data, err := os.ReadFile(childFile); err == nil {
			result = append(result, parseDescendantsRecursive(pid, string(data))...)
		}
	}
	return result
}

func pgrepDescendants(pid int) []int {
	cmd := exec.Command("pgrep", "-P", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	var result []int
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if childPid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && childPid > 0 {
			result = append(result, childPid)
			// Recurse
			result = append(result, pgrepDescendants(childPid)...)
		}
	}
	return result
}
