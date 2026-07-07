package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/standardbeagle/go-cli-server/socket"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "go-cli-server-client-test-*")
	if err != nil {
		fmt.Printf("skipping client socket integration tests: %v\n", err)
		os.Exit(0)
	}
	defer os.RemoveAll(dir)

	ln, err := socket.Listen("unix", filepath.Join(dir, "probe.sock"))
	if err != nil {
		fmt.Printf("skipping client socket integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = ln.Close()

	os.Exit(m.Run())
}
