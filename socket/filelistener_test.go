package socket

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// fileListener must close the descriptor it was handed exactly once.
//
// os.NewFile takes ownership, so `defer file.Close()` already closes it. The
// error path used to close it a second time with syscall.Close. A double close
// is not a no-op when other goroutines are opening descriptors: the kernel can
// hand that number to a socket someone else just opened, and the second close
// shuts theirs down instead. The victim then sees a listener that stops
// accepting, or one that stays alive after its own Close — with nothing in the
// stack pointing back here.
//
// Reproduced deterministically: hand fileListener an fd that makes
// net.FileListener fail (a plain file, not a socket), then check whether a
// descriptor opened afterwards is still valid.
func TestFileListener_ErrorPathClosesFdExactlyOnce(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "notasocket")
	base, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	// Concurrent openers hold descriptors and check they stay valid. A double
	// close inside fileListener's error path lands on whichever number the kernel
	// recycled into one of these sockets.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var stolen atomic.Int64

	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
				if err != nil {
					continue
				}
				var st syscall.Stat_t
				for j := 0; j < 50; j++ {
					if err := syscall.Fstat(fd, &st); err != nil {
						stolen.Add(1)
						break
					}
				}
				_ = syscall.Close(fd)
			}
		}()
	}

	// Repeatedly drive fileListener down its error path with a fresh dup each
	// time: net.FileListener rejects a regular file.
	for i := 0; i < 2000; i++ {
		fd, err := syscall.Dup(int(base.Fd()))
		if err != nil {
			break
		}
		if _, err := fileListener(fd, tmp); err == nil {
			t.Fatal("expected fileListener to fail on a non-socket fd")
		}
	}

	close(stop)
	wg.Wait()

	if n := stolen.Load(); n > 0 {
		t.Fatalf("fileListener closed %d descriptor(s) belonging to another goroutine", n)
	}
}

// The success path still yields a working listener whose Close actually stops it.
func TestFileListener_SuccessPathListenerClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sock")

	ln, err := listenUnix(path)
	if err != nil {
		t.Fatal(err)
	}

	if !IsRunning(path) {
		t.Fatal("listener should be accepting connections")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if IsRunning(path) {
		c, derr := net.Dial("unix", path)
		if c != nil {
			c.Close()
		}
		t.Fatalf("socket still listening after Close (dial err=%v)", derr)
	}
}
