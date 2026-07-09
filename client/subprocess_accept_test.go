package client

import (
	"net"
	"sync"
	"testing"
	"time"
)

// tempErr is a net.Error the accept loop treats as transient, so it backs off
// and retries rather than exiting.
type tempErr struct{}

func (tempErr) Error() string   { return "temporary accept failure" }
func (tempErr) Timeout() bool   { return false }
func (tempErr) Temporary() bool { return true }

// alwaysTempListener fails every Accept with a temporary error, parking the
// accept loop in its backoff.
type alwaysTempListener struct{}

func (alwaysTempListener) Accept() (net.Conn, error) { return nil, tempErr{} }
func (alwaysTempListener) Close() error              { return nil }
func (alwaysTempListener) Addr() net.Addr            { return &net.UnixAddr{Name: "test", Net: "unix"} }

// A transient accept failure backs the loop off for up to a second. That wait
// used to be a bare time.Sleep, so Stop — which closes the listener and then
// waits on the epoch's WaitGroup — blocked until the nap ended, for no reason
// other than that the loop was napping.
func TestAcceptLoop_BackoffAbandonedOnShutdown(t *testing.T) {
	s := NewSubprocessServer(SubprocessServerConfig{ID: "test"})
	s.running.Store(true)

	shutdown := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go s.acceptLoop(alwaysTempListener{}, &wg, shutdown)

	// Let the loop fail an Accept and enter its backoff. Growth is 10ms → 1s, so
	// after ~250ms it is waiting on a delay long enough to be observable.
	time.Sleep(250 * time.Millisecond)

	start := time.Now()
	close(shutdown)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop still backing off 2s after shutdown — it slept out its backoff")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("accept loop took %v to notice shutdown, want prompt exit", elapsed)
	}
}

// A closed listener still terminates the loop, shutdown channel or not.
func TestAcceptLoop_ClosedListenerExits(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("unix", dir+"/s.sock")
	if err != nil {
		t.Fatal(err)
	}

	s := NewSubprocessServer(SubprocessServerConfig{ID: "test"})
	s.running.Store(true)

	var wg sync.WaitGroup
	wg.Add(1)
	go s.acceptLoop(ln, &wg, make(chan struct{}))

	ln.Close()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not exit after its listener closed")
	}
}
