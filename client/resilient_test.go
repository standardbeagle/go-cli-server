package client

import (
	"sync"
	"testing"
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
