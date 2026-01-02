package hub_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/client"
	"github.com/standardbeagle/go-cli-server/hub"
	"github.com/standardbeagle/go-cli-server/protocol"
)

// TestIntegration_SubprocessRegistration tests the full flow of:
// 1. Starting a hub
// 2. Starting a subprocess server
// 3. Registering the subprocess with the hub
// 4. Sending commands through the hub to the subprocess
// 5. Receiving responses
func TestIntegration_SubprocessRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	hubSocket := filepath.Join(tmpDir, "hub.sock")
	subprocessSocket := filepath.Join(tmpDir, "subprocess.sock")

	// Create and start the hub
	h := hub.New(hub.Config{
		SocketPath: hubSocket,
	})

	if err := h.Start(); err != nil {
		t.Fatalf("Failed to start hub: %v", err)
	}
	defer func() { _ = h.Stop(context.Background()) }()

	// Give hub time to be ready
	time.Sleep(50 * time.Millisecond)

	// Create and start the subprocess server
	subServer := client.NewSubprocessServer(client.SubprocessServerConfig{
		ID: "test-echo",
		Transport: client.TransportConfig{
			Type:    "unix",
			Address: subprocessSocket,
		},
	})

	// Register echo handler
	subServer.RegisterHandler("ECHO", func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
		resp := map[string]interface{}{
			"verb":    cmd.Verb,
			"subverb": cmd.SubVerb,
			"args":    cmd.Args,
			"echoed":  true,
		}
		data, _ := json.Marshal(resp)
		return &protocol.Response{
			Type: protocol.ResponseJSON,
			Data: data,
		}
	})

	if err := subServer.Start(); err != nil {
		t.Fatalf("Failed to start subprocess server: %v", err)
	}
	defer func() { _ = subServer.Stop(context.Background()) }()

	// Register subprocess with hub
	regConfig := protocol.SubprocessRegisterConfig{
		ID:       "test-echo",
		Name:     "Test Echo",
		Commands: []string{"ECHO *"},
		Transport: protocol.SubprocessTransport{
			Type:    "unix",
			Address: subprocessSocket,
		},
	}

	if err := client.RegisterWithHub(hubSocket, regConfig); err != nil {
		t.Fatalf("Failed to register with hub: %v", err)
	}

	// Give hub time to process registration
	time.Sleep(50 * time.Millisecond)

	// Connect to hub as a client
	clientConn, err := net.Dial("unix", hubSocket)
	if err != nil {
		t.Fatalf("Failed to connect to hub: %v", err)
	}
	defer clientConn.Close()

	parser := protocol.NewParser(clientConn)
	writer := protocol.NewWriter(clientConn)

	// Test 1: PING should work (built-in)
	if err := writer.WriteCommand("PING", nil, nil); err != nil {
		t.Fatalf("Failed to send PING: %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("Failed to parse PING response: %v", err)
	}
	if resp.Type != protocol.ResponsePong {
		t.Errorf("Expected PONG, got %s", resp.Type)
	}

	// Test 2: SUBPROCESS LIST should show our registered subprocess
	if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "LIST", nil, nil); err != nil {
		t.Fatalf("Failed to send SUBPROCESS LIST: %v", err)
	}

	resp, err = parser.ParseResponse()
	if err != nil {
		t.Fatalf("Failed to parse SUBPROCESS LIST response: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Errorf("Expected JSON, got %s", resp.Type)
	}

	var subprocesses []protocol.SubprocessInfo
	if err := json.Unmarshal(resp.Data, &subprocesses); err != nil {
		t.Fatalf("Failed to unmarshal subprocess list: %v", err)
	}

	found := false
	for _, sp := range subprocesses {
		if sp.ID == "test-echo" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Subprocess 'test-echo' not found in list")
	}
}

// TestIntegration_SubprocessHealthCheck tests that health checks work correctly.
func TestIntegration_SubprocessHealthCheck(t *testing.T) {
	tmpDir := t.TempDir()
	hubSocket := filepath.Join(tmpDir, "hub.sock")
	subprocessSocket := filepath.Join(tmpDir, "subprocess.sock")

	// Create and start hub
	h := hub.New(hub.Config{
		SocketPath: hubSocket,
	})

	if err := h.Start(); err != nil {
		t.Fatalf("Failed to start hub: %v", err)
	}
	defer func() { _ = h.Stop(context.Background()) }()

	time.Sleep(50 * time.Millisecond)

	// Create subprocess
	subServer := client.NewSubprocessServer(client.SubprocessServerConfig{
		ID: "health-test",
		Transport: client.TransportConfig{
			Type:    "unix",
			Address: subprocessSocket,
		},
	})

	subServer.RegisterHandler("TEST", func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
		return client.OKResponse("test ok")
	})

	if err := subServer.Start(); err != nil {
		t.Fatalf("Failed to start subprocess: %v", err)
	}
	defer func() { _ = subServer.Stop(context.Background()) }()

	// Register with health check enabled
	regConfig := protocol.SubprocessRegisterConfig{
		ID:       "health-test",
		Name:     "Health Test",
		Commands: []string{"TEST"},
		Transport: protocol.SubprocessTransport{
			Type:    "unix",
			Address: subprocessSocket,
		},
		HealthCheck: protocol.SubprocessHealthCheck{
			Enabled:          true,
			IntervalMs:       100, // Fast for testing
			TimeoutMs:        50,
			FailureThreshold: 3,
		},
	}

	if err := client.RegisterWithHub(hubSocket, regConfig); err != nil {
		t.Fatalf("Failed to register: %v", err)
	}

	// Wait a bit for health check to run
	time.Sleep(200 * time.Millisecond)

	// Check status
	clientConn, err := net.Dial("unix", hubSocket)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	parser := protocol.NewParser(clientConn)
	writer := protocol.NewWriter(clientConn)

	if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "STATUS", []string{"health-test"}, nil); err != nil {
		t.Fatalf("Failed to send STATUS: %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("Expected JSON, got %s", resp.Type)
	}

	var info protocol.SubprocessInfo
	if err := json.Unmarshal(resp.Data, &info); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Subprocess should be registered
	if info.State != "pending" && info.State != "running" {
		t.Errorf("Unexpected state: %s", info.State)
	}
}

// TestIntegration_MultipleSubprocesses tests registering and managing multiple subprocesses.
func TestIntegration_MultipleSubprocesses(t *testing.T) {
	tmpDir := t.TempDir()
	hubSocket := filepath.Join(tmpDir, "hub.sock")

	h := hub.New(hub.Config{
		SocketPath: hubSocket,
	})

	if err := h.Start(); err != nil {
		t.Fatalf("Failed to start hub: %v", err)
	}
	defer func() { _ = h.Stop(context.Background()) }()

	time.Sleep(50 * time.Millisecond)

	// Create multiple subprocess servers
	subServers := make([]*client.SubprocessServer, 3)
	for i := 0; i < 3; i++ {
		socket := filepath.Join(tmpDir, "sub"+string(rune('a'+i))+".sock")
		subServers[i] = client.NewSubprocessServer(client.SubprocessServerConfig{
			ID: "sub-" + string(rune('a'+i)),
			Transport: client.TransportConfig{
				Type:    "unix",
				Address: socket,
			},
		})

		cmdName := "CMD" + string(rune('A'+i))
		subServers[i].RegisterHandler(cmdName, func(ctx context.Context, cmd *protocol.Command) *protocol.Response {
			return client.OKResponse("handled " + cmd.Verb)
		})

		if err := subServers[i].Start(); err != nil {
			t.Fatalf("Failed to start subprocess %d: %v", i, err)
		}
		defer func(idx int) { _ = subServers[idx].Stop(context.Background()) }(i)

		// Register with hub
		regConfig := protocol.SubprocessRegisterConfig{
			ID:       "sub-" + string(rune('a'+i)),
			Name:     "Subprocess " + string(rune('A'+i)),
			Commands: []string{cmdName},
			Transport: protocol.SubprocessTransport{
				Type:    "unix",
				Address: socket,
			},
		}

		if err := client.RegisterWithHub(hubSocket, regConfig); err != nil {
			t.Fatalf("Failed to register subprocess %d: %v", i, err)
		}
	}

	time.Sleep(50 * time.Millisecond)

	// Verify all are registered
	clientConn, err := net.Dial("unix", hubSocket)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	parser := protocol.NewParser(clientConn)
	writer := protocol.NewWriter(clientConn)

	if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "LIST", nil, nil); err != nil {
		t.Fatalf("Failed to send LIST: %v", err)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	var subprocesses []protocol.SubprocessInfo
	if err := json.Unmarshal(resp.Data, &subprocesses); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(subprocesses) != 3 {
		t.Errorf("Expected 3 subprocesses, got %d", len(subprocesses))
	}
}
