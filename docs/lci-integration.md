# LCI Integration with go-cli-server

## Overview

This document outlines how Lightning Code Index (LCI) can integrate with go-cli-server to gain daemon architecture benefits while maintaining its standalone MCP server capability.

## Current LCI Architecture

LCI currently operates as a standalone MCP server:

```
┌─────────────────┐
│   Claude Code   │
│  (MCP Client)   │
└────────┬────────┘
         │ stdio MCP
         ▼
┌─────────────────┐
│       LCI       │
│  ┌───────────┐  │
│  │ Indexer   │  │
│  └───────────┘  │
│  ┌───────────┐  │
│  │  Search   │  │
│  └───────────┘  │
│  ┌───────────┐  │
│  │MCP Server │  │
│  └───────────┘  │
└─────────────────┘
```

**Characteristics:**
- Single process MCP server via stdio
- Indexes on-demand per session
- No state persistence between sessions
- Uses `urfave/cli` for CLI commands
- Uses `github.com/modelcontextprotocol/go-sdk/mcp` for MCP

## Integration Benefits

### 1. Persistent Index Across Sessions

With go-cli-server hub integration, LCI can maintain its index across MCP sessions:

```
┌─────────────────┐     ┌─────────────────┐
│  Claude Code 1  │     │  Claude Code 2  │
│  (MCP Client)   │     │  (MCP Client)   │
└────────┬────────┘     └────────┬────────┘
         │                       │
         │ stdio                 │ stdio
         ▼                       ▼
┌─────────────────┐     ┌─────────────────┐
│   MCP Shim 1    │     │   MCP Shim 2    │
└────────┬────────┘     └────────┬────────┘
         │                       │
         └──────────┬────────────┘
                    │ socket
                    ▼
┌──────────────────────────────────────────┐
│            go-cli-server hub              │
│  ┌──────────────────────────────────────┐│
│  │       LCI Subprocess                 ││
│  │  ┌───────────┐  ┌───────────────┐   ││
│  │  │ Indexer   │  │ Persistent    │   ││
│  │  │ (shared)  │  │ Index Cache   │   ││
│  │  └───────────┘  └───────────────┘   ││
│  └──────────────────────────────────────┘│
└──────────────────────────────────────────┘
```

**Benefits:**
- First session indexes the codebase
- Subsequent sessions start instantly (no re-indexing)
- All clients share the same index
- File watching can update index incrementally

### 2. Scheduled Indexing Tasks

Use go-cli-server's scheduler for background index maintenance:

```go
import "github.com/standardbeagle/go-cli-server/scheduler"

s, _ := scheduler.New(scheduler.Config{
    TickInterval: 1 * time.Second,
    DeliverFunc: func(ctx context.Context, targetID, payload string) error {
        switch payload {
        case "reindex":
            return indexer.IncrementalUpdate(ctx)
        case "compact":
            return indexer.CompactIndex(ctx)
        case "git-analyze":
            return indexer.GitAnalyze(ctx, targetID)
        }
        return nil
    },
})
```

**Use Cases:**
- Periodic incremental re-indexing
- Scheduled index compaction
- Triggered git change analysis
- Stale index cleanup

### 3. Process Management for File Watchers

Use go-cli-server's process package for file watching:

```go
import "github.com/standardbeagle/go-cli-server/process"

pm := process.NewProcessManager(process.ManagerConfig{
    MaxOutputBuffer:   64 * 1024,
    HealthCheckPeriod: 30 * time.Second,
})

// Start file watcher as managed process
cfg := process.ProcessConfig{
    ID:          "file-watcher",
    ProjectPath: projectRoot,
    Command:     "fswatch",
    Args:        []string{"-r", projectRoot},
    Labels:      map[string]string{"type": "watcher"},
}
proc, _ := pm.StartCommand(ctx, cfg)

// Read change events from output
go func() {
    for {
        output, _ := proc.ReadOutput()
        if changedFile := parseChange(output); changedFile != "" {
            indexer.UpdateFile(changedFile)
        }
    }
}()
```

### 4. Hub Subprocess Model

LCI can run as a hub subprocess, receiving commands from the hub:

```go
import (
    "github.com/standardbeagle/go-cli-server/client"
    "github.com/standardbeagle/go-cli-server/protocol"
)

// Create subprocess server
server := client.NewSubprocessServer(client.SubprocessServerConfig{
    ID: "lci",
    Transport: client.TransportConfig{
        Type:    "unix",
        Address: "/tmp/lci.sock",
    },
})

// Register command handlers
server.RegisterHandler("SEARCH", handleSearch)
server.RegisterHandler("INDEX", handleIndex)
server.RegisterHandler("CONTEXT", handleContext)
server.RegisterHandler("GIT-ANALYZE", handleGitAnalyze)

// Start server and register with hub
client.StartWithHub("/tmp/go-cli-server.sock",
    protocol.SubprocessRegisterConfig{
        ID:       "lci",
        Name:     "Lightning Code Index",
        Commands: []string{"SEARCH *", "INDEX *", "CONTEXT *", "GIT-ANALYZE *"},
        Transport: protocol.SubprocessTransport{
            Type:    "unix",
            Address: "/tmp/lci.sock",
        },
    },
    serverConfig,
)
```

## Integration Approaches

### Approach 1: Thin MCP Shim (Recommended)

Create a thin MCP shim that connects to go-cli-server hub:

```
┌─────────────────┐
│   Claude Code   │
└────────┬────────┘
         │ stdio MCP
         ▼
┌─────────────────┐         ┌─────────────────┐
│   lci-mcp-shim  │◄───────►│  go-cli-server  │
│                 │ socket  │      hub        │
│  Translates:    │         │                 │
│  MCP ↔ Protocol │         │  ┌───────────┐  │
└─────────────────┘         │  │    LCI    │  │
                            │  │subprocess │  │
                            │  └───────────┘  │
                            └─────────────────┘
```

**Implementation:**
1. Create `lci-mcp-shim` binary that:
   - Starts as MCP server over stdio
   - Connects to hub via socket
   - Translates MCP tool calls to hub commands
   - Translates hub responses to MCP results

2. LCI runs as hub subprocess handling:
   - `INDEX START/STATUS/STOP`
   - `SEARCH <pattern>`
   - `CONTEXT <id>`
   - `GIT-ANALYZE <scope>`

**Pros:**
- Clean separation of concerns
- Multiple MCP clients share index
- LCI can still run standalone

**Cons:**
- Extra binary to maintain
- Additional IPC hop

### Approach 2: Direct Hub Integration

Modify LCI to optionally connect to hub directly:

```go
// In lci/cmd/lci/main.go
func mcpCommand(c *cli.Context) error {
    if hubMode := c.Bool("hub"); hubMode {
        // Run as hub subprocess
        return runAsHubSubprocess(c)
    }
    // Run standalone MCP server
    return runStandaloneMCP(c)
}

func runAsHubSubprocess(c *cli.Context) error {
    // Connect to hub
    hubConn := client.NewConn()
    if err := hubConn.Ping(); err != nil {
        return fmt.Errorf("hub not available: %w", err)
    }

    // Create subprocess server
    server := client.NewSubprocessServer(client.SubprocessServerConfig{
        ID: "lci",
        Transport: client.TransportConfig{
            Type:    "unix",
            Address: "/tmp/lci.sock",
        },
    })

    // Register handlers using existing MCP handler logic
    server.RegisterHandlers(map[string]client.CommandHandler{
        "SEARCH":      wrapMCPHandler(handleSearch),
        "INDEX":       wrapMCPHandler(handleIndex),
        "CONTEXT":     wrapMCPHandler(handleContext),
        "GIT-ANALYZE": wrapMCPHandler(handleGitAnalyze),
    })

    // Start and register
    return server.Start()
}
```

**Pros:**
- Single binary
- Reuses existing handler logic
- No translation layer

**Cons:**
- More complex main.go
- Tighter coupling to go-cli-server

### Approach 3: Hybrid Mode

LCI auto-detects hub availability:

```go
func main() {
    // Try hub connection
    hubConn := client.NewConn()
    if err := hubConn.Ping(); err == nil {
        // Hub available - run as subprocess
        return runAsHubSubprocess()
    }

    // No hub - run standalone
    return runStandaloneMCP()
}
```

**Pros:**
- Best of both worlds
- Backward compatible
- Seamless upgrade path

**Cons:**
- More complex initialization
- Potential for inconsistent behavior

## Command Mapping

| LCI MCP Tool | Hub Command | Notes |
|--------------|-------------|-------|
| `search` | `SEARCH <pattern>` | Returns JSON results |
| `get_context` | `CONTEXT GET <id>` | Returns symbol context |
| `find_files` | `FILES FIND <pattern>` | File path search |
| `code_insight` | `INSIGHT <mode>` | Codebase analysis |
| `semantic_annotations` | `SEMANTIC QUERY <label>` | Semantic search |
| `side_effects` | `EFFECTS <symbol>` | Side effect analysis |
| `context` (manifest) | `MANIFEST SAVE/LOAD` | Context manifests |

## Implementation Status

The LCI integration has been implemented with the following components:

### Completed Phase 1: Hub Subprocess Mode

**Files Created:**
- `internal/hub/subprocess.go` - LCI subprocess server that handles hub commands
- `internal/hub/subprocess_test.go` - Basic tests for hub integration

**Files Modified:**
- `go.mod` - Added go-cli-server dependency with replace directive
- `cmd/lci/main.go` - Added `--hub`, `--hub-socket`, and `--socket` flags to mcp command

**Command Handlers Implemented:**
- `INDEX START/STATUS/STOP` - Trigger and monitor indexing
- `SEARCH` - Execute code search queries
- `CONTEXT GET` - Symbol context lookup by ID or name
- `FILES FIND` - File discovery with glob patterns
- `INSIGHT` - Codebase analysis and statistics
- `SEMANTIC QUERY` - Semantic annotation queries
- `EFFECTS` - Side effect analysis for symbols
- `STATUS` - Subprocess status information

**Hybrid Mode:**
- Auto-detects hub availability at startup
- Falls back to standalone MCP mode if no hub is present
- Can be forced with `--hub` flag

### Usage

```bash
# Run LCI in hub subprocess mode (auto-detect)
lci mcp

# Force hub mode
lci mcp --hub

# Specify custom socket paths
lci mcp --hub --hub-socket /tmp/custom-hub.sock --socket /tmp/custom-lci.sock
```

## Migration Path

### Phase 1: Library Integration (COMPLETED)

1. Add go-cli-server as dependency to LCI ✓
2. Implement hub subprocess mode ✓
3. Add hybrid mode detection ✓

```go
// go.mod
require github.com/standardbeagle/go-cli-server v0.x.x

// Use scheduler for periodic tasks
s, _ := scheduler.New(scheduler.Config{
    DeliverFunc: func(ctx context.Context, target, payload string) error {
        return indexer.IncrementalUpdate(ctx)
    },
})
s.Schedule("indexer", 5*time.Minute, "reindex", projectRoot)
```

### Phase 2: Optional Hub Mode

1. Add `--hub` flag for subprocess mode
2. Keep standalone MCP as default
3. Create command handler adapters

### Phase 3: Full Integration

1. Register LCI as hub subprocess
2. Create MCP shim binary
3. Document both deployment modes

## API Design Considerations

### Error Handling

Both go-cli-server and LCI use explicit error types:

```go
// go-cli-server pattern
var ErrProcessNotFound = errors.New("process not found")

// LCI should follow same pattern
var ErrSymbolNotFound = errors.New("symbol not found")
var ErrIndexNotReady = errors.New("index not ready")
```

### Lock-Free Design

Both projects prefer lock-free data structures:

```go
// go-cli-server uses sync.Map
processes sync.Map // map[key]*ManagedProcess

// LCI should continue this pattern
symbolIndex sync.Map // map[symbolID]*Symbol
```

### Response Format

Hub uses structured responses:

```go
// go-cli-server response helpers
client.OKResponse("search complete")
client.JSONResponse(searchResults)
client.ErrResponse(protocol.ErrNotFound, "symbol not found")

// LCI handlers should return same format
func handleSearch(ctx context.Context, cmd *protocol.Command) *protocol.Response {
    results, err := indexer.Search(cmd.Args[0])
    if err != nil {
        return client.ErrResponse(protocol.ErrInternal, err.Error())
    }
    return client.JSONResponse(results)
}
```

## Testing Strategy

### Unit Tests

Both projects use table-driven tests:

```go
func TestSearchCommand(t *testing.T) {
    tests := []struct {
        name    string
        pattern string
        want    int
        wantErr bool
    }{
        {"simple", "foo", 5, false},
        {"regex", "foo.*bar", 2, false},
        {"empty", "", 0, true},
    }
    // ...
}
```

### Integration Tests

Test LCI as hub subprocess:

```go
func TestLCIAsSubprocess(t *testing.T) {
    // Start hub
    h := hub.New(hub.DefaultConfig())
    h.Start()
    defer h.Stop(context.Background())

    // Start LCI subprocess
    lci := startLCISubprocess(t, h.SocketPath())
    defer lci.Stop()

    // Wait for registration
    waitForSubprocess(t, h, "lci")

    // Send search command through hub
    conn := client.NewConn()
    result, err := conn.Request("SEARCH", "").
        WithArgs("function foo").JSON()
    require.NoError(t, err)
    // ...
}
```

## Conclusion

LCI can integrate with go-cli-server in phases:
1. **Phase 1**: Use scheduler package for background tasks (minimal changes)
2. **Phase 2**: Add optional hub subprocess mode
3. **Phase 3**: Full integration with MCP shim

The recommended approach is **Approach 3 (Hybrid Mode)** which provides:
- Backward compatibility with standalone mode
- Seamless upgrade to hub mode when available
- Shared resources across multiple clients
- Persistent index for faster startup
