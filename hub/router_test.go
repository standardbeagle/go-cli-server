package hub

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
	hubsocket "github.com/standardbeagle/go-cli-server/socket"
)

// pongServer is a minimal subprocess server that answers PING with PONG and
// anything else with OK, over the protocol wire format.
type pongServer struct {
	ln    net.Listener
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns []net.Conn
}

func startPongServer(t *testing.T, sockPath string) *pongServer {
	t.Helper()
	ln, err := hubsocket.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &pongServer{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, conn)
			s.mu.Unlock()
			go s.serve(conn)
		}
	}()
	return s
}

func (s *pongServer) serve(conn net.Conn) {
	parser := protocol.NewParser(conn)
	writer := protocol.NewWriter(conn)
	for {
		cmd, err := parser.ParseCommand()
		if err != nil {
			return
		}
		if cmd.Verb == protocol.VerbPing {
			_ = writer.WritePong()
		} else {
			_ = writer.WriteOK("ok")
		}
	}
}

func (s *pongServer) stop() {
	s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func TestSubprocessConn_CloseIdempotent(t *testing.T) {
	var calls atomic.Int32
	conn := &SubprocessConn{
		closer: func() error {
			calls.Add(1)
			return nil
		},
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("closer calls = %d, want 1", got)
	}
}

// TestManagedSubprocess_StopStartCycle verifies a subprocess can be started again
// after being stopped — the lifecycle context must be recreated rather than left
// cancelled (which previously left a "started" subprocess permanently dead).
func TestManagedSubprocess_StopStartCycle(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "pong.sock")
	srv := startPongServer(t, sockPath)
	defer srv.stop()

	sp := &ManagedSubprocess{
		ID:   "cycle",
		Name: "Cycle",
		Transport: SubprocessTransportConfig{
			Type:    "unix",
			Address: sockPath,
			Timeout: time.Second,
		},
		HealthCheck: SubprocessHealthConfig{Enabled: false},
	}
	sp.state.Store(SubprocessPending)
	sp.newLifecycle()

	ctx := context.Background()
	if err := sp.start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if got := sp.state.Load().(ManagedSubprocessState); got != SubprocessRunning {
		t.Fatalf("after start state = %s, want running", got)
	}

	if err := sp.stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := sp.state.Load().(ManagedSubprocessState); got != SubprocessStopped {
		t.Fatalf("after stop state = %s, want stopped", got)
	}

	// The bug: start after stop returned "already running" / left it dead.
	if err := sp.start(ctx); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	if got := sp.state.Load().(ManagedSubprocessState); got != SubprocessRunning {
		t.Fatalf("after restart state = %s, want running", got)
	}
	_ = sp.stop(ctx)
}

// TestManagedSubprocess_AutoRestart verifies triggerAutoRestart from a Running
// state actually reconnects instead of permanently bricking the subprocess.
func TestManagedSubprocess_AutoRestart(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "pong.sock")
	srv := startPongServer(t, sockPath)
	defer srv.stop()

	sp := &ManagedSubprocess{
		ID:   "restart",
		Name: "Restart",
		Transport: SubprocessTransportConfig{
			Type:    "unix",
			Address: sockPath,
			Timeout: time.Second,
		},
		HealthCheck: SubprocessHealthConfig{Enabled: false},
		AutoRestart: true,
		RestartWait: 10 * time.Millisecond,
	}
	sp.state.Store(SubprocessPending)
	sp.newLifecycle()

	if err := sp.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Simulate the health path deciding the subprocess is unhealthy.
	sp.triggerAutoRestart()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sp.restartCount.Load() == 1 &&
			sp.state.Load().(ManagedSubprocessState) == SubprocessRunning {
			_ = sp.stop(context.Background())
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subprocess did not recover: restartCount=%d state=%s",
		sp.restartCount.Load(), sp.state.Load().(ManagedSubprocessState))
}

func TestSubprocessRouter_Register(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
		Commands: []string{
			"TEST *",
			"EXAMPLE GET",
		},
	}

	// Test successful registration
	err := router.Register(sp)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test duplicate registration
	err = router.Register(sp)
	if err == nil {
		t.Error("Expected error for duplicate registration, got nil")
	}

	// Verify subprocess can be retrieved
	retrieved, ok := router.Get("test-subprocess")
	if !ok {
		t.Error("Failed to retrieve registered subprocess")
	}
	if retrieved.ID != "test-subprocess" {
		t.Errorf("Retrieved subprocess ID = %s, want test-subprocess", retrieved.ID)
	}
}

func TestSubprocessRouter_RegisterEmptyID(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		Name: "No ID",
	}

	err := router.Register(sp)
	if err == nil {
		t.Error("Expected error for empty ID, got nil")
	}
}

func TestSubprocessRouter_Unregister(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
	}

	_ = router.Register(sp)

	// Test successful unregistration
	err := router.Unregister("test-subprocess")
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	// Verify subprocess is removed
	_, ok := router.Get("test-subprocess")
	if ok {
		t.Error("Subprocess still exists after unregistration")
	}

	// Test unregistering non-existent subprocess
	err = router.Unregister("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent subprocess, got nil")
	}
}

func TestSubprocessRouter_List(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	// Empty list
	list := router.List()
	if len(list) != 0 {
		t.Errorf("Expected empty list, got %d items", len(list))
	}

	// Add subprocesses
	sp1 := &ManagedSubprocess{ID: "sp1", Name: "Subprocess 1"}
	sp2 := &ManagedSubprocess{ID: "sp2", Name: "Subprocess 2"}

	_ = router.Register(sp1)
	_ = router.Register(sp2)

	list = router.List()
	if len(list) != 2 {
		t.Errorf("Expected 2 subprocesses, got %d", len(list))
	}
}

func TestSubprocessRouter_GetRoutes(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test",
		Commands: []string{
			"PROXY *",
			"EXPORT GET",
			"EXACT MATCH",
		},
	}

	_ = router.Register(sp)

	routes := router.GetRoutes()

	// Check prefix route
	if _, ok := routes["PROXY *"]; !ok {
		t.Error("Missing PROXY * prefix route")
	}

	// Check exact routes
	if _, ok := routes["EXPORT GET"]; !ok {
		t.Error("Missing EXPORT GET exact route")
	}
	if _, ok := routes["EXACT MATCH"]; !ok {
		t.Error("Missing EXACT MATCH exact route")
	}
}

func TestSubprocessRouter_RejectsHubVerbCollision(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	err := router.Register(&ManagedSubprocess{
		ID:       "shadow",
		Name:     "Shadow",
		Commands: []string{"SESSION EXPORT"},
	})
	if err == nil {
		t.Fatal("expected hub verb collision error")
	}
}

func TestSubprocessRouter_MultiWordWildcardRegistersSubVerb(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	err := router.Register(&ManagedSubprocess{
		ID:       "multi",
		Name:     "Multi",
		Commands: []string{"FOO BAR *"},
	})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	parser := protocol.NewParserWithRegistry(strings.NewReader("FOO BAR baz;;"), hub.protocolRegistry)
	cmd, err := parser.ParseCommand()
	if err != nil {
		t.Fatalf("ParseCommand error: %v", err)
	}
	if cmd.SubVerb != "BAR" {
		t.Fatalf("SubVerb = %q, want BAR", cmd.SubVerb)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "baz" {
		t.Fatalf("Args = %#v, want [baz]", cmd.Args)
	}
}

func TestSubprocessRouter_Stats(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
	}
	sp.state.Store(SubprocessPending)

	_ = router.Register(sp)

	stats := router.Stats()

	if stats.Total != 1 {
		t.Errorf("Total = %d, want 1", stats.Total)
	}
	if stats.Running != 0 {
		t.Errorf("Running = %d, want 0", stats.Running)
	}
	if len(stats.Subprocesses) != 1 {
		t.Errorf("Subprocesses count = %d, want 1", len(stats.Subprocesses))
	}
}

func TestManagedSubprocess_StateTransitions(t *testing.T) {
	sp := &ManagedSubprocess{
		ID:   "test",
		Name: "Test",
		Transport: SubprocessTransportConfig{
			Type:    "tcp",
			Address: "localhost:0", // Invalid, will fail to connect
		},
	}
	sp.state.Store(SubprocessPending)
	sp.newLifecycle()

	// Start should fail with invalid transport
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := sp.start(ctx)
	if err == nil {
		t.Error("Expected start to fail with invalid address")
	}

	// State should be failed
	state := sp.state.Load().(ManagedSubprocessState)
	if state != SubprocessFailed {
		t.Errorf("State = %s, want %s", state, SubprocessFailed)
	}
}

func TestSubprocessTransportConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  SubprocessTransportConfig
		wantErr bool
	}{
		{
			name: "unix transport requires address",
			config: SubprocessTransportConfig{
				Type: "unix",
			},
			wantErr: true,
		},
		{
			name: "tcp transport requires address",
			config: SubprocessTransportConfig{
				Type: "tcp",
			},
			wantErr: true,
		},
		{
			name: "stdio transport requires command",
			config: SubprocessTransportConfig{
				Type: "stdio",
			},
			wantErr: true,
		},
		{
			name: "unsupported transport type",
			config: SubprocessTransportConfig{
				Type: "unknown",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &ManagedSubprocess{
				ID:        "test",
				Name:      "Test",
				Transport: tt.config,
			}
			sp.state.Store(SubprocessPending)
			sp.newLifecycle()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := sp.start(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("start() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultSubprocessHealthConfig(t *testing.T) {
	config := DefaultSubprocessHealthConfig()

	if !config.Enabled {
		t.Error("Expected Enabled = true by default")
	}
	if config.Interval != 10*time.Second {
		t.Errorf("Interval = %v, want 10s", config.Interval)
	}
	if config.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", config.Timeout)
	}
	if config.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", config.FailureThreshold)
	}
}

// TestSubprocessRouter_ConcurrentRegisterRoute exercises the previously-racy
// path: rebuildRoutes reassigning route tables while routeToSubprocess reads
// them. Run with -race; the atomic routeTable swap must make this clean.
func TestSubprocessRouter_ConcurrentRegisterRoute(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	var wg sync.WaitGroup

	// Writers: continuously register/unregister subprocesses (drives rebuildRoutes).
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := "sp-" + string(rune('a'+w))
				sp := &ManagedSubprocess{
					ID:       id,
					Commands: []string{"PROXY *", "EXPORT GET"},
				}
				_ = router.Register(sp)
				_ = router.Unregister(id)
			}
		}(w)
	}

	// Readers: continuously route commands (reads the route table lock-free).
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				router.routes.Load()
				_ = router.GetRoutes()
			}
		}()
	}

	wg.Wait()
}
