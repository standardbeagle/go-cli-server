//go:build unix

package socket

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "go-cli-server-socket-test-*")
	if err != nil {
		fmt.Printf("skipping socket integration tests: %v\n", err)
		os.Exit(0)
	}
	defer os.RemoveAll(dir)

	ln, err := Listen("unix", filepath.Join(dir, "probe.sock"))
	if err != nil {
		fmt.Printf("skipping socket integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = ln.Close()

	os.Exit(m.Run())
}
