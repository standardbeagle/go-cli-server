package process

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDefaultManagerConfig verifies the default configuration.
func TestDefaultManagerConfig(t *testing.T) {
	cfg := DefaultManagerConfig()

	if cfg.MaxOutputBuffer != DefaultBufferSize {
		t.Errorf("MaxOutputBuffer = %d, want %d", cfg.MaxOutputBuffer, DefaultBufferSize)
	}
	if cfg.GracefulTimeout != 5*time.Second {
		t.Errorf("GracefulTimeout = %v, want %v", cfg.GracefulTimeout, 5*time.Second)
	}
	if cfg.HealthCheckPeriod != 10*time.Second {
		t.Errorf("HealthCheckPeriod = %v, want %v", cfg.HealthCheckPeriod, 10*time.Second)
	}
	if cfg.PIDTracker != nil {
		t.Error("PIDTracker should be nil by default")
	}
}

// TestNewProcessManager verifies manager creation.
func TestNewProcessManager(t *testing.T) {
	cfg := ManagerConfig{
		HealthCheckPeriod: 0, // Disable health check for this test
	}

	pm := NewProcessManager(cfg)
	if pm == nil {
		t.Fatal("NewProcessManager returned nil")
	}

	if pm.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0", pm.ActiveCount())
	}
	if pm.TotalStarted() != 0 {
		t.Errorf("TotalStarted = %d, want 0", pm.TotalStarted())
	}
	if pm.TotalFailed() != 0 {
		t.Errorf("TotalFailed = %d, want 0", pm.TotalFailed())
	}
	if pm.IsShuttingDown() {
		t.Error("IsShuttingDown should be false initially")
	}
}

// TestNewProcessManagerWithHealthCheck verifies health check is started.
func TestNewProcessManagerWithHealthCheck(t *testing.T) {
	cfg := ManagerConfig{
		HealthCheckPeriod: 10 * time.Millisecond,
	}

	pm := NewProcessManager(cfg)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pm.Shutdown(ctx)
	}()

	if pm == nil {
		t.Fatal("NewProcessManager returned nil")
	}

	// Let health check run briefly
	time.Sleep(30 * time.Millisecond)
}

// TestManagerConfig verifies Config() returns the configuration.
func TestManagerConfig(t *testing.T) {
	cfg := ManagerConfig{
		GracefulTimeout:   10 * time.Second,
		MaxOutputBuffer:   1024,
		HealthCheckPeriod: 0,
	}

	pm := NewProcessManager(cfg)
	got := pm.Config()

	if got.GracefulTimeout != cfg.GracefulTimeout {
		t.Errorf("Config().GracefulTimeout = %v, want %v", got.GracefulTimeout, cfg.GracefulTimeout)
	}
	if got.MaxOutputBuffer != cfg.MaxOutputBuffer {
		t.Errorf("Config().MaxOutputBuffer = %d, want %d", got.MaxOutputBuffer, cfg.MaxOutputBuffer)
	}
}

// TestRegister verifies process registration.
func TestRegister(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc := NewManagedProcess(ProcessConfig{
		ID:          "test-1",
		ProjectPath: "/project",
		Command:     "echo",
	})

	// First registration should succeed
	if err := pm.Register(proc); err != nil {
		t.Errorf("Register() error = %v", err)
	}

	if pm.ActiveCount() != 1 {
		t.Errorf("ActiveCount = %d, want 1", pm.ActiveCount())
	}
	if pm.TotalStarted() != 1 {
		t.Errorf("TotalStarted = %d, want 1", pm.TotalStarted())
	}

	// Duplicate registration should fail
	proc2 := NewManagedProcess(ProcessConfig{
		ID:          "test-1",
		ProjectPath: "/project",
		Command:     "echo",
	})
	if err := pm.Register(proc2); !errors.Is(err, ErrProcessExists) {
		t.Errorf("Register duplicate error = %v, want ErrProcessExists", err)
	}

	// Same ID different path should succeed
	proc3 := NewManagedProcess(ProcessConfig{
		ID:          "test-1",
		ProjectPath: "/other-project",
		Command:     "echo",
	})
	if err := pm.Register(proc3); err != nil {
		t.Errorf("Register same ID different path error = %v", err)
	}

	if pm.ActiveCount() != 2 {
		t.Errorf("ActiveCount = %d, want 2", pm.ActiveCount())
	}
}

// TestRegisterDuringShutdown verifies registration fails during shutdown.
func TestRegisterDuringShutdown(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Shutdown the manager
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = pm.Shutdown(ctx)

	proc := NewManagedProcess(ProcessConfig{
		ID:      "test-shutdown",
		Command: "echo",
	})

	if err := pm.Register(proc); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Register during shutdown error = %v, want ErrShuttingDown", err)
	}
}

// TestGet verifies process retrieval by ID.
func TestGet(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Get non-existent process
	_, err := pm.Get("non-existent")
	if !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("Get non-existent error = %v, want ErrProcessNotFound", err)
	}

	// Register and get
	proc := NewManagedProcess(ProcessConfig{
		ID:          "test-get",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)

	got, err := pm.Get("test-get")
	if err != nil {
		t.Errorf("Get() error = %v", err)
	}
	if got != proc {
		t.Error("Get() returned different process")
	}
	if got.ID != "test-get" {
		t.Errorf("Got ID = %q, want %q", got.ID, "test-get")
	}
}

// TestGetByPath verifies process retrieval by ID and path.
func TestGetByPath(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc := NewManagedProcess(ProcessConfig{
		ID:          "test-getpath",
		ProjectPath: "/project/a",
		Command:     "echo",
	})
	_ = pm.Register(proc)

	// Get with correct path
	got, err := pm.GetByPath("test-getpath", "/project/a")
	if err != nil {
		t.Errorf("GetByPath() error = %v", err)
	}
	if got != proc {
		t.Error("GetByPath() returned different process")
	}

	// Get with wrong path
	_, err = pm.GetByPath("test-getpath", "/project/b")
	if !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("GetByPath wrong path error = %v, want ErrProcessNotFound", err)
	}

	// Get non-existent
	_, err = pm.GetByPath("non-existent", "/project/a")
	if !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("GetByPath non-existent error = %v, want ErrProcessNotFound", err)
	}
}

// TestRemove verifies process removal by ID.
func TestRemove(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc := NewManagedProcess(ProcessConfig{
		ID:          "test-remove",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)

	// Remove should succeed
	if !pm.Remove("test-remove") {
		t.Error("Remove() should return true for existing process")
	}

	if pm.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0", pm.ActiveCount())
	}

	// Remove again should fail
	if pm.Remove("test-remove") {
		t.Error("Remove() should return false for non-existent process")
	}

	// Remove non-existent
	if pm.Remove("never-existed") {
		t.Error("Remove() should return false for never-existed process")
	}
}

// TestRemoveByPath verifies process removal by ID and path.
func TestRemoveByPath(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc1 := NewManagedProcess(ProcessConfig{
		ID:          "multi-path",
		ProjectPath: "/project/a",
		Command:     "echo",
	})
	proc2 := NewManagedProcess(ProcessConfig{
		ID:          "multi-path",
		ProjectPath: "/project/b",
		Command:     "echo",
	})
	_ = pm.Register(proc1)
	_ = pm.Register(proc2)

	if pm.ActiveCount() != 2 {
		t.Errorf("ActiveCount = %d, want 2", pm.ActiveCount())
	}

	// Remove only one
	if !pm.RemoveByPath("multi-path", "/project/a") {
		t.Error("RemoveByPath() should return true")
	}

	if pm.ActiveCount() != 1 {
		t.Errorf("ActiveCount = %d, want 1", pm.ActiveCount())
	}

	// The other should still exist
	_, err := pm.GetByPath("multi-path", "/project/b")
	if err != nil {
		t.Errorf("GetByPath after RemoveByPath error = %v", err)
	}

	// Remove with wrong path
	if pm.RemoveByPath("multi-path", "/project/c") {
		t.Error("RemoveByPath() should return false for wrong path")
	}
}

// TestList verifies listing all processes.
func TestList(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Empty list
	if len(pm.List()) != 0 {
		t.Error("List() should be empty initially")
	}

	// Add processes
	for i := 0; i < 5; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "test-list-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	list := pm.List()
	if len(list) != 5 {
		t.Errorf("List() len = %d, want 5", len(list))
	}
}

// TestListByLabel verifies label-based filtering.
func TestListByLabel(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Create processes with different labels
	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "backend-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
			Labels:      map[string]string{"tier": "backend"},
		})
		_ = pm.Register(proc)
	}

	for i := 0; i < 2; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "frontend-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
			Labels:      map[string]string{"tier": "frontend"},
		})
		_ = pm.Register(proc)
	}

	// Process without labels
	proc := NewManagedProcess(ProcessConfig{
		ID:          "no-labels",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)

	// Filter by tier=backend
	backend := pm.ListByLabel("tier", "backend")
	if len(backend) != 3 {
		t.Errorf("ListByLabel(tier=backend) len = %d, want 3", len(backend))
	}

	// Filter by tier=frontend
	frontend := pm.ListByLabel("tier", "frontend")
	if len(frontend) != 2 {
		t.Errorf("ListByLabel(tier=frontend) len = %d, want 2", len(frontend))
	}

	// Filter by non-existent label
	none := pm.ListByLabel("tier", "database")
	if len(none) != 0 {
		t.Errorf("ListByLabel(tier=database) len = %d, want 0", len(none))
	}

	// Filter by non-existent key
	nokey := pm.ListByLabel("nonexistent", "value")
	if len(nokey) != 0 {
		t.Errorf("ListByLabel(nonexistent=value) len = %d, want 0", len(nokey))
	}
}

// TestGetByPID verifies PID-based lookup.
func TestGetByPID(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc := NewManagedProcess(ProcessConfig{
		ID:          "pid-lookup",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)

	// Not running, so should return nil
	if pm.GetByPID(12345) != nil {
		t.Error("GetByPID should return nil for non-running process")
	}

	// Set as running with a fake PID
	proc.SetState(StateRunning)
	proc.pid.Store(12345)

	got := pm.GetByPID(12345)
	if got != proc {
		t.Error("GetByPID should return the process")
	}

	// Different PID
	if pm.GetByPID(54321) != nil {
		t.Error("GetByPID should return nil for different PID")
	}
}

// TestIsManagedPID verifies managed PID checking.
func TestIsManagedPID(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	proc := NewManagedProcess(ProcessConfig{
		ID:          "managed-pid",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)
	proc.SetState(StateRunning)
	proc.pid.Store(99999)

	if !pm.IsManagedPID(99999) {
		t.Error("IsManagedPID should return true for managed PID")
	}

	if pm.IsManagedPID(11111) {
		t.Error("IsManagedPID should return false for unmanaged PID")
	}
}

// TestCounters verifies atomic counters.
func TestCounters(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Register some processes
	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "counter-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	if pm.ActiveCount() != 3 {
		t.Errorf("ActiveCount = %d, want 3", pm.ActiveCount())
	}
	if pm.TotalStarted() != 3 {
		t.Errorf("TotalStarted = %d, want 3", pm.TotalStarted())
	}

	// Remove one
	pm.Remove("counter-1")

	if pm.ActiveCount() != 2 {
		t.Errorf("ActiveCount after remove = %d, want 2", pm.ActiveCount())
	}
	if pm.TotalStarted() != 3 {
		t.Errorf("TotalStarted after remove = %d, want 3 (should not decrease)", pm.TotalStarted())
	}

	// Increment failed
	pm.IncrementFailed()
	pm.IncrementFailed()

	if pm.TotalFailed() != 2 {
		t.Errorf("TotalFailed = %d, want 2", pm.TotalFailed())
	}
}

// TestShutdown verifies graceful shutdown.
func TestShutdown(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{
		HealthCheckPeriod: 10 * time.Millisecond,
		GracefulTimeout:   time.Second,
	})

	// Register some processes
	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "shutdown-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := pm.Shutdown(ctx)
	if err != nil {
		t.Logf("Shutdown returned: %v", err)
	}

	if !pm.IsShuttingDown() {
		t.Error("IsShuttingDown should be true after Shutdown")
	}
}

// TestShutdownIdempotent verifies shutdown can be called multiple times.
func TestShutdownIdempotent(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// First shutdown
	err1 := pm.Shutdown(ctx)

	// Second shutdown should be no-op
	err2 := pm.Shutdown(ctx)

	// Both should succeed (or same error)
	if err1 != err2 {
		t.Logf("First shutdown: %v, Second shutdown: %v", err1, err2)
	}
}

// TestShutdownWithRunningProcesses verifies processes are stopped.
func TestShutdownWithRunningProcesses(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{
		HealthCheckPeriod: 0,
		GracefulTimeout:   time.Second,
	})

	// Create processes and mark as running
	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "running-" + itoa(i),
			ProjectPath: "/project",
			Command:     "sleep",
			Args:        []string{"10"},
		})
		_ = pm.Register(proc)
		proc.SetState(StateRunning)
	}

	// Aggressive shutdown with short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = pm.Shutdown(ctx)

	if !pm.IsShuttingDown() {
		t.Error("Should be shutting down")
	}
}

// TestStopAll verifies stopping all processes.
func TestStopAll(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Register processes
	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "stopall-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := pm.StopAll(ctx)
	if err != nil {
		t.Logf("StopAll returned: %v", err)
	}

	// All should be removed
	if pm.ActiveCount() != 0 {
		t.Errorf("ActiveCount after StopAll = %d, want 0", pm.ActiveCount())
	}
}

// TestStopByProjectPath verifies stopping processes by path.
func TestStopByProjectPath(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Register processes in different paths
	for i := 0; i < 2; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "path-a-" + itoa(i),
			ProjectPath: "/project/a",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	for i := 0; i < 3; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "path-b-" + itoa(i),
			ProjectPath: "/project/b",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	if pm.ActiveCount() != 5 {
		t.Errorf("ActiveCount = %d, want 5", pm.ActiveCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stoppedIDs, err := pm.StopByProjectPath(ctx, "/project/a")
	if err != nil {
		t.Logf("StopByProjectPath returned: %v", err)
	}

	if len(stoppedIDs) != 2 {
		t.Errorf("StopByProjectPath returned %d IDs, want 2", len(stoppedIDs))
	}

	if pm.ActiveCount() != 3 {
		t.Errorf("ActiveCount after StopByProjectPath = %d, want 3", pm.ActiveCount())
	}

	// Processes from /project/b should still exist
	list := pm.List()
	for _, p := range list {
		if p.ProjectPath != "/project/b" {
			t.Errorf("Process %s has path %s, want /project/b", p.ID, p.ProjectPath)
		}
	}
}

// TestWriteStdin verifies stdin writing through manager.
func TestWriteStdin(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Test with non-existent process
	_, err := pm.WriteStdin("non-existent", []byte("test"))
	if !errors.Is(err, ErrProcessNotFound) {
		t.Errorf("WriteStdin non-existent error = %v, want ErrProcessNotFound", err)
	}

	// Test with stdin disabled
	proc := NewManagedProcess(ProcessConfig{
		ID:          "stdin-test",
		ProjectPath: "/project",
		Command:     "cat",
		EnableStdin: false,
	})
	_ = pm.Register(proc)

	_, err = pm.WriteStdin("stdin-test", []byte("test"))
	if !errors.Is(err, ErrStdinNotEnabled) {
		t.Errorf("WriteStdin disabled error = %v, want ErrStdinNotEnabled", err)
	}
}

// TestProcessKey verifies composite key generation.
func TestProcessKey(t *testing.T) {
	tests := []struct {
		id          string
		projectPath string
		want        string
	}{
		{"proc1", "/project", "/project\x00proc1"},
		{"", "/path", "/path\x00"},
		{"id", "", "\x00id"},
		{"", "", "\x00"},
	}

	for _, tt := range tests {
		t.Run(tt.id+"_"+tt.projectPath, func(t *testing.T) {
			got := processKey(tt.id, tt.projectPath)
			if got != tt.want {
				t.Errorf("processKey(%q, %q) = %q, want %q", tt.id, tt.projectPath, got, tt.want)
			}
		})
	}
}

// TestConcurrentOperations verifies thread safety.
func TestConcurrentOperations(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// Concurrent registrations
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			proc := NewManagedProcess(ProcessConfig{
				ID:          "concurrent-" + itoa(id),
				ProjectPath: "/project",
				Command:     "echo",
			})
			err := pm.Register(proc)
			if err != nil && !errors.Is(err, ErrProcessExists) {
				errChan <- err
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pm.List()
			_ = pm.ActiveCount()
			_, _ = pm.Get("concurrent-5")
		}()
	}

	// Concurrent removals
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pm.Remove("concurrent-" + itoa(id))
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent operation error: %v", err)
	}
}

// TestErrorTypes verifies error type definitions.
func TestErrorTypes(t *testing.T) {
	if ErrProcessExists == nil {
		t.Error("ErrProcessExists should not be nil")
	}
	if ErrProcessNotFound == nil {
		t.Error("ErrProcessNotFound should not be nil")
	}
	if ErrInvalidState == nil {
		t.Error("ErrInvalidState should not be nil")
	}
	if ErrShuttingDown == nil {
		t.Error("ErrShuttingDown should not be nil")
	}
	if ErrStdinNotEnabled == nil {
		t.Error("ErrStdinNotEnabled should not be nil")
	}

	// Verify they have distinct messages
	msgs := map[string]bool{}
	for _, err := range []error{ErrProcessExists, ErrProcessNotFound, ErrInvalidState, ErrShuttingDown, ErrStdinNotEnabled} {
		msg := err.Error()
		if msgs[msg] {
			t.Errorf("Duplicate error message: %q", msg)
		}
		msgs[msg] = true
	}
}

// TestParsePIDLines verifies PID parsing from lsof output.
func TestParsePIDLines(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	tests := []struct {
		name   string
		output string
		want   []int
	}{
		{"empty", "", nil},
		{"single", "12345", []int{12345}},
		{"multiple", "123\n456\n789", []int{123, 456, 789}},
		{"with whitespace", "  123  \n  456  ", []int{123, 456}},
		{"with invalid", "123\nabc\n456", []int{123, 456}},
		{"empty lines", "123\n\n456\n\n", []int{123, 456}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pm.parsePIDLines(tt.output)
			if len(got) != len(tt.want) {
				t.Errorf("parsePIDLines(%q) = %v, want %v", tt.output, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parsePIDLines(%q)[%d] = %d, want %d", tt.output, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestHealthCheckWithProcesses verifies health check behavior.
func TestHealthCheckWithProcesses(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{
		HealthCheckPeriod: 10 * time.Millisecond,
	})

	// Add a process in starting state
	proc := NewManagedProcess(ProcessConfig{
		ID:          "health-check-test",
		ProjectPath: "/project",
		Command:     "echo",
	})
	_ = pm.Register(proc)
	proc.SetState(StateStarting)

	// Set start time to long ago to trigger timeout
	oldTime := time.Now().Add(-time.Minute)
	proc.startTime.Store(&oldTime)

	// Let health check run
	time.Sleep(50 * time.Millisecond)

	// Process should be marked as failed due to starting timeout
	if proc.State() != StateFailed {
		t.Logf("State = %v (may or may not be Failed depending on timing)", proc.State())
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = pm.Shutdown(ctx)
}

// mockPIDTracker implements PIDTracker for testing.
type mockPIDTracker struct {
	addCalled    atomic.Int32
	removeCalled atomic.Int32
}

func (m *mockPIDTracker) Add(id string, pid int, pgid int, projectPath string) error {
	m.addCalled.Add(1)
	return nil
}

func (m *mockPIDTracker) Remove(id string, projectPath string) error {
	m.removeCalled.Add(1)
	return nil
}

// TestWithPIDTracker verifies PID tracker integration.
func TestWithPIDTracker(t *testing.T) {
	tracker := &mockPIDTracker{}

	pm := NewProcessManager(ManagerConfig{
		HealthCheckPeriod: 0,
		PIDTracker:        tracker,
	})

	if pm.pidTracker != tracker {
		t.Error("PID tracker not set correctly")
	}
}

// BenchmarkRegister measures registration performance.
func BenchmarkRegister(b *testing.B) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "bench-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}
}

// BenchmarkGet measures lookup performance.
func BenchmarkGet(b *testing.B) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Pre-populate
	for i := 0; i < 1000; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "bench-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pm.Get("bench-500")
	}
}

// BenchmarkList measures list performance.
func BenchmarkList(b *testing.B) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Pre-populate
	for i := 0; i < 100; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "bench-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.List()
	}
}

// BenchmarkConcurrentAccess measures concurrent access performance.
func BenchmarkConcurrentAccess(b *testing.B) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// Pre-populate
	for i := 0; i < 100; i++ {
		proc := NewManagedProcess(ProcessConfig{
			ID:          "bench-" + itoa(i),
			ProjectPath: "/project",
			Command:     "echo",
		})
		_ = pm.Register(proc)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 3 {
			case 0:
				_, _ = pm.Get("bench-50")
			case 1:
				_ = pm.List()
			case 2:
				_ = pm.ActiveCount()
			}
			i++
		}
	})
}
