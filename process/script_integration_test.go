package process

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/script"
)

// TestSetScriptRegistry verifies the setter and getter.
func TestSetScriptRegistry(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	if pm.ScriptRegistry() != nil {
		t.Error("ScriptRegistry should be nil by default")
	}

	reg := script.NewRegistry()
	pm.SetScriptRegistry(reg)

	if pm.ScriptRegistry() != reg {
		t.Error("ScriptRegistry should return the set registry")
	}

	pm.SetScriptRegistry(nil)
	if pm.ScriptRegistry() != nil {
		t.Error("ScriptRegistry should be nil after setting nil")
	}
}

// TestNilRegistryNoPanic verifies existing behavior when registry is nil.
func TestNilRegistryNoPanic(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          "nil-reg-test",
		ProjectPath: "/tmp",
		Command:     "echo",
		Args:        []string{"hello"},
		BufferSize:  1024,
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	if proc.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", proc.ExitCode())
	}
}

// TestScriptEntryUpdatedOnStart verifies ScriptEntry transitions to Running on start.
func TestScriptEntryUpdatedOnStart(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})
	reg := script.NewRegistry()
	pm.SetScriptRegistry(reg)

	processID := "start-test"
	entry, err := reg.Register("test-script", processID, &script.Config{Run: "echo hello"})
	if err != nil {
		t.Fatalf("Register script error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          entry.ProcessID,
		ProjectPath: "/tmp",
		Command:     "echo",
		Args:        []string{"hello"},
		BufferSize:  1024,
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	// Entry should have been set to Running
	if entry.State() != script.StateRunning {
		t.Errorf("entry state = %v, want Running", entry.State())
	}

	if entry.StartCount() != 1 {
		t.Errorf("start count = %d, want 1", entry.StartCount())
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}
}

// TestScriptEntryUpdatedOnNormalExit verifies ScriptEntry transitions to Stopped on exit code 0.
func TestScriptEntryUpdatedOnNormalExit(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})
	reg := script.NewRegistry()
	pm.SetScriptRegistry(reg)

	entry, err := reg.Register("exit-ok", "/tmp", &script.Config{Run: "true"})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          entry.ProcessID,
		ProjectPath: "/tmp",
		Command:     "true",
		BufferSize:  1024,
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	if entry.State() != script.StateStopped {
		t.Errorf("entry state = %v, want Stopped", entry.State())
	}
}

// TestScriptEntryUpdatedOnFailedExit verifies ScriptEntry transitions to Failed on non-zero exit.
func TestScriptEntryUpdatedOnFailedExit(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})
	reg := script.NewRegistry()
	pm.SetScriptRegistry(reg)

	entry, err := reg.Register("exit-fail", "/tmp", &script.Config{Run: "false"})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          entry.ProcessID,
		ProjectPath: "/tmp",
		Command:     "false",
		BufferSize:  1024,
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	if entry.State() != script.StateFailed {
		t.Errorf("entry state = %v, want Failed", entry.State())
	}
	if entry.FailCount() != 1 {
		t.Errorf("fail count = %d, want 1", entry.FailCount())
	}
	if entry.LastError() == "" {
		t.Error("last error should be set on failure")
	}
}

// TestOutputCallbackFeedsScriptEntry verifies output lines are streamed to ScriptEntry.
func TestOutputCallbackFeedsScriptEntry(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})
	reg := script.NewRegistry()
	pm.SetScriptRegistry(reg)

	entry, err := reg.Register("output-test", "/tmp", &script.Config{Run: "echo"})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          entry.ProcessID,
		ProjectPath: "/tmp",
		Command:     "sh",
		Args:        []string{"-c", "echo line1; echo line2; echo line3"},
		BufferSize:  1024,
		OutputCallback: func(processID string, line string) {
			entry.AppendOutput(line)
		},
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	lines := entry.OutputLines()
	if len(lines) < 3 {
		t.Errorf("output lines = %d, want >= 3; got: %v", len(lines), lines)
	}

	expected := []string{"line1", "line2", "line3"}
	for i, want := range expected {
		if i >= len(lines) {
			break
		}
		if lines[i] != want {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want)
		}
	}
}

// TestOutputCallbackConcurrency verifies output callback is safe under concurrent output.
func TestOutputCallbackConcurrency(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	var mu sync.Mutex
	var collected []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          "concurrent-output",
		ProjectPath: "/tmp",
		Command:     "sh",
		Args:        []string{"-c", "for i in $(seq 1 50); do echo line$i; done"},
		BufferSize:  4096,
		OutputCallback: func(processID string, line string) {
			mu.Lock()
			collected = append(collected, line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	mu.Lock()
	count := len(collected)
	mu.Unlock()

	if count != 50 {
		t.Errorf("collected %d lines, want 50", count)
	}
}

// TestNoCallbackNoLineWriters verifies lineWriters are nil when no callback is set.
func TestNoCallbackNoLineWriters(t *testing.T) {
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := pm.StartCommand(ctx, ProcessConfig{
		ID:          "no-callback",
		ProjectPath: "/tmp",
		Command:     "echo",
		Args:        []string{"hello"},
		BufferSize:  1024,
	})
	if err != nil {
		t.Fatalf("StartCommand error: %v", err)
	}

	if proc.stdoutLineWriter != nil {
		t.Error("stdoutLineWriter should be nil when no callback set")
	}
	if proc.stderrLineWriter != nil {
		t.Error("stderrLineWriter should be nil when no callback set")
	}

	select {
	case <-proc.Done():
	case <-ctx.Done():
		t.Fatal("process did not exit in time")
	}

	// Output should still be captured in ring buffer
	stdout, _ := proc.Stdout()
	if len(stdout) == 0 {
		t.Error("stdout should have output even without callback")
	}
}
