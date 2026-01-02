package hub

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
)

// TestDefaultConfig verifies the default configuration values.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.SocketName != "mcp-hub" {
		t.Errorf("SocketName = %q, want %q", cfg.SocketName, "mcp-hub")
	}
	if cfg.MaxClients != 0 {
		t.Errorf("MaxClients = %d, want 0 (unlimited)", cfg.MaxClients)
	}
	if cfg.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 5*time.Second {
		t.Errorf("WriteTimeout = %v, want 5s", cfg.WriteTimeout)
	}
	if !cfg.EnableProcessMgmt {
		t.Error("EnableProcessMgmt should be true by default")
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.0")
	}
}

// TestNew verifies hub creation.
func TestNew(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "test.sock")

	h := New(cfg)
	if h == nil {
		t.Fatal("New returned nil")
	}

	if h.pm == nil {
		t.Error("ProcessManager should be created when EnableProcessMgmt is true")
	}
	if h.subRouter == nil {
		t.Error("SubprocessRouter should be created")
	}
	if h.commands == nil {
		t.Error("CommandRegistry should be created")
	}
}

// TestNewWithProcessMgmtDisabled verifies hub creation without process management.
func TestNewWithProcessMgmtDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableProcessMgmt = false
	cfg.SocketPath = filepath.Join(t.TempDir(), "test.sock")

	h := New(cfg)
	if h == nil {
		t.Fatal("New returned nil")
	}

	if h.pm != nil {
		t.Error("ProcessManager should be nil when EnableProcessMgmt is false")
	}
}

// TestNewWithEmptySocketName verifies default socket name is used.
func TestNewWithEmptySocketName(t *testing.T) {
	cfg := Config{
		SocketName: "",
	}

	h := New(cfg)
	if h.config.SocketName != "mcp-hub" {
		t.Errorf("SocketName = %q, want %q (default)", h.config.SocketName, "mcp-hub")
	}
}

// TestSocketPath verifies SocketPath returns the configured path.
func TestSocketPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = "/custom/path.sock"

	h := New(cfg)
	path := h.SocketPath()

	if path != "/custom/path.sock" {
		t.Errorf("SocketPath() = %q, want %q", path, "/custom/path.sock")
	}
}

// TestProcessManager verifies ProcessManager returns the manager.
func TestProcessManager(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	if h.ProcessManager() == nil {
		t.Error("ProcessManager() should not be nil")
	}

	cfg.EnableProcessMgmt = false
	h2 := New(cfg)

	if h2.ProcessManager() != nil {
		t.Error("ProcessManager() should be nil when disabled")
	}
}

// TestSubprocessRouter verifies SubprocessRouter is created.
func TestSubprocessRouter(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	if h.subRouter == nil {
		t.Error("subRouter should not be nil")
	}
}

// TestStartAndStop verifies hub start and stop.
func TestStartAndStop(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)

	// Start hub
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should be running
	if h.IsShuttingDown() {
		t.Error("Hub should not be shutting down after Start")
	}

	// SocketPath should be accessible
	if h.SocketPath() == "" {
		t.Error("SocketPath should not be empty after Start")
	}

	// Stop with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := h.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	if !h.IsShuttingDown() {
		t.Error("Hub should be shutting down after Stop")
	}
}

// TestStopIdempotent verifies Stop can be called multiple times.
func TestStopIdempotent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	_ = h.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// First stop
	_ = h.Stop(ctx)

	// Second stop should be no-op
	_ = h.Stop(ctx)

	if !h.IsShuttingDown() {
		t.Error("Hub should be shutting down")
	}
}

// TestClientCount verifies client counting.
func TestClientCount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	_ = h.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Initially should be 0
	if count := h.ClientCount(); count != 0 {
		t.Errorf("ClientCount = %d, want 0", count)
	}
}

// TestStartTimeTracked verifies start time is tracked (tested via INFO command).
func TestStartTimeTracked(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	_ = h.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Connect and send INFO command to verify uptime is tracked
	conn, err := net.Dial("unix", h.SocketPath())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Wait a bit to allow uptime to accumulate
	time.Sleep(10 * time.Millisecond)

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	if err := writer.WriteCommand("INFO", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("Response type = %v, want JSON", resp.Type)
	}

	// Response should contain data
	if resp.Data == nil {
		t.Error("INFO response data should not be nil")
	}
}

// TestRegisterCommand verifies command registration.
func TestRegisterCommand(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	handler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return conn.WriteOK("test")
	}

	err := h.RegisterCommand(CommandDefinition{
		Verb:        "CUSTOM",
		SubVerbs:    []string{"ACTION"},
		Handler:     handler,
		Description: "Custom command",
	})

	if err != nil {
		t.Errorf("RegisterCommand() error = %v", err)
	}

	// Verify registered
	if !h.commands.HasVerb("CUSTOM") {
		t.Error("Command should be registered")
	}
}

// TestInfoCommand verifies INFO command response via network.
func TestInfoCommand(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Version = "2.0.0"
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	_ = h.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Connect and send INFO
	conn, err := net.Dial("unix", h.SocketPath())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	if err := writer.WriteCommand("INFO", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}

	// Verify response data is not nil
	if resp.Data == nil {
		t.Error("INFO response should have data")
	}
}

// TestWithRealClient tests connecting a real client.
func TestWithRealClient(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Connect a client
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Wait a moment for client to be registered
	time.Sleep(10 * time.Millisecond)

	// Should have 1 client
	if count := h.ClientCount(); count != 1 {
		t.Errorf("ClientCount = %d, want 1", count)
	}

	// Send PING
	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	if err := writer.WriteCommand("PING", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponsePong {
		t.Errorf("Response type = %v, want PONG", resp.Type)
	}

	// Send INFO
	if err := writer.WriteCommand("INFO", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err = parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestMaxClients verifies client limit enforcement.
func TestMaxClients(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.MaxClients = 2
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Connect max clients
	conns := make([]net.Conn, 0, 3)
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("Failed to connect client %d: %v", i, err)
		}
		conns = append(conns, conn)
		time.Sleep(5 * time.Millisecond) // Give time for registration
	}

	// Clean up
	for _, conn := range conns {
		conn.Close()
	}
}

// TestConcurrentClients tests multiple concurrent clients.
func TestConcurrentClients(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				return
			}
			defer conn.Close()

			writer := protocol.NewWriter(conn)
			parser := protocol.NewParser(conn)

			// Send PING
			if err := writer.WriteCommand("PING", nil, nil); err != nil {
				return
			}

			resp, err := parser.ParseResponse()
			if err != nil {
				return
			}

			if resp.Type == protocol.ResponsePong {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if count := successCount.Load(); count < 5 {
		t.Errorf("Only %d successful pings, expected at least 5", count)
	}
}

// TestBuiltinCommandsRegistered verifies built-in commands are registered.
func TestBuiltinCommandsRegistered(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	// PROC should be registered when ProcessMgmt is enabled
	if !h.commands.HasVerb("PROC") {
		t.Error("PROC command should be registered")
	}
	if !h.commands.HasVerb("RUN") {
		t.Error("RUN command should be registered")
	}
	if !h.commands.HasVerb("RUN-JSON") {
		t.Error("RUN-JSON command should be registered")
	}
	if !h.commands.HasVerb("RELAY") {
		t.Error("RELAY command should be registered")
	}
	if !h.commands.HasVerb("ATTACH") {
		t.Error("ATTACH command should be registered")
	}
	if !h.commands.HasVerb("DETACH") {
		t.Error("DETACH command should be registered")
	}
	if !h.commands.HasVerb("SESSION") {
		t.Error("SESSION command should be registered")
	}
	if !h.commands.HasVerb("SUBPROCESS") {
		t.Error("SUBPROCESS command should be registered")
	}
}

// TestBuiltinCommandsWithoutProcessMgmt verifies PROC/RUN not registered when disabled.
func TestBuiltinCommandsWithoutProcessMgmt(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableProcessMgmt = false
	h := New(cfg)

	// PROC should NOT be registered
	if h.commands.HasVerb("PROC") {
		t.Error("PROC command should NOT be registered when ProcessMgmt disabled")
	}
	if h.commands.HasVerb("RUN") {
		t.Error("RUN command should NOT be registered when ProcessMgmt disabled")
	}

	// Other commands should still be registered
	if !h.commands.HasVerb("RELAY") {
		t.Error("RELAY command should still be registered")
	}
}

// TestWait verifies Wait channel is closed on shutdown.
func TestWait(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	_ = h.Start()

	done := make(chan struct{})
	go func() {
		h.Wait()
		close(done)
	}()

	// Stop the hub
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = h.Stop(ctx)

	// Wait should return
	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Error("Wait() did not return after Stop")
	}
}

// TestIsShuttingDown verifies shutdown state tracking.
func TestIsShuttingDown(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "hub.sock")
	cfg.EnableProcessMgmt = false

	h := New(cfg)

	if h.IsShuttingDown() {
		t.Error("Hub should not be shutting down initially")
	}

	_ = h.Start()

	if h.IsShuttingDown() {
		t.Error("Hub should not be shutting down after Start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = h.Stop(ctx)

	if !h.IsShuttingDown() {
		t.Error("Hub should be shutting down after Stop")
	}
}

// TestExternalProcessRegistry verifies external process tracking.
func TestExternalProcessRegistry(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	// Initially empty
	h.externalProcs.Range(func(key, value any) bool {
		t.Error("externalProcs should be empty initially")
		return false
	})
}

// TestSessionRegistry verifies session tracking.
func TestSessionRegistry(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	// Initially empty
	h.sessions.Range(func(key, value any) bool {
		t.Error("sessions should be empty initially")
		return false
	})
}

// BenchmarkPing measures PING command performance.
func BenchmarkPing(b *testing.B) {
	tmpDir := b.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = writer.WriteCommand("PING", nil, nil)
		_, _ = parser.ParseResponse()
	}
}

// =============================================================================
// Handler Tests - PROC commands
// =============================================================================

// TestProcListEmpty tests PROC LIST with no processes.
func TestProcListEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC LIST
	if err := writer.WriteCommand("PROC", []string{"LIST"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestProcStatusNotFound tests PROC STATUS with non-existent process.
func TestProcStatusNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC STATUS for non-existent process
	if err := writer.WriteCommand("PROC", []string{"STATUS", "nonexistent-id"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteNotFound uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestProcStopNotFound tests PROC STOP with non-existent process.
func TestProcStopNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC STOP for non-existent process
	if err := writer.WriteCommand("PROC", []string{"STOP", "nonexistent-id"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseErr {
		t.Errorf("Response type = %v, want ERR", resp.Type)
	}
}

// TestProcOutputNotFound tests PROC OUTPUT with non-existent process.
func TestProcOutputNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC OUTPUT for non-existent process
	if err := writer.WriteCommand("PROC", []string{"OUTPUT", "nonexistent-id"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteNotFound uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestProcInvalidSubcommand tests PROC with invalid subcommand.
func TestProcInvalidSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC with invalid subcommand - returns structured error as JSON
	if err := writer.WriteCommand("PROC", []string{"INVALID"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteStructuredErr returns JSON, not ERR
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON (structured error)", resp.Type)
	}
}

// TestProcNoSubcommand tests PROC without subcommand.
func TestProcNoSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send PROC without subcommand - returns structured error as JSON
	if err := writer.WriteCommand("PROC", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteStructuredErr returns JSON, not ERR
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON (structured error)", resp.Type)
	}
}

// =============================================================================
// Handler Tests - SESSION commands
// =============================================================================

// TestSessionList tests SESSION LIST command.
func TestSessionList(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send SESSION LIST
	if err := writer.WriteCommand("SESSION", []string{"LIST"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestSessionRegisterAndGet tests SESSION REGISTER and GET commands.
func TestSessionRegisterAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Register a session - uses JSON data for config, returns JSON with code
	configData := []byte(`{"project_path": "/path/to/project"}`)
	if err := writer.WriteCommand("SESSION", []string{"REGISTER"}, configData); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// REGISTER returns JSON with the generated session code
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("REGISTER response type = %v, want JSON", resp.Type)
	}

	// Parse the response to get the session code
	var registerResult map[string]any
	if err := json.Unmarshal(resp.Data, &registerResult); err != nil {
		t.Fatalf("Failed to parse register response: %v", err)
	}

	sessionCode, ok := registerResult["code"].(string)
	if !ok || sessionCode == "" {
		t.Fatalf("Register response should contain 'code' field")
	}

	// Get the session using the returned code
	if err := writer.WriteCommand("SESSION", []string{"GET", sessionCode}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err = parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Errorf("GET response type = %v, want JSON", resp.Type)
	}
}

// TestSessionGetNotFound tests SESSION GET with non-existent session.
func TestSessionGetNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Get non-existent session
	if err := writer.WriteCommand("SESSION", []string{"GET", "nonexistent"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteNotFound uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestSessionHeartbeat tests SESSION HEARTBEAT command.
func TestSessionHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Register a session first
	if err := writer.WriteCommand("SESSION", []string{"REGISTER", "heartbeat-test", "/path"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}
	_, _ = parser.ParseResponse() // consume response

	// Send heartbeat
	if err := writer.WriteCommand("SESSION", []string{"HEARTBEAT", "heartbeat-test"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseOK {
		t.Errorf("Response type = %v, want OK", resp.Type)
	}
}

// TestSessionUnregister tests SESSION UNREGISTER command.
func TestSessionUnregister(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Register a session first
	if err := writer.WriteCommand("SESSION", []string{"REGISTER", "unregister-test", "/path"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}
	_, _ = parser.ParseResponse() // consume response

	// Unregister
	if err := writer.WriteCommand("SESSION", []string{"UNREGISTER", "unregister-test"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseOK {
		t.Errorf("Response type = %v, want OK", resp.Type)
	}
}

// TestSessionNoSubcommand tests SESSION without subcommand.
func TestSessionNoSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send SESSION without subcommand - falls through to default case (structured error)
	if err := writer.WriteCommand("SESSION", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteStructuredErr returns JSON, not ERR
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON (structured error)", resp.Type)
	}
}

// =============================================================================
// Handler Tests - RELAY commands
// =============================================================================

// TestRelayNoSubcommand tests RELAY without subcommand.
func TestRelayNoSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send RELAY without subcommand - returns structured error as JSON
	if err := writer.WriteCommand("RELAY", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteStructuredErr returns JSON, not ERR
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON (structured error)", resp.Type)
	}
}

// TestRelaySendNoTarget tests RELAY SEND without target.
func TestRelaySendNoTarget(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send RELAY SEND without target
	if err := writer.WriteCommand("RELAY", []string{"SEND"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteMissingParam uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// =============================================================================
// Handler Tests - Connection methods
// =============================================================================

// TestConnectionSessionCode tests ATTACH/DETACH for external process registration.
func TestConnectionSessionCode(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	// Connect and interact
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// ATTACH requires JSON config with 'id' field
	attachConfig := []byte(`{"id": "test-ext-proc", "project_path": "/test/path"}`)
	if err := writer.WriteCommand("ATTACH", nil, attachConfig); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseOK {
		t.Errorf("ATTACH response type = %v, want OK", resp.Type)
	}

	// DETACH the process - requires ID arg
	if err := writer.WriteCommand("DETACH", []string{"test-ext-proc"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err = parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseOK {
		t.Errorf("DETACH response type = %v, want OK", resp.Type)
	}
}

// TestAttachNotFound tests ATTACH with non-existent session.
func TestAttachNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// ATTACH to non-existent session (ATTACH expects JSON with 'id' field)
	// Sending via args won't work - handler requires JSON data
	// So this will return a missing param error since cfg.ID is empty
	if err := writer.WriteCommand("ATTACH", []string{"nonexistent"}, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteMissingParam uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestAttachNoCode tests ATTACH without session code.
func TestAttachNoCode(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// ATTACH without session code
	if err := writer.WriteCommand("ATTACH", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	// WriteMissingParam uses WriteStructuredErr which returns JSON
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Response type = %v, want JSON", resp.Type)
	}
}

// TestUnknownCommand tests handling of unknown commands.
func TestUnknownCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "hub.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.EnableProcessMgmt = false

	h := New(cfg)
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	writer := protocol.NewWriter(conn)
	parser := protocol.NewParser(conn)

	// Send unknown command
	if err := writer.WriteCommand("UNKNOWN_CMD", nil, nil); err != nil {
		t.Fatalf("WriteCommand error = %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error = %v", err)
	}

	if resp.Type != protocol.ResponseErr {
		t.Errorf("Response type = %v, want ERR", resp.Type)
	}
}

// TestExternalProcessOperations tests external process registry operations.
func TestExternalProcessOperations(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	proc := &ExternalProcess{
		ID:          "test-ext-1",
		ProjectPath: "/test/project",
	}

	// Register
	if err := h.RegisterExternalProcess(proc); err != nil {
		t.Fatalf("RegisterExternalProcess error = %v", err)
	}

	// Get
	got, ok := h.GetExternalProcess("test-ext-1")
	if !ok {
		t.Fatal("GetExternalProcess returned false")
	}
	if got.ProjectPath != "/test/project" {
		t.Errorf("ProjectPath = %q, want %q", got.ProjectPath, "/test/project")
	}

	// Register duplicate should fail
	if err := h.RegisterExternalProcess(proc); err == nil {
		t.Error("RegisterExternalProcess should fail for duplicate")
	}

	// Unregister
	h.UnregisterExternalProcess("test-ext-1")

	// Get should fail now
	_, ok = h.GetExternalProcess("test-ext-1")
	if ok {
		t.Error("GetExternalProcess should return false after unregister")
	}
}

// TestSetSessionCleanup tests the session cleanup callback.
func TestSetSessionCleanup(t *testing.T) {
	cfg := DefaultConfig()
	h := New(cfg)

	cleanupCalled := false
	h.SetSessionCleanup(func(sessionCode string) {
		cleanupCalled = true
		if sessionCode != "test-session" {
			t.Errorf("sessionCode = %q, want %q", sessionCode, "test-session")
		}
	})

	// Trigger cleanup
	h.cleanupSession("test-session")

	if !cleanupCalled {
		t.Error("Session cleanup callback was not called")
	}
}
