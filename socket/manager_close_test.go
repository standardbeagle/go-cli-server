package socket

import (
	"os"
	"path/filepath"
	"testing"
)

// A hub closes the listener it was handed before the socket Manager closes it
// again. The second close must not surface as an error on any platform; on
// Windows it used to fail with "use of closed network connection".
func TestManagerCloseAfterListenerAlreadyClosed(t *testing.T) {
	dir, err := os.MkdirTemp("", "gcs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sm := NewManager(Config{Path: filepath.Join(dir, "m.sock")})
	ln, err := sm.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := sm.Close(); err != nil {
		t.Fatalf("Manager.Close after listener already closed: %v", err)
	}
}
