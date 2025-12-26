// Package main demonstrates how to create a subprocess that registers with the hub.
//
// This example implements a simple ECHO service that:
// 1. Starts a local server on a Unix socket
// 2. Registers itself with the hub
// 3. Handles ECHO commands by returning the command arguments
//
// Usage:
//
//	# Start the hub first
//	go-cli-server serve --socket /tmp/hub.sock
//
//	# Then run this example
//	go run main.go --hub /tmp/hub.sock
//
// The echo subprocess registers the command "ECHO *" with the hub.
// Clients can then send ECHO commands through the hub:
//
//	ECHO HELLO world;;
//
// And receive a JSON response with the echoed data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/standardbeagle/go-cli-server/client"
	"github.com/standardbeagle/go-cli-server/protocol"
)

func main() {
	hubSocket := flag.String("hub", "", "Hub socket path (optional, skip registration if empty)")
	socketPath := flag.String("socket", "", "Socket path for this subprocess (default: auto-generated)")
	flag.Parse()

	// Generate socket path if not provided
	if *socketPath == "" {
		tmpDir := os.TempDir()
		*socketPath = filepath.Join(tmpDir, fmt.Sprintf("echo-subprocess-%d.sock", os.Getpid()))
	}

	// Create the subprocess server
	server := client.NewSubprocessServer(client.SubprocessServerConfig{
		ID: "echo",
		Transport: client.TransportConfig{
			Type:    "unix",
			Address: *socketPath,
		},
		OnConnect: func() {
			log.Println("Hub connected")
		},
		OnDisconnect: func(err error) {
			if err != nil {
				log.Printf("Hub disconnected with error: %v", err)
			} else {
				log.Println("Hub disconnected")
			}
		},
	})

	// Register the ECHO command handler
	server.RegisterHandler("ECHO", handleEcho)

	// Register INFO command to provide subprocess information
	server.RegisterHandler("INFO", handleInfo)

	// Register STATUS command for health checks
	server.RegisterHandler("STATUS", handleStatus)

	// Start the server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	log.Printf("Echo subprocess listening on %s", server.Address())

	// Register with hub if provided
	if *hubSocket != "" {
		regConfig := protocol.SubprocessRegisterConfig{
			ID:          "echo",
			Name:        "Echo Service",
			Description: "A simple echo service that returns command arguments",
			Commands:    []string{"ECHO *", "INFO", "STATUS"},
			Transport: protocol.SubprocessTransport{
				Type:    "unix",
				Address: *socketPath,
			},
			HealthCheck: protocol.SubprocessHealthCheck{
				Enabled:          true,
				IntervalMs:       10000,
				TimeoutMs:        5000,
				FailureThreshold: 3,
			},
			AutoRestart: true,
			MaxRestarts: 5,
		}

		if err := client.RegisterWithHub(*hubSocket, regConfig); err != nil {
			log.Fatalf("Failed to register with hub: %v", err)
		}
		log.Printf("Registered with hub at %s", *hubSocket)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5)
	defer cancel()

	if err := server.Stop(ctx); err != nil {
		log.Printf("Error stopping server: %v", err)
	}

	log.Println("Shutdown complete")
}

// EchoResponse is the response structure for ECHO commands.
type EchoResponse struct {
	Verb    string   `json:"verb"`
	SubVerb string   `json:"subverb,omitempty"`
	Args    []string `json:"args,omitempty"`
	Message string   `json:"message"`
}

// handleEcho handles ECHO commands by returning the command data.
func handleEcho(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	resp := EchoResponse{
		Verb:    cmd.Verb,
		SubVerb: cmd.SubVerb,
		Args:    cmd.Args,
		Message: "Echo from subprocess!",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return client.ErrResponse(protocol.ErrInternal, fmt.Sprintf("failed to marshal response: %v", err))
	}

	return &protocol.Response{
		Type: protocol.ResponseJSON,
		Data: data,
	}
}

// InfoResponse is the response structure for INFO commands.
type InfoResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Commands    []string `json:"commands"`
	PID         int      `json:"pid"`
}

// handleInfo handles INFO commands by returning subprocess information.
func handleInfo(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	resp := InfoResponse{
		ID:          "echo",
		Name:        "Echo Service",
		Description: "A simple echo service that returns command arguments",
		Commands:    []string{"ECHO *", "INFO", "STATUS"},
		PID:         os.Getpid(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return client.ErrResponse(protocol.ErrInternal, fmt.Sprintf("failed to marshal response: %v", err))
	}

	return &protocol.Response{
		Type: protocol.ResponseJSON,
		Data: data,
	}
}

// StatusResponse is the response structure for STATUS commands.
type StatusResponse struct {
	Status  string `json:"status"`
	Healthy bool   `json:"healthy"`
}

// handleStatus handles STATUS commands for health checks.
func handleStatus(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	resp := StatusResponse{
		Status:  "running",
		Healthy: true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return client.ErrResponse(protocol.ErrInternal, fmt.Sprintf("failed to marshal response: %v", err))
	}

	return &protocol.Response{
		Type: protocol.ResponseJSON,
		Data: data,
	}
}
