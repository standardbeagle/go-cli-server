package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackedProcess_DescendantFields(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")

	// Initially no descendants
	tracked := tracker.ListTracked()
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked process, got %d", len(tracked))
	}
	if len(tracked[0].DescendantPIDs) != 0 {
		t.Errorf("expected no descendants initially, got %v", tracked[0].DescendantPIDs)
	}
	if !tracked[0].LastScanAt.IsZero() {
		t.Error("expected zero LastScanAt initially")
	}
}

func TestUpdateDescendants(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.Add("proc-2", 2000, 2000, "/project")

	// Update descendants for proc-1
	err := tracker.UpdateDescendants(1000, []int{1001, 1002, 1003})
	if err != nil {
		t.Fatal(err)
	}

	// Verify proc-1 has descendants
	tracked := tracker.ListTracked()
	var proc1, proc2 TrackedProcess
	for _, p := range tracked {
		if p.PID == 1000 {
			proc1 = p
		}
		if p.PID == 2000 {
			proc2 = p
		}
	}

	if len(proc1.DescendantPIDs) != 3 {
		t.Fatalf("expected 3 descendants for proc-1, got %d", len(proc1.DescendantPIDs))
	}
	if proc1.DescendantPIDs[0] != 1001 || proc1.DescendantPIDs[1] != 1002 || proc1.DescendantPIDs[2] != 1003 {
		t.Errorf("unexpected descendants: %v", proc1.DescendantPIDs)
	}
	if proc1.LastScanAt.IsZero() {
		t.Error("expected LastScanAt to be set")
	}

	// proc-2 should be unaffected
	if len(proc2.DescendantPIDs) != 0 {
		t.Errorf("expected no descendants for proc-2, got %v", proc2.DescendantPIDs)
	}
}

func TestUpdateDescendants_NonexistentPID(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	// No processes tracked - should be a no-op
	err := tracker.UpdateDescendants(9999, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDescendants_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "pids.json")
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{Path: path})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.UpdateDescendants(1000, []int{1001, 1002})

	// Read the raw file and verify JSON structure
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var tracking PIDTracking
	if err := json.Unmarshal(data, &tracking); err != nil {
		t.Fatal(err)
	}

	if len(tracking.Processes) != 1 {
		t.Fatalf("expected 1 process in file, got %d", len(tracking.Processes))
	}
	if len(tracking.Processes[0].DescendantPIDs) != 2 {
		t.Fatalf("expected 2 descendants in file, got %d", len(tracking.Processes[0].DescendantPIDs))
	}

	// Create a new tracker pointing at the same file and verify persistence
	tracker2 := NewFilePIDTracker(FilePIDTrackerConfig{Path: path})
	tracked := tracker2.ListTracked()
	if len(tracked[0].DescendantPIDs) != 2 {
		t.Errorf("descendants not persisted correctly, got %v", tracked[0].DescendantPIDs)
	}
}

func TestGetDescendants(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.UpdateDescendants(1000, []int{1001, 1002})

	// Get descendants for existing process
	descendants := tracker.GetDescendants(1000)
	if len(descendants) != 2 {
		t.Fatalf("expected 2 descendants, got %d", len(descendants))
	}

	// Get descendants for non-existent PID
	descendants = tracker.GetDescendants(9999)
	if descendants != nil {
		t.Errorf("expected nil for non-existent PID, got %v", descendants)
	}
}

func TestGetVerifiedDescendantsRequiresIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.UpdateDescendants(1000, []int{1001, 1002})

	if got := tracker.GetDescendants(1000); len(got) != 2 {
		t.Fatalf("GetDescendants() len = %d, want 2", len(got))
	}
	if got := tracker.GetVerifiedDescendants(1000); len(got) != 0 {
		t.Fatalf("GetVerifiedDescendants() = %v, want none for descendants without identity", got)
	}
}

func TestListAllPIDs(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.Add("proc-2", 2000, 2000, "/project")
	_ = tracker.UpdateDescendants(1000, []int{1001, 1002})
	_ = tracker.UpdateDescendants(2000, []int{2001})

	allPIDs := tracker.ListAllPIDs()
	expected := map[int]bool{1000: true, 1001: true, 1002: true, 2000: true, 2001: true}

	if len(allPIDs) != len(expected) {
		t.Fatalf("expected %d PIDs, got %d: %v", len(expected), len(allPIDs), allPIDs)
	}
	for _, pid := range allPIDs {
		if !expected[pid] {
			t.Errorf("unexpected PID %d in result", pid)
		}
	}
}

func TestListAllPIDs_Deduplication(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	_ = tracker.Add("proc-1", 1000, 1000, "/project")
	_ = tracker.Add("proc-2", 2000, 2000, "/project")
	// Both processes share a descendant PID (edge case)
	_ = tracker.UpdateDescendants(1000, []int{3000})
	_ = tracker.UpdateDescendants(2000, []int{3000})

	allPIDs := tracker.ListAllPIDs()
	// 1000, 2000, 3000 - no duplicates
	if len(allPIDs) != 3 {
		t.Errorf("expected 3 unique PIDs, got %d: %v", len(allPIDs), allPIDs)
	}
}

func TestListAllPIDs_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	allPIDs := tracker.ListAllPIDs()
	if len(allPIDs) != 0 {
		t.Errorf("expected 0 PIDs, got %d", len(allPIDs))
	}
}

func TestDescendantScanner_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Start scanner with fast interval for testing
	tracker.StartDescendantScanner(ctx, 50*time.Millisecond)

	// Add the current process (will have real descendants to scan)
	currentPID := os.Getpid()
	_ = tracker.Add("self", currentPID, currentPID, "/project")

	// Wait for at least one scan cycle
	time.Sleep(200 * time.Millisecond)

	// Verify LastScanAt was updated
	tracked := tracker.ListTracked()
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked process, got %d", len(tracked))
	}
	if tracked[0].LastScanAt.IsZero() {
		t.Error("expected LastScanAt to be set after scan")
	}

	// Cancel stops the scanner
	cancel()

	// Scanner should stop - verify by checking no panics after context cancel
	time.Sleep(100 * time.Millisecond)
}

func TestDescendantScanner_NoProcesses(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start scanner with no processes - should be a no-op
	tracker.StartDescendantScanner(ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// No panic, no file created (no processes to scan)
	tracked := tracker.ListTracked()
	if len(tracked) != 0 {
		t.Errorf("expected 0 tracked processes, got %d", len(tracked))
	}
}

func TestDescendantScanner_DeadProcess(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	// Add a non-existent PID
	_ = tracker.Add("dead-proc", 99999, 99999, "/project")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker.StartDescendantScanner(ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Dead processes should not have LastScanAt updated
	tracked := tracker.ListTracked()
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked process, got %d", len(tracked))
	}
	if !tracked[0].LastScanAt.IsZero() {
		t.Error("expected LastScanAt to remain zero for dead process")
	}
}

func TestCleanupOrphans_WithDescendants(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	// Simulate a previous daemon with tracked descendants
	_ = tracker.SetDaemonPID(99999) // Non-existent daemon
	_ = tracker.Add("proc-1", 88888, 88888, "/project")
	_ = tracker.UpdateDescendants(88888, []int{77777, 66666})

	// Cleanup orphans - none alive so no kills
	killedCount, err := tracker.CleanupOrphans(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if killedCount != 0 {
		t.Errorf("expected 0 kills (no alive PIDs), got %d", killedCount)
	}

	// Processes should be cleared
	tracked := tracker.ListTracked()
	if len(tracked) != 0 {
		t.Errorf("expected 0 processes after cleanup, got %d", len(tracked))
	}
}

func TestProcessManager_ScannerIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	config := DefaultManagerConfig()
	config.PIDTracker = tracker

	pm := NewProcessManager(config)

	// Scanner should be running (PM started it)
	// Verify by adding a live process and checking scan
	_ = tracker.Add("self", os.Getpid(), os.Getpid(), "/project")

	// Wait for scanner to run
	time.Sleep(6 * time.Second)

	tracked := tracker.ListTracked()
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked process, got %d", len(tracked))
	}
	if tracked[0].LastScanAt.IsZero() {
		t.Error("expected scanner to update LastScanAt")
	}

	// Shutdown should stop scanner without hanging
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pm.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func TestDescendantTracker_Interface(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewFilePIDTracker(FilePIDTrackerConfig{
		Path: filepath.Join(tmpDir, "pids.json"),
	})

	// Verify FilePIDTracker satisfies DescendantTracker
	var dt DescendantTracker = tracker
	if dt == nil {
		t.Fatal("FilePIDTracker should satisfy DescendantTracker")
	}
}
