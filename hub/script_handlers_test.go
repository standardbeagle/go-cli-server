package hub

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
	"github.com/standardbeagle/go-cli-server/script"
)

func startTestHub(t *testing.T, reg *script.Registry) *Hub {
	t.Helper()
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "test.sock")
	h := New(cfg)
	if reg != nil {
		h.pm.SetScriptRegistry(reg)
	}
	if err := h.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	})
	time.Sleep(20 * time.Millisecond)
	return h
}

func connectHub(t *testing.T, sockPath string) (*protocol.Writer, *protocol.Parser, net.Conn) {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return protocol.NewWriter(conn), protocol.NewParser(conn), conn
}

// TestScriptHandlerRegistration verifies SCRIPT command is registered when PM exists.
func TestScriptHandlerRegistration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "test.sock")
	h := New(cfg)

	if !h.commands.HasVerb("SCRIPT") {
		t.Error("SCRIPT command should be registered when ProcessManager is enabled")
	}
}

// TestScriptHandlerNotRegisteredWithoutPM verifies SCRIPT is absent without PM.
func TestScriptHandlerNotRegisteredWithoutPM(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SocketPath = filepath.Join(t.TempDir(), "test.sock")
	cfg.EnableProcessMgmt = false
	h := New(cfg)

	if h.commands.HasVerb("SCRIPT") {
		t.Error("SCRIPT should NOT be registered without ProcessManager")
	}
}

// TestScriptEntryToSummary verifies the Entry to summary map conversion.
func TestScriptEntryToSummary(t *testing.T) {
	reg := script.NewRegistry()
	entry, err := reg.Register("dev", "/test/project", &script.Config{Run: "npm run dev"})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	entry.SetResolvedCommand("sh", []string{"-c", "npm run dev"})
	entry.SetState(script.StateRunning)
	entry.IncrementStartCount()

	summary := scriptEntryToSummary(entry)

	if summary["name"] != "dev" {
		t.Errorf("name = %v, want %q", summary["name"], "dev")
	}
	if summary["state"] != "running" {
		t.Errorf("state = %v, want %q", summary["state"], "running")
	}
	if summary["start_count"] != int64(1) {
		t.Errorf("start_count = %v, want 1", summary["start_count"])
	}
	if summary["command"] != "sh" {
		t.Errorf("command = %v, want %q", summary["command"], "sh")
	}
	if _, ok := summary["last_error"]; ok {
		t.Error("last_error should not be present when empty")
	}
}

// TestScriptEntryToSummaryWithError verifies last_error inclusion.
func TestScriptEntryToSummaryWithError(t *testing.T) {
	reg := script.NewRegistry()
	entry, _ := reg.Register("build", "/test/project", &script.Config{Run: "make build"})
	entry.SetLastError("exit code 1")
	entry.IncrementFailCount()

	summary := scriptEntryToSummary(entry)

	if summary["last_error"] != "exit code 1" {
		t.Errorf("last_error = %v, want %q", summary["last_error"], "exit code 1")
	}
	if summary["fail_count"] != int64(1) {
		t.Errorf("fail_count = %v, want 1", summary["fail_count"])
	}
}

// TestScriptEntryToSummaryLastStarted verifies last_started derivation.
func TestScriptEntryToSummaryLastStarted(t *testing.T) {
	reg := script.NewRegistry()
	entry, _ := reg.Register("server", "/test/project", &script.Config{Run: "node server.js"})
	entry.SetState(script.StateStarting)
	entry.SetState(script.StateRunning)

	summary := scriptEntryToSummary(entry)

	ts, ok := summary["last_started"].(string)
	if !ok {
		t.Fatal("last_started should be present after StateStarting transition")
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("last_started is not valid RFC3339: %v", err)
	}
}

// TestNormalizePath verifies path normalization.
func TestNormalizePath(t *testing.T) {
	if got := normalizePath(""); got != "." {
		t.Errorf("normalizePath('') = %q, want '.'", got)
	}
	if got := normalizePath("."); got != "." {
		t.Errorf("normalizePath('.') = %q, want '.'", got)
	}
	if got := normalizePath("/some/path"); got != "/some/path" {
		t.Errorf("normalizePath('/some/path') = %q, want '/some/path'", got)
	}
}

// TestHandleScriptMissingSub verifies missing sub-verb error.
func TestHandleScriptMissingSub(t *testing.T) {
	h := startTestHub(t, nil)
	w, p, _ := connectHub(t, h.SocketPath())

	if err := w.WriteCommand("SCRIPT", nil, nil); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s: %s", resp.Type, resp.Message)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrMissingParam {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrMissingParam)
	}
}

// TestHandleScriptInvalidSub verifies unknown sub-verb error.
func TestHandleScriptInvalidSub(t *testing.T) {
	h := startTestHub(t, nil)
	w, p, _ := connectHub(t, h.SocketPath())

	if err := w.WriteCommand("SCRIPT BOGUS", nil, nil); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrInvalidAction {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrInvalidAction)
	}
}

// TestHandleScriptListNoRegistry verifies error when ScriptRegistry is nil.
func TestHandleScriptListNoRegistry(t *testing.T) {
	h := startTestHub(t, nil) // no registry
	w, p, _ := connectHub(t, h.SocketPath())

	if err := w.WriteCommand("SCRIPT LIST", nil, nil); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrInternal {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrInternal)
	}
}

// TestHandleScriptListMissingDirectory verifies error without directory.
func TestHandleScriptListMissingDirectory(t *testing.T) {
	reg := script.NewRegistry()
	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	if err := w.WriteCommand("SCRIPT LIST", nil, nil); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrMissingParam {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrMissingParam)
	}
}

// TestHandleScriptListSuccess verifies SCRIPT LIST returns scripts.
func TestHandleScriptListSuccess(t *testing.T) {
	reg := script.NewRegistry()
	projectPath := "/test/project"
	entry, _ := reg.Register("dev", projectPath, &script.Config{Run: "npm run dev"})
	entry.SetResolvedCommand("sh", []string{"-c", "npm run dev"})
	entry.SetState(script.StateRunning)

	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": projectPath})
	if err := w.WriteCommand("SCRIPT LIST", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s", resp.Type)
	}

	var result struct {
		Scripts []map[string]any `json:"scripts"`
		Count   int              `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("count = %d, want 1", result.Count)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts len = %d, want 1", len(result.Scripts))
	}
	if result.Scripts[0]["name"] != "dev" {
		t.Errorf("name = %v, want %q", result.Scripts[0]["name"], "dev")
	}
}

// TestHandleScriptGetNotFound verifies not-found error.
func TestHandleScriptGetNotFound(t *testing.T) {
	reg := script.NewRegistry()
	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": "/test/project"})
	if err := w.WriteCommand("SCRIPT GET nonexistent", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrNotFound {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrNotFound)
	}
}

// TestHandleScriptGetSuccess verifies SCRIPT GET returns full details.
func TestHandleScriptGetSuccess(t *testing.T) {
	reg := script.NewRegistry()
	projectPath := "/test/project"
	entry, _ := reg.Register("dev", projectPath, &script.Config{Run: "npm run dev"})
	entry.SetResolvedCommand("sh", []string{"-c", "npm run dev"})
	entry.SetState(script.StateRunning)
	entry.AppendOutput("server started")
	entry.AppendOutput("listening on :3000")

	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": projectPath})
	if err := w.WriteCommand("SCRIPT GET dev", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s", resp.Type)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if result["name"] != "dev" {
		t.Errorf("name = %v, want %q", result["name"], "dev")
	}
	if result["state"] != "running" {
		t.Errorf("state = %v, want %q", result["state"], "running")
	}

	output, ok := result["output"].([]any)
	if !ok {
		t.Fatalf("output should be array, got %T", result["output"])
	}
	if len(output) != 2 {
		t.Errorf("output len = %d, want 2", len(output))
	}

	history, ok := result["history"].([]any)
	if !ok {
		t.Fatalf("history should be array, got %T", result["history"])
	}
	if len(history) < 2 {
		t.Errorf("history len = %d, want >= 2", len(history))
	}
}

// TestHandleScriptOutputSuccess verifies SCRIPT OUTPUT with tail.
func TestHandleScriptOutputSuccess(t *testing.T) {
	reg := script.NewRegistry()
	projectPath := "/test/project"
	entry, _ := reg.Register("dev", projectPath, &script.Config{Run: "npm run dev"})
	for i := 0; i < 10; i++ {
		entry.AppendOutput("line")
	}

	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]any{"directory": projectPath, "tail": 5})
	if err := w.WriteCommand("SCRIPT OUTPUT dev", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s", resp.Type)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	lines := result["lines"].([]any)
	if len(lines) != 5 {
		t.Errorf("lines count = %d, want 5", len(lines))
	}
	if result["total"].(float64) != 10 {
		t.Errorf("total = %v, want 10", result["total"])
	}
	if result["count"].(float64) != 5 {
		t.Errorf("count = %v, want 5", result["count"])
	}
}

// TestHandleScriptStopAlreadyStopped verifies STOP on non-running script.
func TestHandleScriptStopAlreadyStopped(t *testing.T) {
	reg := script.NewRegistry()
	projectPath := "/test/project"
	entry, _ := reg.Register("dev", projectPath, &script.Config{Run: "npm run dev"})
	entry.SetState(script.StateStopped)

	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": projectPath})
	if err := w.WriteCommand("SCRIPT STOP dev", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s", resp.Type)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if result["success"] != true {
		t.Errorf("success = %v, want true", result["success"])
	}
	if result["state"] != "stopped" {
		t.Errorf("state = %v, want %q", result["state"], "stopped")
	}
}

// TestHandleScriptGetMissingName verifies error when name is missing.
func TestHandleScriptGetMissingName(t *testing.T) {
	reg := script.NewRegistry()
	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	if err := w.WriteCommand("SCRIPT GET", nil, nil); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrMissingParam {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrMissingParam)
	}
}

// TestHandleScriptRestartNotFound verifies RESTART on non-existent script.
func TestHandleScriptRestartNotFound(t *testing.T) {
	reg := script.NewRegistry()
	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": "/test/project"})
	if err := w.WriteCommand("SCRIPT RESTART ghost", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}

	var se protocol.StructuredError
	if err := json.Unmarshal(resp.Data, &se); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if se.Code != protocol.ErrNotFound {
		t.Errorf("code = %q, want %q", se.Code, protocol.ErrNotFound)
	}
}

// TestHandleScriptRestartNoProcess verifies RESTART when no running process.
func TestHandleScriptRestartNoProcess(t *testing.T) {
	reg := script.NewRegistry()
	projectPath := "/test/project"
	entry, _ := reg.Register("dev", projectPath, &script.Config{Run: "npm run dev"})
	entry.SetState(script.StateIdle)

	h := startTestHub(t, reg)
	w, p, _ := connectHub(t, h.SocketPath())

	data, _ := json.Marshal(map[string]string{"directory": projectPath})
	if err := w.WriteCommand("SCRIPT RESTART dev", nil, data); err != nil {
		t.Fatalf("WriteCommand error: %v", err)
	}

	resp, err := p.ParseResponse()
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Type != protocol.ResponseJSON {
		t.Fatalf("expected JSON, got %s", resp.Type)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if result["success"] != true {
		t.Errorf("success = %v, want true", result["success"])
	}
}
