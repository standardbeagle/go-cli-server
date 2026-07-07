package client

import (
	"sync"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/socket"
)

// TestStartHeartbeatCancelIdempotent verifies that calling heartbeatCancel
// multiple times does not panic (regression test for double-close of channel).
func TestStartHeartbeatCancelIdempotent(t *testing.T) {
	rc := &ResilientConn{
		config: ResilientConfig{
			HeartbeatInterval: 1, // non-zero to enable heartbeat
		},
	}

	// startHeartbeat creates a done channel and assigns heartbeatCancel
	rc.startHeartbeat()

	if rc.heartbeatCancel == nil {
		t.Fatal("heartbeatCancel should be set after startHeartbeat")
	}

	// Calling cancel multiple times must not panic
	rc.heartbeatCancel()
	rc.heartbeatCancel()
	rc.heartbeatCancel()
}

// TestStartHeartbeatConcurrentCancel verifies no panic when Close and
// reconnectLoop race to cancel the heartbeat.
func TestStartHeartbeatConcurrentCancel(t *testing.T) {
	rc := &ResilientConn{
		config: ResilientConfig{
			HeartbeatInterval: 1,
		},
	}

	rc.startHeartbeat()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Simulate Close() and startHeartbeat() racing
			cancel := rc.heartbeatCancel
			if cancel != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
}

func TestNewResilientConnNormalizesPartialConfig(t *testing.T) {
	rc := NewResilientConn(ResilientConfig{
		HeartbeatInterval: time.Second,
	})

	if rc.config.AutoStartConfig.SocketPath != socket.DefaultSocketPath(socket.DefaultSocketName) {
		t.Fatalf("SocketPath = %q, want default path", rc.config.AutoStartConfig.SocketPath)
	}
	if rc.config.HeartbeatTimeout != 5*time.Second {
		t.Fatalf("HeartbeatTimeout = %v, want 5s", rc.config.HeartbeatTimeout)
	}
	if rc.config.ReconnectBackoffMin != 100*time.Millisecond {
		t.Fatalf("ReconnectBackoffMin = %v, want 100ms", rc.config.ReconnectBackoffMin)
	}
	if rc.config.ReconnectBackoffMax != 30*time.Second {
		t.Fatalf("ReconnectBackoffMax = %v, want 30s", rc.config.ReconnectBackoffMax)
	}
}

func TestNewResilientConnPreservesExplicitConfig(t *testing.T) {
	rc := NewResilientConn(ResilientConfig{
		AutoStartConfig: AutoStartConfig{
			SocketPath: "/tmp/custom.sock",
		},
		HeartbeatInterval:   time.Second,
		HeartbeatTimeout:    750 * time.Millisecond,
		ReconnectBackoffMin: 25 * time.Millisecond,
		ReconnectBackoffMax: 2 * time.Second,
	})

	if rc.config.AutoStartConfig.SocketPath != "/tmp/custom.sock" {
		t.Fatalf("SocketPath = %q, want explicit path", rc.config.AutoStartConfig.SocketPath)
	}
	if rc.config.HeartbeatTimeout != 750*time.Millisecond {
		t.Fatalf("HeartbeatTimeout = %v, want explicit timeout", rc.config.HeartbeatTimeout)
	}
	if rc.config.ReconnectBackoffMin != 25*time.Millisecond {
		t.Fatalf("ReconnectBackoffMin = %v, want explicit min", rc.config.ReconnectBackoffMin)
	}
	if rc.config.ReconnectBackoffMax != 2*time.Second {
		t.Fatalf("ReconnectBackoffMax = %v, want explicit max", rc.config.ReconnectBackoffMax)
	}
}
