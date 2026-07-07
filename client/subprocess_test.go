package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
)

var errTestWrite = errors.New("write failed")

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errTestWrite }

func TestSubprocessServer_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "unix",
			Address: sockPath,
		},
	})

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify it's listening
	if server.Address() == "" {
		t.Error("Address() returned empty")
	}

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Stop(ctx); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

func TestSubprocessServer_WaitBeforeStartReturns(t *testing.T) {
	server := NewSubprocessServer(SubprocessServerConfig{
		ID:        "test",
		Transport: TransportConfig{Type: "tcp", Address: "127.0.0.1:0"},
	})

	done := make(chan struct{})
	go func() {
		server.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Wait before Start blocked")
	}
}

func TestSubprocessServer_StartDoesNotRemoveNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}

	server := NewSubprocessServer(SubprocessServerConfig{
		ID:        "test",
		Transport: TransportConfig{Type: "unix", Address: path},
	})

	if err := server.Start(); err == nil {
		_ = server.Stop(context.Background())
		t.Fatal("Start succeeded with non-socket path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
	if string(data) != "keep me" {
		t.Fatalf("regular file contents changed: %q", string(data))
	}
}

// TestSubprocessServer_Restart verifies a server can be restarted after Stop
// (the shutdown channel must be reinstated, not left closed).
func TestSubprocessServer_Restart(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "restart.sock")

	server := NewSubprocessServer(SubprocessServerConfig{
		ID:        "restart",
		Transport: TransportConfig{Type: "unix", Address: sockPath},
	})

	for i := 0; i < 3; i++ {
		if err := server.Start(); err != nil {
			t.Fatalf("Start #%d failed: %v", i, err)
		}
		// A live epoch must accept a connection.
		conn, err := net.DialTimeout("unix", sockPath, time.Second)
		if err != nil {
			t.Fatalf("dial after Start #%d: %v", i, err)
		}
		conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := server.Stop(ctx); err != nil {
			t.Errorf("Stop #%d failed: %v", i, err)
		}
		cancel()
	}
}

// TestSubprocessServer_ConcurrentStartStop hammers Start/Stop concurrently to
// surface epoch-swap races (double-close panic, listener mis-close, wg reuse)
// under the race detector.
func TestSubprocessServer_ConcurrentStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "concurrent.sock")

	server := NewSubprocessServer(SubprocessServerConfig{
		ID:        "concurrent",
		Transport: TransportConfig{Type: "unix", Address: sockPath},
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = server.Start() }()
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Stop(ctx)
		}()
	}
	wg.Wait()

	// Leave it stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Stop(ctx)
}

func TestSubprocessServer_HandleCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "unix",
			Address: sockPath,
		},
	})

	// Register handler
	server.RegisterHandler("ECHO", func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
		return JSONResponse(map[string]interface{}{
			"verb":    cmd.Verb,
			"subverb": cmd.SubVerb,
			"args":    cmd.Args,
		})
	})

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = server.Stop(context.Background()) }()

	// Connect as client
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	parser := protocol.NewParser(conn)
	writer := protocol.NewWriter(conn)

	// Send PING
	if err := writer.WriteCommand("PING", nil, nil); err != nil {
		t.Fatalf("WriteCommand failed: %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}

	if resp.Type != protocol.ResponsePong {
		t.Errorf("Expected PONG, got %s", resp.Type)
	}

	// Send ECHO command
	if err := writer.WriteCommandWithSubVerb("ECHO", "TEST", []string{"arg1", "arg2"}, nil); err != nil {
		t.Fatalf("WriteCommand failed: %v", err)
	}

	resp, err = parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Expected JSON, got %s", resp.Type)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if result["verb"] != "ECHO" {
		t.Errorf("verb = %v, want ECHO", result["verb"])
	}
}

func TestSubprocessServer_UnknownCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "unix",
			Address: sockPath,
		},
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = server.Stop(context.Background()) }()

	// Connect as client
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	parser := protocol.NewParser(conn)
	_ = protocol.NewWriter(conn) // unused but kept for symmetry

	// Send unknown command (need to register it with the parser first)
	// For this test, we'll use a raw write
	rawCmd := "UNKNOWN;;"
	_, _ = conn.Write([]byte(rawCmd))

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}

	if resp.Type != protocol.ResponseErr {
		t.Errorf("Expected ERR, got %s", resp.Type)
	}
}

func TestSubprocessServer_TCPTransport(t *testing.T) {
	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "tcp",
			Address: "127.0.0.1:0", // Dynamic port
		},
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = server.Stop(context.Background()) }()

	addr := server.Address()
	if addr == "" {
		t.Fatal("Address() returned empty")
	}

	// Connect
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	parser := protocol.NewParser(conn)
	writer := protocol.NewWriter(conn)

	// Send PING
	if err := writer.WriteCommand("PING", nil, nil); err != nil {
		t.Fatalf("WriteCommand failed: %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}

	if resp.Type != protocol.ResponsePong {
		t.Errorf("Expected PONG, got %s", resp.Type)
	}
}

func TestSubprocessServer_Callbacks(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	connected := make(chan struct{})
	disconnected := make(chan error, 1)

	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "unix",
			Address: sockPath,
		},
		OnConnect: func() {
			close(connected)
		},
		OnDisconnect: func(err error) {
			disconnected <- err
		},
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = server.Stop(context.Background()) }()

	// Connect
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}

	// Wait for connect callback
	select {
	case <-connected:
		// OK
	case <-time.After(time.Second):
		t.Error("OnConnect not called")
	}

	// Close connection
	conn.Close()

	// Wait for disconnect callback
	select {
	case <-disconnected:
		// OK
	case <-time.After(2 * time.Second):
		// May not always trigger depending on read timeout
	}
}

func TestResponseHelpers(t *testing.T) {
	t.Run("OKResponse", func(t *testing.T) {
		resp := OKResponse("success")
		if resp.Type != protocol.ResponseOK {
			t.Errorf("Type = %s, want OK", resp.Type)
		}
		if resp.Message != "success" {
			t.Errorf("Message = %s, want success", resp.Message)
		}
	})

	t.Run("ErrResponse", func(t *testing.T) {
		resp := ErrResponse(protocol.ErrNotFound, "not found")
		if resp.Type != protocol.ResponseErr {
			t.Errorf("Type = %s, want ERR", resp.Type)
		}
		if resp.Code != string(protocol.ErrNotFound) {
			t.Errorf("Code = %s, want not_found", resp.Code)
		}
	})

	t.Run("JSONResponse", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		resp := JSONResponse(data)
		if resp.Type != protocol.ResponseJSON {
			t.Errorf("Type = %s, want JSON", resp.Type)
		}

		var result map[string]string
		if err := json.Unmarshal(resp.Data, &result); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		if result["key"] != "value" {
			t.Errorf("key = %s, want value", result["key"])
		}
	})

	t.Run("DataResponse", func(t *testing.T) {
		data := []byte{1, 2, 3, 4}
		resp := DataResponse(data)
		if resp.Type != protocol.ResponseData {
			t.Errorf("Type = %s, want DATA", resp.Type)
		}
		if len(resp.Data) != 4 {
			t.Errorf("Data length = %d, want 4", len(resp.Data))
		}
	})
}

func TestWriteStdioResponseReturnsWriteError(t *testing.T) {
	writer := protocol.NewWriter(errWriter{})
	err := writeStdioResponse(writer, OKResponse("ok"))
	if !errors.Is(err, errTestWrite) {
		t.Fatalf("writeStdioResponse error = %v, want %v", err, errTestWrite)
	}
}

func TestRegisterHandlers(t *testing.T) {
	server := NewSubprocessServer(SubprocessServerConfig{
		ID: "test",
		Transport: TransportConfig{
			Type:    "tcp",
			Address: "127.0.0.1:0",
		},
	})

	handlers := map[string]CommandHandler{
		"CMD1": func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
			return OKResponse("cmd1")
		},
		"CMD2": func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
			return OKResponse("cmd2")
		},
	}

	server.RegisterHandlers(handlers)

	// Verify handlers are registered
	server.handlersMu.RLock()
	defer server.handlersMu.RUnlock()

	if _, ok := server.handlers["CMD1"]; !ok {
		t.Error("CMD1 handler not registered")
	}
	if _, ok := server.handlers["CMD2"]; !ok {
		t.Error("CMD2 handler not registered")
	}
}

func TestSubprocessStdioServer(t *testing.T) {
	server := NewSubprocessStdioServer()

	server.RegisterHandler("TEST", func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
		return OKResponse("ok")
	})

	// Can't easily test stdin/stdout without more setup
	// Just verify handler registration works
	server.handlersMu.RLock()
	_, ok := server.handlers["TEST"]
	server.handlersMu.RUnlock()

	if !ok {
		t.Error("Handler not registered")
	}
}

func TestSubprocessStdioServerStopUnblocksRun(t *testing.T) {
	inputR, inputW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputW.Close()

	outputR, outputW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputR.Close()
	defer outputW.Close()

	server := NewSubprocessStdioServerWithIO(inputR, outputW)

	done := make(chan error, 1)
	go func() {
		done <- server.Run()
	}()

	time.Sleep(50 * time.Millisecond)
	server.Stop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error after Stop = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not unblock Run()")
	}
}
