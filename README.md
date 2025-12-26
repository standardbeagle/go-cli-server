# go-cli-server

A reusable Go module for building CLI tools with persistent daemon backends. Extracted from [agnt](https://github.com/standardbeagle/agnt) to enable sharing between multiple projects.

## Features

- **Lock-free design**: Uses `sync.Map` and `atomic` operations for all core data structures
- **Process management**: Start, stop, monitor, and communicate with subprocesses
- **PID tracking**: Track process IDs across daemon restarts for orphan cleanup
- **Command dispatch**: Extensible command registry with verb/sub-verb pattern
- **Message relay**: Inter-process communication through the hub
- **Unix socket IPC**: Persistent daemon with auto-start capability
- **Resilient client**: Auto-reconnection, heartbeat monitoring, and version checking
- **Task scheduler**: Schedule and deliver messages with persistence

## Packages

```
go-cli-server/
├── protocol/    # IPC protocol: commands, responses, parser, writer
├── process/     # Process lifecycle management, output capture, PID tracking
├── socket/      # Unix domain socket management
├── hub/         # Main hub orchestrator and command registry
├── client/      # Client library with request builder, auto-start, resilience
└── scheduler/   # Task scheduling with persistence
```

## Installation

```bash
go get github.com/standardbeagle/go-cli-server
```

## Quick Start

### Creating a Hub Server

```go
package main

import (
    "context"
    "log"

    "github.com/standardbeagle/go-cli-server/hub"
    "github.com/standardbeagle/go-cli-server/protocol"
)

func main() {
    // Create hub with default config
    h := hub.New(hub.DefaultConfig())

    // Register custom command
    h.RegisterCommand(hub.CommandDefinition{
        Verb:     "INDEX",
        SubVerbs: []string{"START", "STATUS", "STOP"},
        Handler:  handleIndex,
    })

    // Start the hub
    if err := h.Start(); err != nil {
        log.Fatal(err)
    }

    log.Printf("Hub listening on %s", h.SocketPath())
    h.Wait()
}

func handleIndex(ctx context.Context, conn *hub.Connection, cmd *protocol.Command) error {
    switch cmd.SubVerb {
    case "START":
        // Start indexing...
        return conn.WriteOK("indexing started")
    case "STATUS":
        // Return status...
        return conn.WriteJSON([]byte(`{"status": "running"}`))
    default:
        return conn.WriteErr(protocol.ErrInvalidAction, "unknown action")
    }
}
```

### Using the Client

```go
package main

import (
    "log"

    "github.com/standardbeagle/go-cli-server/client"
)

func main() {
    // Create client connection
    conn := client.NewConn()
    defer conn.Close()

    // Ping the hub
    if err := conn.Ping(); err != nil {
        log.Fatal("Hub not running:", err)
    }

    // Request with JSON response
    result, err := conn.Request("INDEX", "STATUS").JSON()
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Status: %v", result["status"])

    // Request with OK response
    err = conn.Request("INDEX", "START").
        WithJSON(map[string]string{"path": "/project"}).
        OK()
    if err != nil {
        log.Fatal(err)
    }
}
```

### Auto-Starting the Hub

The client can automatically start the hub daemon if it's not running:

```go
package main

import (
    "log"

    "github.com/standardbeagle/go-cli-server/client"
)

func main() {
    // Configure auto-start
    config := client.DefaultAutoStartConfig("myapp")
    config.HubPath = "/usr/local/bin/myapp-daemon"
    config.HubArgs = []string{"daemon", "start"}

    // This will start the hub if needed
    conn, err := client.EnsureHubRunning(config)
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // Use the connection...
}
```

### Resilient Client with Auto-Reconnection

For long-running clients that need to survive hub restarts:

```go
package main

import (
    "log"

    "github.com/standardbeagle/go-cli-server/client"
)

func main() {
    config := client.DefaultResilientConfig("myapp")

    // Optional: version checking
    config.VersionCheck = client.MakeVersionCheck("1.0.0", nil)

    // Optional: callbacks
    config.OnDisconnect = func(err error) {
        log.Printf("Disconnected: %v", err)
    }
    config.OnReconnect = func(conn *client.Conn) error {
        log.Println("Reconnected!")
        return nil
    }

    rc := client.NewResilientConn(config)
    if err := rc.Connect(); err != nil {
        log.Fatal(err)
    }
    defer rc.Close()

    // Use WithConn for operations
    err := rc.WithConn(func(c *client.Conn) error {
        return c.Ping()
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

### Task Scheduling

Schedule messages for future delivery:

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/standardbeagle/go-cli-server/scheduler"
)

func main() {
    s, err := scheduler.New(scheduler.Config{
        TickInterval: 100 * time.Millisecond,
        DeliverFunc: func(ctx context.Context, targetID, payload string) error {
            log.Printf("Delivered to %s: %s", targetID, payload)
            return nil
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    if err := s.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
    defer s.Stop()

    // Schedule a task
    task, err := s.Schedule("user-1", 5*time.Second, "Hello!", "/project")
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Scheduled task %s", task.ID)
}
```

### PID Tracking for Orphan Cleanup

Track process IDs across daemon restarts:

```go
package main

import (
    "log"
    "os"

    "github.com/standardbeagle/go-cli-server/process"
)

func main() {
    tracker := process.NewFilePIDTracker(process.FilePIDTrackerConfig{
        AppName: "myapp",
    })

    // On daemon startup, cleanup orphans from previous run
    killed, err := tracker.CleanupOrphans(os.Getpid())
    if err != nil {
        log.Fatal(err)
    }
    if killed > 0 {
        log.Printf("Cleaned up %d orphan processes", killed)
    }

    // Track a new process
    err = tracker.Add("worker-1", 12345, 12345, "/project")
    if err != nil {
        log.Fatal(err)
    }

    // Remove when done
    err = tracker.Remove("worker-1", "/project")
    if err != nil {
        log.Fatal(err)
    }
}
```

## Protocol Format

Commands use an explicit terminator-based format for resilience:

```
VERB [SUBVERB] [ARGS...] [-- LENGTH\nBASE64DATA];;
```

Responses:
```
OK [message];;
ERR CODE message;;
JSON -- LENGTH\nBASE64DATA;;
PONG;;
CHUNK -- LENGTH\nBASE64DATA;;
END;;
```

## Process Management

The hub includes optional process management:

```go
config := hub.Config{
    EnableProcessMgmt: true,
    ProcessConfig: process.ManagerConfig{
        MaxOutputBuffer:   256 * 1024,
        GracefulTimeout:   5 * time.Second,
    },
}
h := hub.New(config)

// Now PROC and RUN commands are available
// PROC STATUS <id>
// PROC OUTPUT <id>
// PROC STOP <id>
// PROC LIST
// RUN-JSON with config payload
```

## Message Relay

Processes can attach to the hub for inter-process messaging:

```
ATTACH -- {"id": "worker-1", "labels": {"type": "indexer"}};;
RELAY SEND worker-2 -- base64message;;
RELAY BROADCAST -- base64message;;
DETACH worker-1;;
```

## Design Principles

1. **Lock-free where possible**: Use `sync.Map` and `atomic` operations
2. **Explicit state machine**: Process states with CAS transitions
3. **Bounded buffers**: Ring buffers prevent unbounded memory growth
4. **Graceful shutdown**: SIGTERM with timeout before SIGKILL
5. **Platform support**: Unix and Windows with appropriate abstractions
6. **Callback-based extensibility**: Generic components use callbacks for app-specific behavior

## Version Management

The client includes semantic version utilities:

```go
// Parse and compare versions
major, minor, patch, err := client.ParseVersion("v1.2.3")
cmp, err := client.CompareVersions("1.0.0", "1.0.1") // returns -1

// Check compatibility
if client.VersionsMatch(clientVer, hubVer) {
    // Versions are equal
}

// Create version check for resilient client
versionCheck := client.MakeVersionCheck("1.0.0", func(clientVer, hubVer string) error {
    // Handle mismatch - e.g., trigger update
    return nil // proceed anyway
})
```

## License

MIT License - see LICENSE file for details.
