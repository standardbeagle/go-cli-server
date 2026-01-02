# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A reusable Go module for building CLI tools with persistent daemon backends. Features lock-free concurrency, subprocess management with command routing, and Unix socket IPC. Minimal dependencies (only `golang.org/x/sys`).

## Build Commands

```bash
# Build binaries
go build -o go-cli-hub ./cmd/hub
go build -o hubctl ./cmd/hubctl
go build -o echo-subprocess ./examples/echo-subprocess

# Run tests
go test ./...                            # All tests
go test ./hub -v                         # Single package with verbose
go test ./hub -run Integration -v        # Specific test pattern
go test -cover ./...                     # With coverage report

# Lint (if golangci-lint installed)
golangci-lint run ./...
```

## Architecture

### Package Structure

- **protocol/** - Text-based IPC protocol with explicit terminators (`;;`). Command format: `VERB [SUBVERB] [ARGS...] [-- LENGTH\nBASE64DATA];;`
- **hub/** - Main orchestrator with subprocess registry and command router
- **client/** - Client library with request builder, auto-start, and resilient connection
- **process/** - Process lifecycle with state machine, PID tracking, ring buffers for output
- **socket/** - Unix domain socket (or named pipes on Windows)
- **scheduler/** - Task scheduling with persistence

### Command Routing

Three-level priority:
1. Exact match: `"PROXY START"` → subprocess ID
2. Prefix match: `"PROXY"` → subprocess ID
3. Built-in handlers: PING, INFO, PROC, RUN, SUBPROCESS

### Lock-Free Design

Core data structures use `sync.Map` and `atomic` operations:
- `sync.Map` for subprocess registry, client tracking, process manager
- `atomic.Value` for state machines with CAS transitions
- `atomic.Int64` for counters and metrics

### Process State Machine

```
Pending → Started → Running → Exited
    ↓
  Failed (at any step)
```

State transitions use `CompareAndSwapState(old, new)` for thread safety.

### Subprocess Registration

Subprocesses register via `SUBPROCESS REGISTER` with JSON config specifying:
- ID, name, and command patterns (supports wildcards like `"PROXY *"`)
- Transport type: unix socket, TCP, or stdio
- Health check configuration
- Auto-restart behavior

## Key Patterns

### Request Builder (client/conn.go)

```go
result, err := conn.Request("PROC", "LIST").WithJSON(filter).JSON()
err := conn.Request("PROC", "STOP", processID).OK()
```

### Command Handler (hub/handlers.go)

```go
func handleCommand(ctx context.Context, conn *hub.Connection, cmd *protocol.Command) error {
    switch cmd.SubVerb {
    case "STATUS":
        return conn.WriteJSON(statusData)
    default:
        return conn.WriteErr(protocol.ErrInvalidAction, "unknown action")
    }
}
```

### Creating a Subprocess

See `examples/echo-subprocess/main.go`:
1. Create `SubprocessServer` with socket config
2. Register command handlers
3. Call `client.RegisterWithHub()` to register with hub
4. Handle SIGTERM for graceful shutdown

## Protocol Format

Commands use explicit terminator (`;;`) for parsing resilience:

```
# Request
VERB [SUBVERB] [ARGS...] [-- LENGTH\nBASE64DATA];;

# Responses
OK [message];;
ERR CODE message;;
JSON -- LENGTH\nBASE64DATA;;
PONG;;
```

## Platform Support

- Unix: `socket/socket_unix.go`, `process/lifecycle_unix.go`
- Windows: `socket/socket_windows.go`, `process/lifecycle_windows.go`
- Build tags: `//go:build unix`, `//go:build windows`

## Testing Notes

- Integration tests in `hub/integration_test.go` test full subprocess registration flow
- Tests use `t.TempDir()` for temporary socket paths
- Context-based timeouts prevent test hangs
