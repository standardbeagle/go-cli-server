package client

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	hubsocket "github.com/standardbeagle/go-cli-server/socket"
)

// hangListener accepts connections and never responds, simulating a hung hub.
func hangListener(t *testing.T) (string, func()) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "hang.sock")
	l, err := hubsocket.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- conn // hold the conn open, never reply
		}
	}()
	return sockPath, func() {
		l.Close()
		close(accepted)
		for c := range accepted {
			c.Close()
		}
	}
}

// TestExecuteHonorsTimeout verifies a request against a hung hub returns via the
// read deadline instead of blocking forever.
func TestExecuteHonorsTimeout(t *testing.T) {
	sockPath, cleanup := hangListener(t)
	defer cleanup()

	c := NewConn(WithSocketPath(sockPath), WithTimeout(150*time.Millisecond))
	defer c.Close()

	done := make(chan error, 1)
	go func() {
		done <- c.Request("PING").OK()
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error from hung hub, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request blocked past the read deadline (deadlock)")
	}
}

// TestCloseInterruptsBlockedRead verifies Close aborts an in-flight blocked read
// instead of deadlocking behind the operation mutex.
func TestCloseInterruptsBlockedRead(t *testing.T) {
	sockPath, cleanup := hangListener(t)
	defer cleanup()

	// Long timeout so only Close can unblock the read.
	c := NewConn(WithSocketPath(sockPath), WithTimeout(30*time.Second))

	if err := c.EnsureConnected(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- c.Request("PING").OK()
	}()

	// Let the request reach its blocking read.
	time.Sleep(100 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked behind the in-flight read (deadlock)")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked request did not return after Close()")
	}
}
