package script

import (
	"fmt"
	"sync"
	"testing"
)

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateIdle, "idle"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateFailed, "failed"},
		{StateStopped, "stopped"},
		{StateRestarting, "restarting"},
		{State(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestRegistry_Register(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	entry, err := reg.Register("dev", "/home/user/myapp", cfg)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if entry.Name != "dev" {
		t.Errorf("Name = %q, want %q", entry.Name, "dev")
	}
	if entry.ProjectPath != "/home/user/myapp" {
		t.Errorf("ProjectPath = %q, want %q", entry.ProjectPath, "/home/user/myapp")
	}
	if entry.State() != StateIdle {
		t.Errorf("State() = %v, want %v", entry.State(), StateIdle)
	}
	if entry.Config != cfg {
		t.Errorf("Config = %p, want %p", entry.Config, cfg)
	}
	wantID := MakeProcessID("/home/user/myapp", "dev")
	if entry.ProcessID != wantID {
		t.Errorf("ProcessID = %q, want %q", entry.ProcessID, wantID)
	}
}

func TestRegistry_RegisterReturnsExisting(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	first, err := reg.Register("dev", "/home/user/myapp", cfg)
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	second, err := reg.Register("dev", "/home/user/myapp", cfg)
	if err != nil {
		t.Fatalf("second Register() error = %v", err)
	}

	if first != second {
		t.Error("second Register must return the same entry")
	}
}

func TestRegistry_RegisterValidation(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	if _, err := reg.Register("", "/home/user/myapp", cfg); err == nil {
		t.Error("Register with empty name should return error")
	}

	if _, err := reg.Register("dev", "", cfg); err == nil {
		t.Error("Register with empty project path should return error")
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	reg.Register("dev", "/home/user/myapp", cfg)

	entry, ok := reg.Get("dev", "/home/user/myapp")
	if !ok {
		t.Fatal("Get() returned false for registered entry")
	}
	if entry.Name != "dev" {
		t.Errorf("Name = %q, want %q", entry.Name, "dev")
	}

	if _, ok := reg.Get("build", "/home/user/myapp"); ok {
		t.Error("Get() returned true for unregistered name")
	}

	if _, ok := reg.Get("dev", "/other/path"); ok {
		t.Error("Get() returned true for unregistered path")
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	reg.Register("dev", "/home/user/app1", cfg)
	reg.Register("build", "/home/user/app1", cfg)
	reg.Register("dev", "/home/user/app2", cfg)

	list := reg.List("/home/user/app1")
	if len(list) != 2 {
		t.Errorf("List(app1) len = %d, want 2", len(list))
	}

	list = reg.List("/home/user/app2")
	if len(list) != 1 {
		t.Errorf("List(app2) len = %d, want 1", len(list))
	}

	list = reg.List("/nonexistent")
	if len(list) != 0 {
		t.Errorf("List(nonexistent) len = %d, want 0", len(list))
	}
}

func TestRegistry_ListAll(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	reg.Register("dev", "/home/user/app1", cfg)
	reg.Register("build", "/home/user/app1", cfg)
	reg.Register("dev", "/home/user/app2", cfg)

	all := reg.ListAll()
	if len(all) != 3 {
		t.Errorf("ListAll() len = %d, want 3", len(all))
	}
}

func TestEntry_CompareAndSwapState(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Valid transition: Idle -> Starting
	if !entry.CompareAndSwapState(StateIdle, StateStarting) {
		t.Error("CAS(Idle, Starting) = false, want true")
	}
	if entry.State() != StateStarting {
		t.Errorf("State() = %v, want %v", entry.State(), StateStarting)
	}

	// Invalid transition: tries Idle -> Running but state is Starting
	if entry.CompareAndSwapState(StateIdle, StateRunning) {
		t.Error("CAS(Idle, Running) = true, want false (state is Starting)")
	}
	if entry.State() != StateStarting {
		t.Errorf("State() = %v, want %v", entry.State(), StateStarting)
	}

	// Valid: Starting -> Running
	if !entry.CompareAndSwapState(StateStarting, StateRunning) {
		t.Error("CAS(Starting, Running) = false, want true")
	}
	if entry.State() != StateRunning {
		t.Errorf("State() = %v, want %v", entry.State(), StateRunning)
	}
}

func TestEntry_SetState(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.SetState(StateRunning)
	if entry.State() != StateRunning {
		t.Errorf("State() = %v, want %v", entry.State(), StateRunning)
	}

	entry.SetState(StateFailed)
	if entry.State() != StateFailed {
		t.Errorf("State() = %v, want %v", entry.State(), StateFailed)
	}
}

func TestEntry_StateHistoryBounded(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Initial state (Idle) is already 1 transition.
	// Add 110 more to exceed the cap of 100.
	for i := 0; i < 110; i++ {
		if i%2 == 0 {
			entry.SetState(StateRunning)
		} else {
			entry.SetState(StateIdle)
		}
	}

	history := entry.StateHistory()
	if len(history) > maxStateTransitions {
		t.Errorf("StateHistory() len = %d, want <= %d", len(history), maxStateTransitions)
	}
}

func TestEntry_StateHistoryRecordsTransitions(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.CompareAndSwapState(StateIdle, StateStarting)
	entry.CompareAndSwapState(StateStarting, StateRunning)

	history := entry.StateHistory()
	if len(history) != 3 { // Idle (initial) + Starting + Running
		t.Fatalf("StateHistory() len = %d, want 3", len(history))
	}
	if history[0].State != StateIdle {
		t.Errorf("history[0].State = %v, want %v", history[0].State, StateIdle)
	}
	if history[1].State != StateStarting {
		t.Errorf("history[1].State = %v, want %v", history[1].State, StateStarting)
	}
	if history[2].State != StateRunning {
		t.Errorf("history[2].State = %v, want %v", history[2].State, StateRunning)
	}
}

func TestEntry_OutputRingBuffer(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.AppendOutput("line 1")
	entry.AppendOutput("line 2")
	entry.AppendOutput("line 3")

	lines := entry.OutputLines()
	want := []string{"line 1", "line 2", "line 3"}
	if len(lines) != len(want) {
		t.Fatalf("OutputLines() len = %d, want %d", len(lines), len(want))
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("OutputLines()[%d] = %q, want %q", i, lines[i], w)
		}
	}
	if entry.OutputLen() != 3 {
		t.Errorf("OutputLen() = %d, want 3", entry.OutputLen())
	}
}

func TestEntry_OutputRingBufferOverflow(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Fill beyond capacity
	for i := 0; i < maxOutputLines+500; i++ {
		entry.AppendOutput(fmt.Sprintf("line %d", i))
	}

	lines := entry.OutputLines()
	if len(lines) != maxOutputLines {
		t.Fatalf("OutputLines() len = %d, want %d", len(lines), maxOutputLines)
	}

	// Oldest should be line 500 (the first 500 were evicted)
	if lines[0] != "line 500" {
		t.Errorf("OutputLines()[0] = %q, want %q", lines[0], "line 500")
	}
	wantLast := fmt.Sprintf("line %d", maxOutputLines+499)
	if lines[maxOutputLines-1] != wantLast {
		t.Errorf("OutputLines()[%d] = %q, want %q", maxOutputLines-1, lines[maxOutputLines-1], wantLast)
	}
}

func TestEntry_RestartMarker(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.AppendOutput("run1 output")
	entry.AddRestartMarker()
	entry.AppendOutput("run2 output")

	lines := entry.OutputLines()
	if len(lines) != 3 {
		t.Fatalf("OutputLines() len = %d, want 3", len(lines))
	}
	if lines[0] != "run1 output" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "run1 output")
	}
	if lines[1] != restartMarker {
		t.Errorf("lines[1] = %q, want %q", lines[1], restartMarker)
	}
	if lines[2] != "run2 output" {
		t.Errorf("lines[2] = %q, want %q", lines[2], "run2 output")
	}
}

func TestEntry_Counters(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	if entry.StartCount() != 0 {
		t.Errorf("StartCount() = %d, want 0", entry.StartCount())
	}
	if entry.FailCount() != 0 {
		t.Errorf("FailCount() = %d, want 0", entry.FailCount())
	}

	entry.IncrementStartCount()
	entry.IncrementStartCount()
	entry.IncrementFailCount()

	if entry.StartCount() != 2 {
		t.Errorf("StartCount() = %d, want 2", entry.StartCount())
	}
	if entry.FailCount() != 1 {
		t.Errorf("FailCount() = %d, want 1", entry.FailCount())
	}
}

func TestEntry_ResolvedCommand(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.SetResolvedCommand("sh", []string{"-c", "npm start"})
	cmd, args := entry.ResolvedCommand()
	if cmd != "sh" {
		t.Errorf("cmd = %q, want %q", cmd, "sh")
	}
	wantArgs := []string{"-c", "npm start"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args len = %d, want %d", len(args), len(wantArgs))
	}
	for i, w := range wantArgs {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, args[i], w)
		}
	}
}

func TestEntry_Sessions(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.AddSession("claude-1")
	entry.AddSession("claude-2")

	sessions := entry.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() len = %d, want 2", len(sessions))
	}
	sessionSet := map[string]bool{}
	for _, s := range sessions {
		sessionSet[s] = true
	}
	if !sessionSet["claude-1"] {
		t.Error("ListSessions() missing claude-1")
	}
	if !sessionSet["claude-2"] {
		t.Error("ListSessions() missing claude-2")
	}

	entry.RemoveSession("claude-1")
	sessions = entry.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("ListSessions() len = %d after remove, want 1", len(sessions))
	}
	if sessions[0] != "claude-2" {
		t.Errorf("ListSessions()[0] = %q, want %q", sessions[0], "claude-2")
	}
}

func TestEntry_SessionSharing(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	entry1, _ := reg.Register("dev", "/home/user/myapp", cfg)
	entry2, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Both registrations return the same entry
	entry1.AddSession("session-a")

	sessions := entry2.ListSessions()
	sessionSet := map[string]bool{}
	for _, s := range sessions {
		sessionSet[s] = true
	}
	if !sessionSet["session-a"] {
		t.Error("session sharing failed: entry2.ListSessions() missing session-a")
	}
}

func TestEntry_ConcurrentOutputWrites(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				entry.AppendOutput(fmt.Sprintf("goroutine-%d-line-%d", id, i))
			}
		}(g)
	}
	wg.Wait()

	lines := entry.OutputLines()
	if len(lines) != 1000 {
		t.Errorf("OutputLines() len = %d, want 1000", len(lines))
	}
}

func TestEntry_ConcurrentStateTransitions(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	var wg sync.WaitGroup
	// Multiple goroutines racing to CAS
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry.CompareAndSwapState(StateIdle, StateStarting)
		}()
	}
	wg.Wait()

	// Exactly one should have won the CAS
	if entry.State() != StateStarting {
		t.Errorf("State() = %v, want %v", entry.State(), StateStarting)
	}
}

func TestRegistry_IsolationByProject(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	entry1, _ := reg.Register("dev", "/project/a", cfg)
	entry2, _ := reg.Register("dev", "/project/b", cfg)

	if entry1 == entry2 {
		t.Error("different projects must have separate entries")
	}

	entry1.SetState(StateRunning)
	if entry2.State() != StateIdle {
		t.Errorf("entry2.State() = %v, want %v (state must be isolated per project)", entry2.State(), StateIdle)
	}
}

func TestEntry_Ownership(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Initially unowned
	if entry.Owner() != "" {
		t.Errorf("Owner() = %q, want empty", entry.Owner())
	}

	// Set owner
	entry.SetOwner("session-1")
	if entry.Owner() != "session-1" {
		t.Errorf("Owner() = %q, want %q", entry.Owner(), "session-1")
	}

	// Change owner
	entry.SetOwner("session-2")
	if entry.Owner() != "session-2" {
		t.Errorf("Owner() = %q, want %q", entry.Owner(), "session-2")
	}
}

func TestEntry_ObserverCount(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	if entry.ObserverCount() != 0 {
		t.Errorf("ObserverCount() = %d, want 0", entry.ObserverCount())
	}

	entry.AddSession("s1")
	if entry.ObserverCount() != 1 {
		t.Errorf("ObserverCount() = %d, want 1", entry.ObserverCount())
	}

	entry.AddSession("s2")
	entry.AddSession("s3")
	if entry.ObserverCount() != 3 {
		t.Errorf("ObserverCount() = %d, want 3", entry.ObserverCount())
	}

	entry.RemoveSession("s2")
	if entry.ObserverCount() != 2 {
		t.Errorf("ObserverCount() = %d, want 2", entry.ObserverCount())
	}
}

func TestEntry_TransferOwnership(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.SetOwner("owner-1")
	entry.AddSession("observer-a")
	entry.AddSession("observer-b")

	// Transfer ownership should pick one of the remaining observers
	newOwner := entry.TransferOwnership()
	if newOwner == "" {
		t.Fatal("TransferOwnership() returned empty string, want an observer")
	}
	if entry.Owner() != newOwner {
		t.Errorf("Owner() = %q, want %q", entry.Owner(), newOwner)
	}
	if newOwner != "observer-a" && newOwner != "observer-b" {
		t.Errorf("TransferOwnership() = %q, want observer-a or observer-b", newOwner)
	}
}

func TestEntry_TransferOwnershipNoObservers(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	entry.SetOwner("owner-1")

	// No observers -- transfer clears ownership
	newOwner := entry.TransferOwnership()
	if newOwner != "" {
		t.Errorf("TransferOwnership() = %q, want empty", newOwner)
	}
	if entry.Owner() != "" {
		t.Errorf("Owner() = %q, want empty", entry.Owner())
	}
}

func TestEntry_ConcurrentStartProtection(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Simulate sessions racing to start the same script via CAS
	var wg sync.WaitGroup
	winners := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if entry.CompareAndSwapState(StateIdle, StateStarting) {
				winners <- id
			}
		}(i)
	}
	wg.Wait()
	close(winners)

	// Exactly one goroutine should win the CAS
	var winnerIDs []int
	for id := range winners {
		winnerIDs = append(winnerIDs, id)
	}
	if len(winnerIDs) != 1 {
		t.Errorf("CAS winners = %d, want 1", len(winnerIDs))
	}
	if entry.State() != StateStarting {
		t.Errorf("State() = %v, want %v", entry.State(), StateStarting)
	}
}

func TestEntry_OwnershipAtomicity(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}
	entry, _ := reg.Register("dev", "/home/user/myapp", cfg)

	// Concurrent ownership changes should not panic or corrupt
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			code := fmt.Sprintf("session-%d", id)
			entry.SetOwner(code)
			entry.Owner()
		}(i)
	}
	wg.Wait()

	// Owner should be one of the sessions
	owner := entry.Owner()
	if owner == "" {
		t.Error("Owner() = empty after concurrent writes, want a session")
	}
}

func TestRegistry_Remove(t *testing.T) {
	reg := NewRegistry()
	cfg := &Config{Run: "npm start"}

	_, err := reg.Register("dev", "/project", cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	_, err = reg.Register("test", "/project", cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Remove one entry
	if !reg.Remove("dev", "/project") {
		t.Error("Remove should return true for existing entry")
	}

	// Verify it's gone
	_, ok := reg.Get("dev", "/project")
	if ok {
		t.Error("Get should return false after Remove")
	}

	// Other entry still exists
	_, ok = reg.Get("test", "/project")
	if !ok {
		t.Error("Remove should not affect other entries")
	}

	// Removing again returns false
	if reg.Remove("dev", "/project") {
		t.Error("Remove should return false for already-removed entry")
	}

	// List should only contain "test"
	entries := reg.List("/project")
	if len(entries) != 1 || entries[0].Name != "test" {
		t.Errorf("List after Remove: got %d entries, want 1 (test)", len(entries))
	}
}
