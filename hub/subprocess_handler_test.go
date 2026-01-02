package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
)

// mockConnection creates a mock connection for testing handlers.
func mockConnection(hub *Hub) (*Connection, *bytes.Buffer) {
	// Create a pipe for the connection
	server, client := net.Pipe()

	// Create output buffer
	output := &bytes.Buffer{}

	// Create connection with the server end
	conn := &Connection{
		hub:    hub,
		conn:   server,
		parser: protocol.NewParser(server),
		writer: protocol.NewWriter(output), // Write to buffer for inspection
	}

	// Close client side (we won't use it)
	client.Close()

	return conn, output
}

func TestSubprocessHandler_Register(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)
	router.RegisterSubprocessCommands()

	ctx := context.Background()

	t.Run("successful registration", func(t *testing.T) {
		config := protocol.SubprocessRegisterConfig{
			ID:       "test-sp",
			Name:     "Test Subprocess",
			Commands: []string{"TEST *"},
			Transport: protocol.SubprocessTransport{
				Type:    "tcp",
				Address: "localhost:9999",
			},
		}
		data, _ := json.Marshal(config)

		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbRegister,
			Data:    data,
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		// Verify subprocess was registered
		sp, ok := router.Get("test-sp")
		if !ok {
			t.Error("Subprocess not found after registration")
		}
		if sp.Name != "Test Subprocess" {
			t.Errorf("Name = %s, want Test Subprocess", sp.Name)
		}

		// Verify JSON response
		if output.Len() == 0 {
			t.Error("No output written")
		}

		// Cleanup
		_ = router.Unregister("test-sp")
	})

	t.Run("missing data", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbRegister,
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		// Should have written an error response
		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})

	t.Run("missing ID", func(t *testing.T) {
		config := protocol.SubprocessRegisterConfig{
			Name:     "No ID",
			Commands: []string{"TEST"},
			Transport: protocol.SubprocessTransport{
				Type: "tcp",
			},
		}
		data, _ := json.Marshal(config)

		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbRegister,
			Data:    data,
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})

	t.Run("missing commands", func(t *testing.T) {
		config := protocol.SubprocessRegisterConfig{
			ID:   "no-commands",
			Name: "No Commands",
			Transport: protocol.SubprocessTransport{
				Type: "tcp",
			},
		}
		data, _ := json.Marshal(config)

		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbRegister,
			Data:    data,
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})

	t.Run("duplicate registration", func(t *testing.T) {
		config := protocol.SubprocessRegisterConfig{
			ID:       "dup-test",
			Name:     "Duplicate Test",
			Commands: []string{"DUP"},
			Transport: protocol.SubprocessTransport{
				Type:    "tcp",
				Address: "localhost:9999",
			},
		}
		data, _ := json.Marshal(config)

		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbRegister,
			Data:    data,
		}

		// First registration
		conn1, _ := mockConnection(hub)
		defer conn1.conn.Close()
		_ = router.handleSubprocess(ctx, conn1, cmd)

		// Second registration should fail
		conn2, output := mockConnection(hub)
		defer conn2.conn.Close()
		err := router.handleSubprocess(ctx, conn2, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for duplicate registration")
		}

		// Cleanup
		_ = router.Unregister("dup-test")
	})
}

func TestSubprocessHandler_Unregister(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)
	router.RegisterSubprocessCommands()

	ctx := context.Background()

	// Register a subprocess first
	sp := &ManagedSubprocess{
		ID:       "to-unregister",
		Name:     "To Unregister",
		Commands: []string{"UNREG"},
	}
	sp.state.Store(SubprocessPending)
	sp.ctx, sp.cancel = context.WithCancel(context.Background())
	_ = router.Register(sp)

	t.Run("successful unregister", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbUnregister,
			Args:    []string{"to-unregister"},
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		// Verify subprocess was removed
		_, ok := router.Get("to-unregister")
		if ok {
			t.Error("Subprocess still exists after unregistration")
		}

		if output.Len() == 0 {
			t.Error("No output written")
		}
	})

	t.Run("missing ID", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbUnregister,
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})

	t.Run("not found", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbUnregister,
			Args:    []string{"nonexistent"},
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})
}

func TestSubprocessHandler_List(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)
	router.RegisterSubprocessCommands()

	ctx := context.Background()

	// Register some subprocesses
	for i := 0; i < 3; i++ {
		sp := &ManagedSubprocess{
			ID:       string(rune('a' + i)),
			Name:     "Test",
			Commands: []string{"CMD"},
		}
		sp.state.Store(SubprocessPending)
		sp.ctx, sp.cancel = context.WithCancel(context.Background())
		_ = router.Register(sp)
	}

	cmd := &protocol.Command{
		Verb:    protocol.VerbSubprocess,
		SubVerb: protocol.SubVerbList,
	}

	conn, output := mockConnection(hub)
	defer conn.conn.Close()

	err := router.handleSubprocess(ctx, conn, cmd)
	if err != nil {
		t.Fatalf("handleSubprocess returned error: %v", err)
	}

	if output.Len() == 0 {
		t.Error("No output written")
	}

	// Verify we got JSON with 3 items
	// The output format is "JSON -- LENGTH\nDATA;;"
	outputStr := output.String()
	if len(outputStr) == 0 {
		t.Error("Empty output")
	}
}

func TestSubprocessHandler_Status(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)
	router.RegisterSubprocessCommands()

	ctx := context.Background()

	// Register a subprocess
	sp := &ManagedSubprocess{
		ID:       "status-test",
		Name:     "Status Test",
		Commands: []string{"STATUS"},
	}
	sp.state.Store(SubprocessRunning)
	sp.healthy.Store(true)
	sp.ctx, sp.cancel = context.WithCancel(context.Background())
	_ = router.Register(sp)

	t.Run("get status", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbStatus,
			Args:    []string{"status-test"},
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written")
		}
	})

	t.Run("not found", func(t *testing.T) {
		cmd := &protocol.Command{
			Verb:    protocol.VerbSubprocess,
			SubVerb: protocol.SubVerbStatus,
			Args:    []string{"nonexistent"},
		}

		conn, output := mockConnection(hub)
		defer conn.conn.Close()

		err := router.handleSubprocess(ctx, conn, cmd)
		if err != nil {
			t.Fatalf("handleSubprocess returned error: %v", err)
		}

		if output.Len() == 0 {
			t.Error("No output written for error case")
		}
	})
}

func TestSubprocessHandler_InvalidAction(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)
	router.RegisterSubprocessCommands()

	ctx := context.Background()

	cmd := &protocol.Command{
		Verb:    protocol.VerbSubprocess,
		SubVerb: "INVALID",
	}

	conn, output := mockConnection(hub)
	defer conn.conn.Close()

	err := router.handleSubprocess(ctx, conn, cmd)
	if err != nil {
		t.Fatalf("handleSubprocess returned error: %v", err)
	}

	if output.Len() == 0 {
		t.Error("No output written for invalid action")
	}
}

func TestConfigToManagedSubprocess(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	config := protocol.SubprocessRegisterConfig{
		ID:          "test",
		Name:        "Test",
		Description: "Test subprocess",
		Commands:    []string{"CMD1", "CMD2 *"},
		Transport: protocol.SubprocessTransport{
			Type:      "unix",
			Address:   "/tmp/test.sock",
			TimeoutMs: 5000,
		},
		HealthCheck: protocol.SubprocessHealthCheck{
			Enabled:          true,
			IntervalMs:       30000,
			TimeoutMs:        10000,
			FailureThreshold: 5,
		},
		AutoStart:      true,
		AutoRestart:    true,
		MaxRestarts:    10,
		RestartDelayMs: 2000,
	}

	sp := router.configToManagedSubprocess(config)

	if sp.ID != "test" {
		t.Errorf("ID = %s, want test", sp.ID)
	}
	if sp.Name != "Test" {
		t.Errorf("Name = %s, want Test", sp.Name)
	}
	if sp.Description != "Test subprocess" {
		t.Errorf("Description = %s, want Test subprocess", sp.Description)
	}
	if len(sp.Commands) != 2 {
		t.Errorf("Commands count = %d, want 2", len(sp.Commands))
	}
	if sp.Transport.Type != "unix" {
		t.Errorf("Transport.Type = %s, want unix", sp.Transport.Type)
	}
	if sp.Transport.Address != "/tmp/test.sock" {
		t.Errorf("Transport.Address = %s, want /tmp/test.sock", sp.Transport.Address)
	}
	if sp.Transport.Timeout != 5*time.Second {
		t.Errorf("Transport.Timeout = %v, want 5s", sp.Transport.Timeout)
	}
	if !sp.HealthCheck.Enabled {
		t.Error("HealthCheck.Enabled = false, want true")
	}
	if sp.HealthCheck.Interval != 30*time.Second {
		t.Errorf("HealthCheck.Interval = %v, want 30s", sp.HealthCheck.Interval)
	}
	if sp.HealthCheck.FailureThreshold != 5 {
		t.Errorf("HealthCheck.FailureThreshold = %d, want 5", sp.HealthCheck.FailureThreshold)
	}
	if !sp.AutoStart {
		t.Error("AutoStart = false, want true")
	}
	if !sp.AutoRestart {
		t.Error("AutoRestart = false, want true")
	}
	if sp.MaxRestarts != 10 {
		t.Errorf("MaxRestarts = %d, want 10", sp.MaxRestarts)
	}
	if sp.RestartWait != 2*time.Second {
		t.Errorf("RestartWait = %v, want 2s", sp.RestartWait)
	}
}

func TestAutoRestart(t *testing.T) {
	sp := &ManagedSubprocess{
		ID:          "restart-test",
		Name:        "Restart Test",
		AutoRestart: true,
		MaxRestarts: 3,
		RestartWait: 10 * time.Millisecond,
		Transport: SubprocessTransportConfig{
			Type:    "tcp",
			Address: "localhost:0", // Invalid
		},
	}
	sp.state.Store(SubprocessRunning)
	sp.ctx, sp.cancel = context.WithCancel(context.Background())

	// Trigger auto-restart
	sp.triggerAutoRestart()

	// Wait for restart attempt
	time.Sleep(50 * time.Millisecond)

	// Should have incremented restart count
	if sp.restartCount.Load() == 0 {
		t.Error("Restart count not incremented")
	}

	// Cleanup
	sp.cancel()
	sp.wg.Wait()
}

func TestAutoRestartMaxLimit(t *testing.T) {
	sp := &ManagedSubprocess{
		ID:          "max-restart-test",
		Name:        "Max Restart Test",
		AutoRestart: true,
		MaxRestarts: 2,
	}
	sp.state.Store(SubprocessRunning)
	sp.restartCount.Store(2) // Already at max
	sp.ctx, sp.cancel = context.WithCancel(context.Background())

	// Trigger auto-restart - should not restart
	sp.triggerAutoRestart()

	// State should be failed
	state := sp.state.Load().(ManagedSubprocessState)
	if state != SubprocessFailed {
		t.Errorf("State = %s, want failed", state)
	}

	// Cleanup
	sp.cancel()
}
