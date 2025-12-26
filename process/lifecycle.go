package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Start begins execution of a process.
func (pm *ProcessManager) Start(ctx context.Context, proc *ManagedProcess) error {
	if pm.shuttingDown.Load() {
		return ErrShuttingDown
	}

	// Atomic state transition: Pending -> Starting
	if !proc.CompareAndSwapState(StatePending, StateStarting) {
		return fmt.Errorf("%w: cannot start process %s (state: %s)",
			ErrInvalidState, proc.ID, proc.State())
	}

	// Register the process
	if err := pm.Register(proc); err != nil {
		proc.SetState(StatePending)
		return err
	}

	// Build the command
	proc.cmd = exec.CommandContext(proc.ctx, proc.Command, proc.Args...)
	proc.cmd.Dir = proc.ProjectPath

	if len(proc.Env) > 0 {
		proc.cmd.Env = proc.Env
	} else {
		proc.cmd.Env = os.Environ()
	}

	// Set platform-specific process attributes
	setProcAttr(proc.cmd)

	// Connect output streams to ring buffers
	proc.cmd.Stdout = proc.stdout
	proc.cmd.Stderr = proc.stderr

	// Setup stdin pipe if enabled
	if proc.stdinEnabled {
		stdinPipe, err := proc.cmd.StdinPipe()
		if err != nil {
			proc.SetState(StateFailed)
			pm.IncrementFailed()
			return fmt.Errorf("failed to create stdin pipe for %s: %w", proc.ID, err)
		}
		proc.stdin = stdinPipe
	}

	// Start the process
	if err := proc.cmd.Start(); err != nil {
		proc.SetState(StateFailed)
		pm.IncrementFailed()
		return fmt.Errorf("failed to start process %s: %w", proc.ID, err)
	}

	// Setup platform-specific process group management
	if err := SetupJobObject(proc.cmd); err != nil {
		// Non-fatal
	}

	// Record start time and PID
	now := time.Now()
	proc.startTime.Store(&now)
	pid := proc.cmd.Process.Pid
	proc.pid.Store(int32(pid))
	proc.SetState(StateRunning)

	// Track PID for orphan cleanup
	if pm.pidTracker != nil {
		pgid := getProcessGroupID(pid)
		_ = pm.pidTracker.Add(proc.ID, pid, pgid, proc.ProjectPath)
	}

	// Start goroutine to wait for completion
	pm.wg.Add(1)
	go pm.waitForProcess(proc)

	return nil
}

// waitForProcess monitors the process until it exits.
func (pm *ProcessManager) waitForProcess(proc *ManagedProcess) {
	defer pm.wg.Done()

	err := proc.cmd.Wait()

	// Cleanup platform-specific resources
	if proc.cmd != nil && proc.cmd.Process != nil {
		CleanupJobObject(proc.cmd.Process.Pid)
	}

	// Close stdin if open
	if proc.stdin != nil {
		proc.stdin.Close()
	}

	now := time.Now()
	proc.endTime.Store(&now)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			proc.exitCode.Store(int32(exitErr.ExitCode()))
		} else {
			proc.exitCode.Store(-1)
		}
		proc.SetState(StateFailed)
		pm.IncrementFailed()
	} else {
		proc.exitCode.Store(0)
		proc.SetState(StateStopped)
	}

	if pm.pidTracker != nil {
		_ = pm.pidTracker.Remove(proc.ID, proc.ProjectPath)
	}

	close(proc.done)
}

// Stop terminates a process gracefully.
func (pm *ProcessManager) Stop(ctx context.Context, id string) error {
	proc, err := pm.Get(id)
	if err != nil {
		return err
	}

	return pm.StopProcess(ctx, proc)
}

// StopProcess terminates the given process.
func (pm *ProcessManager) StopProcess(ctx context.Context, proc *ManagedProcess) error {
	state := proc.State()
	if state == StateStopped || state == StateFailed {
		return nil
	}

	if !proc.CompareAndSwapState(StateRunning, StateStopping) {
		if proc.State() == StateStopping {
			select {
			case <-proc.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return fmt.Errorf("%w: cannot stop process %s (state: %s)",
			ErrInvalidState, proc.ID, proc.State())
	}

	proc.Cancel()

	select {
	case <-ctx.Done():
		return pm.forceKill(proc)
	default:
	}

	if proc.cmd != nil && proc.cmd.Process != nil {
		_ = pm.signalProcessGroup(proc.cmd.Process.Pid, syscall.SIGTERM)
	}

	gracefulTimeout := pm.config.GracefulTimeout
	if gracefulTimeout == 0 {
		gracefulTimeout = 5 * time.Second
	}

	select {
	case <-proc.done:
		return nil
	case <-time.After(gracefulTimeout):
		return pm.forceKill(proc)
	case <-ctx.Done():
		return pm.forceKill(proc)
	}
}

// forceKill forcefully terminates the process.
func (pm *ProcessManager) forceKill(proc *ManagedProcess) error {
	if proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}

	if err := pm.signalProcessGroup(proc.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to force kill process %s: %w", proc.ID, err)
	}

	select {
	case <-proc.done:
		return nil
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// Restart stops a process and starts a new one with the same configuration.
func (pm *ProcessManager) Restart(ctx context.Context, id string) (*ManagedProcess, error) {
	proc, err := pm.Get(id)
	if err != nil {
		return nil, err
	}

	if err := pm.StopProcess(ctx, proc); err != nil {
		return nil, fmt.Errorf("failed to stop process for restart: %w", err)
	}

	pm.Remove(id)

	newProc := NewManagedProcess(ProcessConfig{
		ID:          id,
		ProjectPath: proc.ProjectPath,
		Command:     proc.Command,
		Args:        proc.Args,
		Labels:      proc.Labels,
		BufferSize:  proc.stdout.Cap(),
		EnableStdin: proc.stdinEnabled,
	})

	if err := pm.Start(ctx, newProc); err != nil {
		return nil, fmt.Errorf("failed to start process after restart: %w", err)
	}

	return newProc, nil
}

// StartCommand is a convenience method to create and start a process.
func (pm *ProcessManager) StartCommand(ctx context.Context, cfg ProcessConfig) (*ManagedProcess, error) {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = pm.config.MaxOutputBuffer
	}

	if cfg.Timeout == 0 && pm.config.DefaultTimeout > 0 {
		cfg.Timeout = pm.config.DefaultTimeout
	}

	proc := NewManagedProcess(cfg)
	if err := pm.Start(ctx, proc); err != nil {
		return nil, err
	}

	return proc, nil
}

// StartOrReuseResult contains the result of StartOrReuse operation.
type StartOrReuseResult struct {
	Process      *ManagedProcess
	Reused       bool
	Cleaned      bool
	PortRetried  bool
	PortsCleared []int
	PortError    string
}

// StartOrReuse implements idempotent process start.
func (pm *ProcessManager) StartOrReuse(ctx context.Context, cfg ProcessConfig) (*StartOrReuseResult, error) {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = pm.config.MaxOutputBuffer
	}

	if cfg.Timeout == 0 && pm.config.DefaultTimeout > 0 {
		cfg.Timeout = pm.config.DefaultTimeout
	}

	result := &StartOrReuseResult{}

	existing, err := pm.GetByPath(cfg.ID, cfg.ProjectPath)
	if err == nil {
		state := existing.State()
		switch state {
		case StateRunning, StateStarting:
			result.Process = existing
			result.Reused = true
			return result, nil
		case StateStopped, StateFailed:
			pm.RemoveByPath(cfg.ID, cfg.ProjectPath)
			result.Cleaned = true
		case StateStopping:
			select {
			case <-existing.Done():
				pm.RemoveByPath(cfg.ID, cfg.ProjectPath)
				result.Cleaned = true
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			pm.RemoveByPath(cfg.ID, cfg.ProjectPath)
		}
	}

	proc := NewManagedProcess(cfg)
	if err := pm.Start(ctx, proc); err != nil {
		return nil, err
	}

	result.Process = proc
	return result, nil
}

// RunSync starts a process and waits for it to complete.
func (pm *ProcessManager) RunSync(ctx context.Context, cfg ProcessConfig) (int, error) {
	proc, err := pm.StartCommand(ctx, cfg)
	if err != nil {
		return -1, err
	}

	select {
	case <-proc.done:
		return proc.ExitCode(), nil
	case <-ctx.Done():
		pm.StopProcess(ctx, proc)
		return -1, ctx.Err()
	}
}
