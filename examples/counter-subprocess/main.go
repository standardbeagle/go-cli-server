// Package main demonstrates a stdio-based subprocess that maintains state.
//
// This example implements a simple counter service that:
// 1. Uses stdin/stdout for communication (no socket)
// 2. Maintains state (a counter) between commands
// 3. Handles INCREMENT, DECREMENT, GET, and RESET commands
//
// This type of subprocess is designed to be spawned by the hub using the
// "stdio" transport type. The hub will communicate with it via stdin/stdout.
//
// Usage (standalone testing):
//
//	echo "GET;;" | go run main.go
//	echo "INCREMENT;;" | go run main.go
//
// Registration config for the hub:
//
//	{
//	  "id": "counter",
//	  "name": "Counter Service",
//	  "commands": ["INCREMENT", "DECREMENT", "GET", "RESET"],
//	  "transport": {
//	    "type": "stdio",
//	    "command": "go",
//	    "args": ["run", "examples/counter-subprocess/main.go"]
//	  }
//	}
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"github.com/standardbeagle/go-cli-server/client"
	"github.com/standardbeagle/go-cli-server/protocol"
)

// counter is the global counter state
var counter atomic.Int64

func main() {
	// Redirect logs to stderr since stdout is used for protocol communication
	log.SetOutput(os.Stderr)

	server := client.NewSubprocessStdioServer()

	// Register command handlers
	server.RegisterHandler("INCREMENT", handleIncrement)
	server.RegisterHandler("DECREMENT", handleDecrement)
	server.RegisterHandler("GET", handleGet)
	server.RegisterHandler("RESET", handleReset)

	log.Println("Counter subprocess started, reading from stdin...")

	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Counter subprocess exiting")
}

// CounterResponse is the response structure for counter commands.
type CounterResponse struct {
	Value   int64  `json:"value"`
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

// handleIncrement increments the counter and returns the new value.
func handleIncrement(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	// Check for optional increment amount in args
	amount := int64(1)
	if len(cmd.Args) > 0 {
		var n int64
		if _, err := fmt.Sscanf(cmd.Args[0], "%d", &n); err == nil {
			amount = n
		}
	}

	newValue := counter.Add(amount)

	resp := CounterResponse{
		Value:  newValue,
		Action: "increment",
	}

	return jsonResponse(resp)
}

// handleDecrement decrements the counter and returns the new value.
func handleDecrement(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	// Check for optional decrement amount in args
	amount := int64(1)
	if len(cmd.Args) > 0 {
		var n int64
		if _, err := fmt.Sscanf(cmd.Args[0], "%d", &n); err == nil {
			amount = n
		}
	}

	newValue := counter.Add(-amount)

	resp := CounterResponse{
		Value:  newValue,
		Action: "decrement",
	}

	return jsonResponse(resp)
}

// handleGet returns the current counter value.
func handleGet(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	resp := CounterResponse{
		Value:  counter.Load(),
		Action: "get",
	}

	return jsonResponse(resp)
}

// handleReset resets the counter to zero and returns the old value.
func handleReset(ctx context.Context, cmd *protocol.Command) *protocol.Response {
	oldValue := counter.Swap(0)

	resp := CounterResponse{
		Value:   0,
		Action:  "reset",
		Message: fmt.Sprintf("Reset from %d", oldValue),
	}

	return jsonResponse(resp)
}

// jsonResponse creates a JSON response from a value.
func jsonResponse(v interface{}) *protocol.Response {
	data, err := json.Marshal(v)
	if err != nil {
		return client.ErrResponse(protocol.ErrInternal, fmt.Sprintf("failed to marshal response: %v", err))
	}

	return &protocol.Response{
		Type: protocol.ResponseJSON,
		Data: data,
	}
}
