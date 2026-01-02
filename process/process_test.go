package process

import (
	"testing"
)

func TestProcessState(t *testing.T) {
	cfg := ProcessConfig{
		ID:          "test-process",
		ProjectPath: "/tmp",
		Command:     "echo",
		Args:        []string{"hello"},
	}

	proc := NewManagedProcess(cfg)

	// Test initial state
	if proc.State() != StatePending {
		t.Errorf("initial state = %v, want %v", proc.State(), StatePending)
	}

	// Test ID
	if proc.ID != "test-process" {
		t.Errorf("ID = %q, want %q", proc.ID, "test-process")
	}

	// Test ProjectPath
	if proc.ProjectPath != "/tmp" {
		t.Errorf("ProjectPath = %q, want %q", proc.ProjectPath, "/tmp")
	}

	// Test command
	if proc.Command != "echo" {
		t.Errorf("Command = %q, want %q", proc.Command, "echo")
	}
}

func TestProcessStateString(t *testing.T) {
	tests := []struct {
		state ProcessState
		want  string
	}{
		{StatePending, "pending"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateFailed, "failed"},
		{ProcessState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("State.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProcessCompareAndSwapState(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "cas-test",
		Command: "sleep",
		Args:    []string{"1"},
	}
	proc := NewManagedProcess(cfg)

	// Should succeed: Pending -> Starting
	if !proc.CompareAndSwapState(StatePending, StateStarting) {
		t.Error("CAS Pending->Starting should succeed")
	}
	if proc.State() != StateStarting {
		t.Errorf("State after CAS = %v, want %v", proc.State(), StateStarting)
	}

	// Should fail: trying to CAS from Pending when already Starting
	if proc.CompareAndSwapState(StatePending, StateRunning) {
		t.Error("CAS Pending->Running should fail when state is Starting")
	}

	// State should be unchanged
	if proc.State() != StateStarting {
		t.Errorf("State after failed CAS = %v, want %v", proc.State(), StateStarting)
	}
}

func TestProcessSetState(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "setstate-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	proc.SetState(StateRunning)
	if proc.State() != StateRunning {
		t.Errorf("State = %v, want %v", proc.State(), StateRunning)
	}

	proc.SetState(StateStopped)
	if proc.State() != StateStopped {
		t.Errorf("State = %v, want %v", proc.State(), StateStopped)
	}
}

func TestProcessIsDone(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "isdone-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// Not done when pending
	if proc.IsDone() {
		t.Error("IsDone should be false for Pending state")
	}

	proc.SetState(StateRunning)
	if proc.IsDone() {
		t.Error("IsDone should be false for Running state")
	}

	proc.SetState(StateStopped)
	if !proc.IsDone() {
		t.Error("IsDone should be true for Stopped state")
	}

	proc.SetState(StateFailed)
	if !proc.IsDone() {
		t.Error("IsDone should be true for Failed state")
	}
}

func TestProcessIsRunning(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "isrunning-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	if proc.IsRunning() {
		t.Error("IsRunning should be false for Pending state")
	}

	proc.SetState(StateRunning)
	if !proc.IsRunning() {
		t.Error("IsRunning should be true for Running state")
	}

	proc.SetState(StateStopped)
	if proc.IsRunning() {
		t.Error("IsRunning should be false for Stopped state")
	}
}

func TestProcessLabels(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "labels-test",
		Command: "true",
		Labels: map[string]string{
			"env":  "development",
			"tier": "backend",
		},
	}
	proc := NewManagedProcess(cfg)

	if proc.Labels == nil {
		t.Fatal("Labels should not be nil")
	}

	if proc.Labels["env"] != "development" {
		t.Errorf("Labels[env] = %q, want %q", proc.Labels["env"], "development")
	}

	if proc.Labels["tier"] != "backend" {
		t.Errorf("Labels[tier] = %q, want %q", proc.Labels["tier"], "backend")
	}
}

func TestProcessStdinNotEnabled(t *testing.T) {
	cfg := ProcessConfig{
		ID:          "stdin-disabled",
		Command:     "cat",
		EnableStdin: false,
	}
	proc := NewManagedProcess(cfg)

	n, err := proc.WriteStdin([]byte("test"))
	if err == nil {
		t.Error("WriteStdin should return error when stdin not enabled")
	}
	if n != 0 {
		t.Errorf("WriteStdin returned n=%d, want 0", n)
	}
}

func TestProcessOutputBuffers(t *testing.T) {
	cfg := ProcessConfig{
		ID:         "output-test",
		Command:    "echo",
		Args:       []string{"hello"},
		BufferSize: 1024,
	}
	proc := NewManagedProcess(cfg)

	// Write directly to stdout buffer for testing
	_, _ = proc.stdout.Write([]byte("stdout content\n"))
	_, _ = proc.stderr.Write([]byte("stderr content\n"))

	stdout, _ := proc.Stdout()
	if string(stdout) != "stdout content\n" {
		t.Errorf("Stdout = %q, want %q", stdout, "stdout content\n")
	}

	stderr, _ := proc.Stderr()
	if string(stderr) != "stderr content\n" {
		t.Errorf("Stderr = %q, want %q", stderr, "stderr content\n")
	}

	combined, _ := proc.CombinedOutput()
	if len(combined) == 0 {
		t.Error("CombinedOutput should not be empty")
	}
}

func TestProcessDoneChannel(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "done-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// Done channel should exist and not be nil
	if proc.Done() == nil {
		t.Error("Done() should not return nil")
	}

	// Channel should not be closed initially
	select {
	case <-proc.Done():
		t.Error("Done() channel should not be closed initially")
	default:
		// Expected
	}
}

func TestProcessRuntime(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "runtime-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// Runtime should be 0 before start
	if proc.Runtime() != 0 {
		t.Errorf("Runtime() = %v, want 0 before start", proc.Runtime())
	}
}

func TestProcessPID(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "pid-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// PID should be -1 before start
	if proc.PID() != -1 {
		t.Errorf("PID() = %d, want -1 before start", proc.PID())
	}
}

func TestProcessExitCode(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "exitcode-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// Exit code should be -1 before completion
	if proc.ExitCode() != -1 {
		t.Errorf("ExitCode() = %d, want -1 before completion", proc.ExitCode())
	}
}

func TestProcessStdinEnabled(t *testing.T) {
	cfg := ProcessConfig{
		ID:          "stdin-enabled-test",
		Command:     "cat",
		EnableStdin: true,
	}
	proc := NewManagedProcess(cfg)

	if !proc.StdinEnabled() {
		t.Error("StdinEnabled() should return true when EnableStdin is true")
	}

	cfg.EnableStdin = false
	proc2 := NewManagedProcess(cfg)
	if proc2.StdinEnabled() {
		t.Error("StdinEnabled() should return false when EnableStdin is false")
	}
}

func TestProcessStartTime(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "starttime-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// StartTime should be nil before start
	if proc.StartTime() != nil {
		t.Error("StartTime() should be nil before start")
	}
}

func TestProcessEndTime(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "endtime-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// EndTime should be nil before completion
	if proc.EndTime() != nil {
		t.Error("EndTime() should be nil before completion")
	}
}

func TestProcessSummary(t *testing.T) {
	cfg := ProcessConfig{
		ID:      "summary-test",
		Command: "true",
	}
	proc := NewManagedProcess(cfg)

	// Summary should return "pending" initially
	if proc.Summary() != "pending" {
		t.Errorf("Summary() = %q, want %q", proc.Summary(), "pending")
	}

	proc.SetState(StateRunning)
	if proc.Summary() != "running" {
		t.Errorf("Summary() = %q, want %q", proc.Summary(), "running")
	}
}
