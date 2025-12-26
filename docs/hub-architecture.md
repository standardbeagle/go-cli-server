# go-cli-server Hub Architecture

## Vision

go-cli-server is a **shared development infrastructure hub** that enables multiple local applications (MCP servers, CLI tools, GUI apps) to share resources like code indexes, reverse proxies, SSH terminals, and managed processes.

## Core Principles

1. **Resource Sharing** - Multiple clients share expensive resources (indexes, connections)
2. **Process Isolation** - Subprocesses are isolated but can communicate through the hub
3. **Protocol Agnostic** - Clients can connect via socket, MCP, WebSocket, gRPC
4. **Plugin Architecture** - Subprocesses register capabilities, hub routes commands

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      go-cli-server hub                       │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                  Connection Layer                       │ │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │ │
│  │  │  Unix    │ │  Named   │ │   MCP    │ │WebSocket │  │ │
│  │  │  Socket  │ │  Pipe    │ │  Stdio   │ │  Server  │  │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
│                            │                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                   Protocol Layer                        │ │
│  │  ┌──────────────────────────────────────────────────┐  │ │
│  │  │  Text Protocol Parser (existing)                 │  │ │
│  │  │  VERB [SUBVERB] [ARGS...] [LENGTH]\r\n[DATA]     │  │ │
│  │  └──────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
│                            │                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                   Routing Layer                         │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │ │
│  │  │   Command    │  │  Subprocess  │  │   Built-in   │  │ │
│  │  │   Router     │  │   Registry   │  │   Handlers   │  │ │
│  │  └──────────────┘  └──────────────┘  └──────────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
│                            │                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                  Resource Layer                         │ │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │ │
│  │  │ Process  │ │ Session  │ │Scheduler │ │   PID    │  │ │
│  │  │ Manager  │ │ Registry │ │          │ │ Tracker  │  │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
│                            │                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                 Subprocess Layer                        │ │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │ │
│  │  │   agnt   │ │   code   │ │   ssh    │ │  plugin  │  │ │
│  │  │  proxy   │ │  index   │ │ terminal │ │    N     │  │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Subprocess Registration

Subprocesses register their capabilities when they start:

```go
// Subprocess configuration
type SubprocessConfig struct {
    // Unique identifier for this subprocess
    ID string

    // Human-readable name
    Name string

    // Commands this subprocess handles
    // Supports wildcards: "PROXY *" matches PROXY START, PROXY STOP, etc.
    Commands []string

    // How to communicate with this subprocess
    Transport SubprocessTransport

    // Health check configuration
    HealthCheck HealthCheckConfig

    // Auto-restart on crash
    AutoRestart bool

    // Resource requirements/limits
    Resources ResourceConfig
}

type SubprocessTransport struct {
    // Transport type: "unix", "tcp", "grpc", "stdio"
    Type string

    // Address (socket path, host:port, etc.)
    Address string

    // For stdio: command to execute
    Command string
    Args    []string
}
```

## Command Routing

The router maintains a routing table:

```go
type Router struct {
    // Exact command matches (highest priority)
    exact map[string]string  // command -> subprocess ID

    // Prefix matches (e.g., "PROXY" matches "PROXY START")
    prefix map[string]string

    // Built-in handlers (process management, ping, info)
    builtin map[string]Handler
}

// Routing priority:
// 1. Exact match in routing table
// 2. Prefix match in routing table
// 3. Built-in handler
// 4. Unknown command error
```

## Built-in Commands

The hub provides built-in commands that don't route to subprocesses:

| Command | Description |
|---------|-------------|
| `PING` | Health check |
| `INFO` | Hub information and statistics |
| `PROC *` | Process management |
| `RUN *` | Start processes |
| `SUBPROCESS REGISTER` | Register a subprocess (JSON config in data) |
| `SUBPROCESS UNREGISTER <id>` | Remove a subprocess |
| `SUBPROCESS START <id>` | Start a registered subprocess |
| `SUBPROCESS STOP <id>` | Stop a running subprocess |
| `SUBPROCESS STATUS <id>` | Get subprocess status |
| `SUBPROCESS LIST` | List all registered subprocesses |

## Client-Driven Registration

Clients register subprocesses with the hub using the `SUBPROCESS REGISTER` command. The hub then manages the subprocess lifecycle (starting, stopping, health checking, auto-restart).

### Registration Flow

```
┌─────────┐                    ┌─────────┐                    ┌────────────┐
│  Client │                    │   Hub   │                    │ Subprocess │
└────┬────┘                    └────┬────┘                    └─────┬──────┘
     │                              │                               │
     │ SUBPROCESS REGISTER {...}    │                               │
     │─────────────────────────────>│                               │
     │                              │                               │
     │                              │ Store config, rebuild routes  │
     │                              │──────────────────────────────>│
     │                              │                               │
     │    JSON {id, state, ...}     │                               │
     │<─────────────────────────────│                               │
     │                              │                               │
     │ SUBPROCESS START <id>        │                               │
     │─────────────────────────────>│                               │
     │                              │                               │
     │                              │ Connect (unix/tcp/stdio)      │
     │                              │──────────────────────────────>│
     │                              │                               │
     │                              │ Start health checks           │
     │                              │<─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│
     │                              │                               │
     │    JSON {state: "running"}   │                               │
     │<─────────────────────────────│                               │
     │                              │                               │
     │ PROXY START dev ...          │                               │
     │─────────────────────────────>│                               │
     │                              │                               │
     │                              │ Forward: PROXY START dev ...  │
     │                              │──────────────────────────────>│
     │                              │                               │
     │                              │ Response: JSON {...}          │
     │                              │<──────────────────────────────│
     │                              │                               │
     │    JSON {id: "dev", ...}     │                               │
     │<─────────────────────────────│                               │
     │                              │                               │
```

### Registration Protocol

**Request:**
```
SUBPROCESS REGISTER -- 256
eyJpZCI6ImFnbnQiLCJuYW1lIjoiYWdudCAtIEJyb3dzZXIgU3VwZXJwb3dlcnMiLC...;;
```

**JSON Payload:**
```json
{
  "id": "agnt",
  "name": "agnt - Browser Superpowers",
  "description": "Reverse proxy, traffic logging, frontend instrumentation",
  "commands": ["PROXY *", "PROXYLOG *", "CURRENTPAGE *", "SESSION *"],
  "transport": {
    "type": "unix",
    "address": "/tmp/agnt.sock"
  },
  "health_check": {
    "enabled": true,
    "interval_ms": 10000,
    "timeout_ms": 5000,
    "failure_threshold": 3
  },
  "auto_start": true,
  "auto_restart": true,
  "max_restarts": 5,
  "restart_delay_ms": 1000
}
```

**Response:**
```
JSON -- 156
eyJpZCI6ImFnbnQiLCJuYW1lIjoiYWdudCAtIEJyb3dzZXIgU3VwZXJwb3dlcnMiLC...;;
```

### Transport Types

| Type | Description | Required Fields |
|------|-------------|-----------------|
| `unix` | Unix domain socket | `address` (socket path) |
| `tcp` | TCP connection | `address` (host:port) |
| `stdio` | Spawn process, use stdin/stdout | `command`, `args`, `env` |

### Auto-Restart Behavior

When `auto_restart` is enabled:
1. Hub monitors subprocess health via periodic PING commands
2. If `failure_threshold` consecutive health checks fail, subprocess is marked unhealthy
3. Hub closes existing connection and waits `restart_delay_ms`
4. Hub attempts to reconnect/restart the subprocess
5. Process repeats until `max_restarts` limit is reached (0 = unlimited)
6. If limit reached, subprocess state becomes `failed`

## Subprocess Communication

### Option 1: Text Protocol (Simple)

Subprocesses use the same text protocol as clients:

```
Hub → Subprocess: PROXY START dev http://localhost:3000 42\r\n{"port": 8080}\r\n
Subprocess → Hub: JSON 156\r\n{"id": "dev", "listen_addr": ":12345", ...}\r\n
```

### Option 2: gRPC (Typed, Efficient)

```protobuf
service Subprocess {
    rpc HandleCommand(CommandRequest) returns (CommandResponse);
    rpc StreamEvents(Empty) returns (stream Event);
    rpc HealthCheck(Empty) returns (HealthStatus);
}

message CommandRequest {
    string verb = 1;
    string subverb = 2;
    repeated string args = 3;
    bytes data = 4;
    map<string, string> metadata = 5;
}
```

### Option 3: Simple JSON-RPC over Unix Socket

```json
// Request
{"id": 1, "method": "proxy.start", "params": {"id": "dev", "target": "http://localhost:3000"}}

// Response
{"id": 1, "result": {"id": "dev", "listen_addr": ":12345"}}
```

## Shared Resources

### Code Index

Multiple clients can query the same index:

```
Client A: INDEX SEARCH "function foo" → results
Client B: INDEX SEARCH "class Bar" → results (same index)
```

The index subprocess maintains the index, hub routes queries.

### Reverse Proxies

Proxies are shared across clients:

```
Client A: PROXY START dev http://localhost:3000
Client B: PROXY STATUS dev → sees same proxy
Client C: PROXYLOG QUERY dev → sees traffic from all clients
```

### SSH Terminals

Terminals can be shared or isolated:

```
Client A: SSH CONNECT server1 → terminal session
Client B: SSH ATTACH server1 → joins same session (if permitted)
Client C: SSH CONNECT server1 --new → new isolated session
```

## Client Types

### MCP Client (Claude Code, Claude Desktop)

- Connects via stdio MCP transport
- Hub translates MCP tool calls to text protocol
- Results translated back to MCP format

### CLI Client

- Connects via Unix socket
- Direct text protocol communication
- Interactive or scripted usage

### GUI Client (Wails App)

- Connects via WebSocket or Unix socket
- Real-time updates via event streaming
- Rich UI for process/proxy/terminal management

### VS Code Extension

- Connects via WebSocket
- Integrates with VS Code UI
- Terminal integration, proxy status, etc.

## Implementation Plan

### Phase 1: Hub Core

1. Refactor existing daemon into hub structure
2. Add subprocess registry
3. Add command router
4. Keep existing built-in handlers

### Phase 2: Subprocess Protocol

1. Define subprocess communication protocol
2. Implement subprocess lifecycle management
3. Add health checking and auto-restart

### Phase 3: agnt as Subprocess

1. Refactor agnt to register with hub
2. Move proxy/session/overlay logic to subprocess
3. Remove socket management from agnt (hub handles it)

### Phase 4: Additional Subprocesses

1. Code index subprocess (wrap existing code-search)
2. SSH terminal subprocess
3. Additional plugins as needed

## Example: agnt as Subprocess

```go
// agnt/main.go
func main() {
    // Connect to hub
    hub := hubclient.Connect("/tmp/go-cli-server.sock")

    // Register capabilities
    hub.Register(hubclient.SubprocessConfig{
        ID:   "agnt",
        Name: "agnt - Browser Superpowers",
        Commands: []string{
            "PROXY *",
            "PROXYLOG *",
            "CURRENTPAGE *",
            "SESSION *",
            "OVERLAY *",
            "CHAOS *",
            "TUNNEL *",
        },
    })

    // Handle commands
    hub.OnCommand(func(cmd hubclient.Command) hubclient.Response {
        switch cmd.Verb {
        case "PROXY":
            return handleProxy(cmd)
        case "SESSION":
            return handleSession(cmd)
        // ...
        }
    })

    // Run until shutdown
    hub.Wait()
}
```

## Benefits

1. **Resource Efficiency** - One index, shared proxies, pooled connections
2. **Consistent State** - All clients see same state
3. **Extensibility** - Add new subprocesses without changing hub
4. **Isolation** - Subprocess crashes don't affect hub or other subprocesses
5. **Multi-Client** - MCP, CLI, GUI all work together
6. **Discoverability** - Clients can query available capabilities
