package process

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilePIDTracker_AddRemove(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "test-pids.json"),
	})

	// Add a process
	err := tracker.Add("test-proc", 1234, 1234, "/home/user/project")
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's tracked
	tracking := tracker.Load()
	if len(tracking.Processes) != 1 {
		t.Errorf("expected 1 process, got %d", len(tracking.Processes))
	}
	if tracking.Processes[0].ID != "test-proc" {
		t.Errorf("expected ID 'test-proc', got %q", tracking.Processes[0].ID)
	}
	if tracking.Processes[0].PID != 1234 {
		t.Errorf("expected PID 1234, got %d", tracking.Processes[0].PID)
	}

	// Remove the process
	err = tracker.Remove("test-proc", "/home/user/project")
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's gone
	tracking = tracker.Load()
	if len(tracking.Processes) != 0 {
		t.Errorf("expected 0 processes, got %d", len(tracking.Processes))
	}
}

func TestFilePIDTracker_SetDaemonPID(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "test-pids.json"),
	})

	currentPID := os.Getpid()
	err := tracker.SetDaemonPID(currentPID)
	if err != nil {
		t.Fatal(err)
	}

	tracking := tracker.Load()
	if tracking.DaemonPID != currentPID {
		t.Errorf("expected daemon PID %d, got %d", currentPID, tracking.DaemonPID)
	}
}

func TestFilePIDTracker_CleanupOrphans(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "test-pids.json"),
	})

	// Simulate a previous daemon session
	err := tracker.SetDaemonPID(99999) // A PID that doesn't exist
	if err != nil {
		t.Fatal(err)
	}

	// Add a tracked process (use a non-existent PID)
	err = tracker.Add("old-proc", 88888, 88888, "/home/user/project")
	if err != nil {
		t.Fatal(err)
	}

	// Cleanup orphans with current daemon PID
	currentPID := os.Getpid()
	killedCount, err := tracker.CleanupOrphans(currentPID)
	if err != nil {
		t.Fatal(err)
	}

	// PID 88888 shouldn't exist, so no kills expected
	if killedCount != 0 {
		t.Errorf("expected 0 kills, got %d", killedCount)
	}

	// Verify daemon PID was updated and processes cleared
	tracking := tracker.Load()
	if tracking.DaemonPID != currentPID {
		t.Errorf("expected daemon PID %d, got %d", currentPID, tracking.DaemonPID)
	}
	if len(tracking.Processes) != 0 {
		t.Errorf("expected 0 processes after cleanup, got %d", len(tracking.Processes))
	}
}

func TestFilePIDTracker_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test-pids.json")
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{Path: path})

	// Add some data
	err := tracker.Add("test-proc", 1234, 1234, "/project")
	if err != nil {
		t.Fatal(err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected tracking file to exist")
	}

	// Clear
	err = tracker.Clear()
	if err != nil {
		t.Fatal(err)
	}

	// Verify file is gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected tracking file to be removed")
	}
}

func TestFilePIDTracker_DefaultPath(t *testing.T) {
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		AppName: "test-app",
	})

	path := tracker.Path()
	if path == "" {
		t.Error("expected non-empty default path")
	}

	// Should contain app name
	if filepath.Base(filepath.Dir(path)) != "test-app" {
		t.Errorf("expected path to contain 'test-app', got %q", path)
	}
}

func TestFilePIDTracker_ListTracked(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "test-pids.json"),
	})

	// Add multiple processes
	_ = tracker.Add("proc-1", 1001, 1001, "/project-a")
	_ = tracker.Add("proc-2", 1002, 1002, "/project-a")
	_ = tracker.Add("proc-3", 1003, 1003, "/project-b")

	tracked := tracker.ListTracked()
	if len(tracked) != 3 {
		t.Errorf("expected 3 tracked processes, got %d", len(tracked))
	}
}
