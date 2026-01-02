package client

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
)

// TestNewConn verifies connection creation with options.
func TestNewConn(t *testing.T) {
	// Default options
	c := NewConn()
	if c == nil {
		t.Fatal("NewConn returned nil")
	}
	if c.socketPath == "" {
		t.Error("Default socket path should not be empty")
	}
	if c.timeout != 30*time.Second {
		t.Errorf("Default timeout = %v, want 30s", c.timeout)
	}

	// Custom options
	c2 := NewConn(
		WithSocketPath("/custom/path.sock"),
		WithTimeout(10*time.Second),
	)
	if c2.socketPath != "/custom/path.sock" {
		t.Errorf("SocketPath = %q, want %q", c2.socketPath, "/custom/path.sock")
	}
	if c2.timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", c2.timeout)
	}
}

// TestSocketPath verifies SocketPath returns the configured path.
func TestSocketPath(t *testing.T) {
	c := NewConn(WithSocketPath("/test/path.sock"))
	if got := c.SocketPath(); got != "/test/path.sock" {
		t.Errorf("SocketPath() = %q, want %q", got, "/test/path.sock")
	}
}

// TestSetTimeout verifies timeout can be changed.
func TestSetTimeout(t *testing.T) {
	c := NewConn()
	c.SetTimeout(5 * time.Second)
	if c.timeout != 5*time.Second {
		t.Errorf("Timeout after SetTimeout = %v, want 5s", c.timeout)
	}
}

// TestIsConnected verifies connection state tracking.
func TestIsConnected(t *testing.T) {
	c := NewConn(WithSocketPath("/nonexistent.sock"))

	// Initially not connected
	if c.IsConnected() {
		t.Error("IsConnected should be false initially")
	}

	// After close
	c.Close()
	if c.IsConnected() {
		t.Error("IsConnected should be false after Close")
	}
}

// TestClose verifies connection closing.
func TestClose(t *testing.T) {
	c := NewConn()

	// Close should succeed even if not connected
	if err := c.Close(); err != nil {
		t.Errorf("Close() on unconnected returned error: %v", err)
	}

	// Close should be idempotent
	if err := c.Close(); err != nil {
		t.Errorf("Second Close() returned error: %v", err)
	}

	// Connection should be marked as closed
	if !c.closed {
		t.Error("Connection should be marked as closed")
	}
}

// TestDisconnect verifies disconnection allows reconnection.
func TestDisconnect(t *testing.T) {
	c := NewConn()

	// Disconnect should succeed even if not connected
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect() on unconnected returned error: %v", err)
	}

	// Should not be marked as closed
	if c.closed {
		t.Error("Connection should not be marked as closed after Disconnect")
	}
}

// TestEnsureConnectedToNonexistent verifies error on nonexistent socket.
func TestEnsureConnectedToNonexistent(t *testing.T) {
	c := NewConn(WithSocketPath("/nonexistent/path.sock"))

	err := c.EnsureConnected()
	if err == nil {
		t.Error("EnsureConnected to nonexistent socket should fail")
	}
}

// TestEnsureConnectedAfterClose verifies error on closed connection.
func TestEnsureConnectedAfterClose(t *testing.T) {
	c := NewConn()
	c.Close()

	err := c.EnsureConnected()
	if !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("EnsureConnected after Close error = %v, want ErrConnectionClosed", err)
	}
}

// TestWithRealSocket tests with an actual socket.
func TestWithRealSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	// Create a mock server
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to create test socket: %v", err)
	}

	// Handle connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleMockConnection(conn)
		}
	}()

	// Test connection
	c := NewConn(WithSocketPath(sockPath))

	if err := c.EnsureConnected(); err != nil {
		t.Errorf("EnsureConnected() error = %v", err)
	}

	if !c.IsConnected() {
		t.Error("Should be connected after EnsureConnected")
	}

	// Test Ping
	if err := c.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}

	// Test Disconnect and reconnect
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect() error = %v", err)
	}

	if c.IsConnected() {
		t.Error("Should not be connected after Disconnect")
	}

	if err := c.EnsureConnected(); err != nil {
		t.Errorf("EnsureConnected after Disconnect error = %v", err)
	}

	if !c.IsConnected() {
		t.Error("Should be connected after reconnect")
	}

	// Cleanup
	c.Close()
	listener.Close()
}

// handleMockConnection handles a mock server connection.
func handleMockConnection(conn net.Conn) {
	defer conn.Close()

	// Register TEST verb so parser recognizes it
	protocol.DefaultRegistry.RegisterVerb("TEST")
	protocol.DefaultRegistry.RegisterSubVerb("OK", "ERROR", "JSON", "CHUNKED", "ECHO")

	parser := protocol.NewParser(conn)
	writer := protocol.NewWriter(conn)

	for {
		cmd, err := parser.ParseCommand()
		if err != nil {
			return
		}

		switch cmd.Verb {
		case "PING":
			writer.WritePong()
		case "INFO":
			data, _ := json.Marshal(map[string]interface{}{
				"version": "1.0.0",
				"uptime":  100,
			})
			writer.WriteJSON(data)
		case "TEST":
			// Handle TEST command - args[0] is the sub-verb since WriteCommandWithSubVerb
			// puts subVerb as first arg if it's empty string
			subVerb := ""
			if len(cmd.Args) > 0 {
				subVerb = cmd.Args[0]
			}
			if cmd.SubVerb != "" {
				subVerb = cmd.SubVerb
			}

			switch subVerb {
			case "OK":
				writer.WriteOK("success")
			case "ERROR":
				writer.WriteErr(protocol.ErrInvalidAction, "test error")
			case "JSON":
				data, _ := json.Marshal(map[string]interface{}{
					"key":    "value",
					"number": 42,
				})
				writer.WriteJSON(data)
			case "CHUNKED":
				writer.WriteChunk([]byte("chunk1"))
				writer.WriteChunk([]byte("chunk2"))
				writer.WriteEnd()
			case "ECHO":
				if len(cmd.Data) > 0 {
					writer.WriteJSON(cmd.Data)
				} else {
					writer.WriteJSON([]byte("{}"))
				}
			default:
				writer.WriteOK("handled")
			}
		default:
			writer.WriteErr(protocol.ErrInvalidCommand, "unknown command")
		}
	}
}

// TestRequestBuilder tests the request builder pattern.
func TestRequestBuilder(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "builder.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to create test socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handleMockConnection(conn)
		}
	}()

	c := NewConn(WithSocketPath(sockPath))
	defer c.Close()

	t.Run("OK response", func(t *testing.T) {
		err := c.Request("TEST", "OK").OK()
		if err != nil {
			t.Errorf("OK() error = %v", err)
		}
	})

	t.Run("Error response", func(t *testing.T) {
		err := c.Request("TEST", "ERROR").OK()
		if err == nil {
			t.Error("OK() should return error for error response")
		}
		if !errors.Is(err, ErrServerError) {
			t.Errorf("Error should wrap ErrServerError, got %v", err)
		}
	})

	t.Run("JSON response", func(t *testing.T) {
		result, err := c.Request("TEST", "JSON").JSON()
		if err != nil {
			t.Errorf("JSON() error = %v", err)
		}
		if result["key"] != "value" {
			t.Errorf("result[key] = %v, want 'value'", result["key"])
		}
		if result["number"] != float64(42) {
			t.Errorf("result[number] = %v, want 42", result["number"])
		}
	})

	t.Run("JSONInto", func(t *testing.T) {
		var result struct {
			Key    string `json:"key"`
			Number int    `json:"number"`
		}
		err := c.Request("TEST", "JSON").JSONInto(&result)
		if err != nil {
			t.Errorf("JSONInto() error = %v", err)
		}
		if result.Key != "value" {
			t.Errorf("result.Key = %q, want 'value'", result.Key)
		}
		if result.Number != 42 {
			t.Errorf("result.Number = %d, want 42", result.Number)
		}
	})

	t.Run("Bytes response", func(t *testing.T) {
		data, err := c.Request("TEST", "JSON").Bytes()
		if err != nil {
			t.Errorf("Bytes() error = %v", err)
		}
		if len(data) == 0 {
			t.Error("Bytes() returned empty data")
		}
	})

	t.Run("Chunked response", func(t *testing.T) {
		data, err := c.Request("TEST", "CHUNKED").Chunked()
		if err != nil {
			t.Errorf("Chunked() error = %v", err)
		}
		if string(data) != "chunk1chunk2" {
			t.Errorf("Chunked() data = %q, want 'chunk1chunk2'", data)
		}
	})

	t.Run("String response", func(t *testing.T) {
		s, err := c.Request("TEST", "CHUNKED").String()
		if err != nil {
			t.Errorf("String() error = %v", err)
		}
		if s != "chunk1chunk2" {
			t.Errorf("String() = %q, want 'chunk1chunk2'", s)
		}
	})

	t.Run("WithArgs", func(t *testing.T) {
		// Just verify the builder works; server doesn't use args in this test
		err := c.Request("TEST", "OK").WithArgs("arg1", "arg2").OK()
		if err != nil {
			t.Errorf("WithArgs().OK() error = %v", err)
		}
	})

	t.Run("WithData", func(t *testing.T) {
		data, err := c.Request("TEST", "ECHO").WithData([]byte(`{"test":"data"}`)).Bytes()
		if err != nil {
			t.Errorf("WithData().Bytes() error = %v", err)
		}
		if string(data) != `{"test":"data"}` {
			t.Errorf("Echo data = %q, want '{\"test\":\"data\"}'", data)
		}
	})

	t.Run("WithJSON", func(t *testing.T) {
		input := map[string]string{"key": "value"}
		data, err := c.Request("TEST", "ECHO").WithJSON(input).Bytes()
		if err != nil {
			t.Errorf("WithJSON().Bytes() error = %v", err)
		}
		var result map[string]string
		json.Unmarshal(data, &result)
		if result["key"] != "value" {
			t.Errorf("Echo result = %v, want key=value", result)
		}
	})

	t.Run("Unknown command", func(t *testing.T) {
		err := c.Request("UNKNOWN").OK()
		if err == nil {
			t.Error("Unknown command should return error")
		}
	})
}

// TestConcurrentRequests tests thread safety of the connection.
func TestConcurrentRequests(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "concurrent.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to create test socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handleMockConnection(conn)
		}
	}()

	c := NewConn(WithSocketPath(sockPath))
	defer c.Close()

	var wg sync.WaitGroup
	errChan := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Ping(); err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent Ping error: %v", err)
	}
}

// TestErrorTypes verifies error type definitions.
func TestErrorTypes(t *testing.T) {
	if ErrNotConnected == nil {
		t.Error("ErrNotConnected should not be nil")
	}
	if ErrConnectionClosed == nil {
		t.Error("ErrConnectionClosed should not be nil")
	}
	if ErrServerError == nil {
		t.Error("ErrServerError should not be nil")
	}

	// Verify distinct error messages
	msgs := map[string]bool{}
	for _, err := range []error{ErrNotConnected, ErrConnectionClosed, ErrServerError} {
		msg := err.Error()
		if msgs[msg] {
			t.Errorf("Duplicate error message: %q", msg)
		}
		msgs[msg] = true
	}
}

// TestWithTimeout option.
func TestWithTimeoutOption(t *testing.T) {
	opt := WithTimeout(5 * time.Second)
	c := &Conn{}
	opt(c)
	if c.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.timeout)
	}
}

// TestWithSocketPath option.
func TestWithSocketPathOption(t *testing.T) {
	opt := WithSocketPath("/custom.sock")
	c := &Conn{}
	opt(c)
	if c.socketPath != "/custom.sock" {
		t.Errorf("socketPath = %q, want '/custom.sock'", c.socketPath)
	}
}

// TestRequestBuilderChaining verifies method chaining works correctly.
func TestRequestBuilderChaining(t *testing.T) {
	c := NewConn(WithSocketPath("/test.sock"))

	rb := c.Request("VERB", "ARG1")
	if rb.verb != "VERB" {
		t.Errorf("verb = %q, want 'VERB'", rb.verb)
	}
	if len(rb.args) != 1 || rb.args[0] != "ARG1" {
		t.Errorf("args = %v, want ['ARG1']", rb.args)
	}

	// Chain WithArgs
	rb2 := rb.WithArgs("ARG2", "ARG3")
	if rb2 != rb {
		t.Error("WithArgs should return same builder")
	}
	if len(rb.args) != 3 {
		t.Errorf("args after WithArgs = %v, want 3 elements", rb.args)
	}

	// Chain WithData
	rb3 := rb.WithData([]byte("test"))
	if rb3 != rb {
		t.Error("WithData should return same builder")
	}
	if string(rb.data) != "test" {
		t.Errorf("data = %q, want 'test'", rb.data)
	}

	// Chain WithJSON (replaces data)
	rb4 := rb.WithJSON(map[string]string{"key": "value"})
	if rb4 != rb {
		t.Error("WithJSON should return same builder")
	}
	if rb.data == nil || len(rb.data) == 0 {
		t.Error("WithJSON should set data")
	}
}

// TestHandleErrorLocked verifies error handling clears connection state.
func TestHandleErrorLocked(t *testing.T) {
	c := NewConn()
	c.mu.Lock()

	// Simulate a connected state
	c.conn = &mockConn{}
	c.parser = &protocol.Parser{}
	c.writer = &protocol.Writer{}

	c.handleErrorLocked()

	if c.conn != nil {
		t.Error("conn should be nil after handleErrorLocked")
	}
	if c.parser != nil {
		t.Error("parser should be nil after handleErrorLocked")
	}
	if c.writer != nil {
		t.Error("writer should be nil after handleErrorLocked")
	}

	c.mu.Unlock()
}

// mockConn implements net.Conn for testing.
type mockConn struct{}

func (m *mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// BenchmarkPing measures ping performance.
func BenchmarkPing(b *testing.B) {
	tmpDir := b.TempDir()
	sockPath := filepath.Join(tmpDir, "bench.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		b.Fatalf("Failed to create test socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handleMockConnection(conn)
		}
	}()

	c := NewConn(WithSocketPath(sockPath))
	defer c.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Ping(); err != nil {
			b.Fatalf("Ping error: %v", err)
		}
	}
}

// BenchmarkJSONRequest measures JSON request performance.
func BenchmarkJSONRequest(b *testing.B) {
	tmpDir := b.TempDir()
	sockPath := filepath.Join(tmpDir, "bench.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		b.Fatalf("Failed to create test socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handleMockConnection(conn)
		}
	}()

	c := NewConn(WithSocketPath(sockPath))
	defer c.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.Request("TEST", "JSON").JSON()
		if err != nil {
			b.Fatalf("JSON request error: %v", err)
		}
	}
}
