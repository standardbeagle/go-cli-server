package process

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestStartShutdownRace pins the Start/Shutdown ordering that guards the
// WaitGroup. Before startMu ordered Start's wg.Add(1) against Shutdown's
// shuttingDown.Store(true), a Start that had passed the shuttingDown guard but
// had not yet reached wg.Add(1) — it was still spawning the OS process — could
// have its waitForProcess goroutine leak past a Shutdown whose wg.Wait()
// observed a zero counter and returned early. The race was load-dependent
// because it needed a scheduling stall inside that window; the startGuardHook
// seam makes it deterministic by freezing a Start exactly there while Shutdown
// runs to completion concurrently.
//
// Invariant under test: once Shutdown returns, every process that Start reported
// as started (returned nil) must already be reaped — its done channel closed —
// because Shutdown's wg.Wait() is supposed to account for every waitForProcess
// goroutine. A started process whose done is still open means its waiter
// outlived Shutdown: the leak.
//
// Run with -race -count=20 to also exercise the memory ordering repeatedly.
func TestStartShutdownRace(t *testing.T) {
	workDir := t.TempDir() // must exist: it becomes the child's cwd
	pm := NewProcessManager(ManagerConfig{HealthCheckPeriod: 0})

	// One Start is frozen in the post-guard window; the rest race freely.
	frozen := make(chan struct{}) // closed when the frozen Start reaches the hook
	release := make(chan struct{})
	var hookOnce sync.Once
	pm.startGuardHook = func() {
		hookOnce.Do(func() {
			close(frozen)
			<-release
		})
	}

	var mu sync.Mutex
	started := make([]*ManagedProcess, 0, 8)
	recordStart := func(proc *ManagedProcess, err error) {
		if err == nil {
			mu.Lock()
			started = append(started, proc)
			mu.Unlock()
		}
	}

	var startWg sync.WaitGroup

	// The frozen starter: passes the guard, takes its wg ticket, then parks in the
	// hook until released — the exact window the old code leaked from.
	startWg.Add(1)
	go func() {
		defer startWg.Done()
		proc := NewManagedProcess(ProcessConfig{
			ID: "race-frozen", ProjectPath: workDir, WorkingDir: workDir,
			Command: "sleep", Args: []string{"0.05"},
		})
		recordStart(proc, pm.Start(context.Background(), proc))
	}()

	// Wait until the frozen starter is parked in the window before shutting down.
	<-frozen

	// Fire Shutdown while a Start is parked mid-window. With the fix, Shutdown's
	// wg.Wait() must account for the frozen starter's ticket and block until its
	// waiter finishes; without it, Shutdown could return with the waiter leaked.
	var shutdownWg sync.WaitGroup
	shutdownWg.Add(1)
	go func() {
		defer shutdownWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = pm.Shutdown(ctx)
	}()

	// Give Shutdown a beat to reach (and, pre-fix, wrongly pass) wg.Wait() before
	// releasing the frozen Start into its spawn + goroutine launch.
	time.Sleep(50 * time.Millisecond)
	close(release)

	startWg.Wait()
	shutdownWg.Wait()

	// Shutdown has returned. Every started process must already be reaped; a
	// still-open done channel is a waitForProcess goroutine that outlived
	// Shutdown's wg.Wait() — the leak this fix closes.
	mu.Lock()
	procs := started
	mu.Unlock()
	for _, proc := range procs {
		select {
		case <-proc.done:
		default:
			t.Fatalf("process %s started but its waiter outlived Shutdown "+
				"(done still open) — leaked waitForProcess goroutine", proc.ID)
		}
	}
}
