package client

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// A reconnect backoff must not outlive Close. The loop used to sleep out its
// full delay — up to ReconnectBackoffMax, 30s by default — so a closed
// connection kept a goroutine alive long past the process that closed it. Under
// a test binary that runs many connections, they accumulate and goleak fails.
func TestResilientConn_CloseAbandonsReconnectBackoff(t *testing.T) {
	// A socket path nothing listens on, and a hub binary that cannot be spawned:
	// every reconnect attempt fails fast, so the loop spends its life in backoff.
	sock := filepath.Join(t.TempDir(), "absent.sock")
	rc := NewResilientConn(ResilientConfig{
		AutoStartConfig:     AutoStartConfig{SocketPath: sock, HubPath: filepath.Join(t.TempDir(), "no-such-binary")},
		ReconnectBackoffMin: 30 * time.Second,
		ReconnectBackoffMax: 30 * time.Second,
		HeartbeatInterval:   0,
	})

	// Signalled once the loop has failed an attempt and is about to wait out its
	// backoff. Closing before that point would prove nothing: the loop would exit
	// at its next shutdown check without ever sleeping.
	inBackoff := make(chan time.Duration, 1)
	rc.onBackoff = func(d time.Duration) {
		select {
		case inBackoff <- d:
		default:
		}
	}

	rc.triggerReconnect(errors.New("induced disconnect"))

	select {
	case d := <-inBackoff:
		if d != 30*time.Second {
			t.Fatalf("backoff = %v, want 30s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reconnect loop never reached its backoff")
	}
	if !rc.IsReconnecting() {
		t.Fatal("loop should still be reconnecting while backing off")
	}

	start := time.Now()
	if err := rc.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// reconnectLoop clears the reconnecting flag as it returns, so this observes
	// the goroutine actually exiting rather than the flag being reset elsewhere.
	if !waitFor(t, 5*time.Second, func() bool { return !rc.IsReconnecting() }) {
		t.Fatal("reconnect loop still running 5s after Close — it slept out its backoff")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("reconnect loop took %v to notice Close, want prompt exit", elapsed)
	}
}

// Close is idempotent, and closing the signal channel twice would panic.
func TestResilientConn_CloseTwice(t *testing.T) {
	rc := NewResilientConn(ResilientConfig{AutoStartConfig: AutoStartConfig{SocketPath: filepath.Join(t.TempDir(), "absent.sock")}})
	if err := rc.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

// A zero-value ResilientConn must not panic on Close: the close signal is
// created lazily, and Close is the first thing to touch it.
func TestResilientConn_ZeroValueCloseDoesNotPanic(t *testing.T) {
	rc := &ResilientConn{}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close() on zero value error = %v", err)
	}
}

// A reconnect triggered after Close must not start a loop at all.
func TestResilientConn_NoReconnectAfterClose(t *testing.T) {
	rc := NewResilientConn(ResilientConfig{AutoStartConfig: AutoStartConfig{SocketPath: filepath.Join(t.TempDir(), "absent.sock")}})
	if err := rc.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	rc.triggerReconnect(errors.New("late disconnect"))
	if rc.IsReconnecting() {
		t.Error("triggerReconnect started a loop after Close")
	}
}

// The backoff is not the only place a reconnect loop parks. After spawning a
// hub it waits up to StartTimeout for the socket to appear, and that wait used
// to run on context.Background() — so Close left the goroutine pinned there for
// seconds, which is exactly what tripped goleak in the consumer's suite.
func TestResilientConn_CloseAbandonsHubStartupWait(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "absent.sock")

	// A hub binary that starts, never binds the socket, and outlives the wait.
	// waitForHub therefore polls until StartTimeout — the window Close must cut.
	hub := filepath.Join(dir, "sleeper.sh")
	if err := os.WriteFile(hub, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	rc := NewResilientConn(ResilientConfig{
		AutoStartConfig: AutoStartConfig{
			SocketPath:    sock,
			HubPath:       hub,
			HubArgs:       []string{},
			StartTimeout:  60 * time.Second,
			RetryInterval: 10 * time.Millisecond,
		},
		ReconnectBackoffMin: time.Millisecond,
		ReconnectBackoffMax: time.Millisecond,
	})
	t.Cleanup(func() { _ = rc.Close() })

	rc.triggerReconnect(errors.New("induced disconnect"))

	// Give the loop time to spawn the hub and enter its startup wait.
	if !waitFor(t, 5*time.Second, rc.IsReconnecting) {
		t.Fatal("reconnect loop never started")
	}
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	if err := rc.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return !rc.IsReconnecting() }) {
		t.Fatal("reconnect loop still running 5s after Close — it waited out StartTimeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("reconnect loop took %v to notice Close, want prompt exit", elapsed)
	}
}
