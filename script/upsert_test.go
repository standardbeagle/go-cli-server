package script

import (
	"sync"
	"testing"
)

func TestUpsert_CreatesWhenAbsent(t *testing.T) {
	r := NewRegistry()

	entry, replaced, err := r.Upsert("dev", "/proj", &Config{Run: "npm run dev"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if replaced {
		t.Error("replaced = true, want false for a fresh entry")
	}
	if entry.Name != "dev" || entry.Config.Run != "npm run dev" {
		t.Errorf("entry = %+v, want name=dev run=%q", entry, "npm run dev")
	}
	if got, ok := r.Get("dev", "/proj"); !ok || got != entry {
		t.Error("Get() did not return the upserted entry")
	}
}

// An idempotent reload must not discard runtime state.
func TestUpsert_EqualConfigReusesEntry(t *testing.T) {
	r := NewRegistry()
	cfg := func() *Config {
		return &Config{Command: "go", Args: []string{"run", "."}, Env: map[string]string{"A": "1"}}
	}

	first, _, err := r.Upsert("api", "/proj", cfg())
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	first.SetState(StateRunning)
	first.IncrementStartCount()

	again, replaced, err := r.Upsert("api", "/proj", cfg())
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if replaced {
		t.Error("replaced = true, want false for an equal config")
	}
	if again != first {
		t.Error("Upsert() returned a new entry for an equal config")
	}
	if again.State() != StateRunning || again.StartCount() != 1 {
		t.Errorf("runtime state lost: state=%v startCount=%d", again.State(), again.StartCount())
	}
}

// Where Register errors, Upsert replaces — that is the whole point of it.
func TestUpsert_ReplacesOnChangedConfig(t *testing.T) {
	r := NewRegistry()

	if _, err := r.Register("dev", "/proj", &Config{Run: "vite"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := r.Register("dev", "/proj", &Config{Run: "vite --host"}); err == nil {
		t.Fatal("Register() with a changed config = nil error, want error")
	}

	entry, replaced, err := r.Upsert("dev", "/proj", &Config{Run: "vite --host"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if !replaced {
		t.Error("replaced = false, want true")
	}
	if entry.Config.Run != "vite --host" {
		t.Errorf("Config.Run = %q, want %q", entry.Config.Run, "vite --host")
	}
	if got, ok := r.Get("dev", "/proj"); !ok || got != entry {
		t.Error("registry still holds the stale entry")
	}
}

// Observers describe who is watching a script, not how it runs, so they survive
// a replacement. Dropping them would let the next session disconnect tear down
// a script another session still owns.
func TestUpsert_CarriesSessionsAndOwner(t *testing.T) {
	r := NewRegistry()

	entry, _, err := r.Upsert("web", "/proj", &Config{Run: "vite"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	entry.AddSession("sess-a")
	entry.AddSession("sess-b")
	entry.SetOwner("sess-a")
	entry.SetState(StateRunning)

	replacedEntry, replaced, err := r.Upsert("web", "/proj", &Config{Run: "vite --host"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if !replaced {
		t.Fatal("replaced = false, want true")
	}
	if got := replacedEntry.ObserverCount(); got != 2 {
		t.Errorf("ObserverCount() = %d, want 2", got)
	}
	if got := replacedEntry.Owner(); got != "sess-a" {
		t.Errorf("Owner() = %q, want sess-a", got)
	}
	// Runtime state described a process launched from a config that is gone.
	if got := replacedEntry.State(); got != StateIdle {
		t.Errorf("State() = %v, want StateIdle on a replaced entry", got)
	}
}

func TestUpsert_NormalizesProjectPath(t *testing.T) {
	r := NewRegistry()

	first, _, err := r.Upsert("dev", "/proj/sub/..", &Config{Run: "x"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	second, replaced, err := r.Upsert("dev", "/proj", &Config{Run: "x"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if replaced || second != first {
		t.Error("an unnormalized project path created a duplicate entry")
	}
}

func TestUpsert_RejectsEmptyKey(t *testing.T) {
	r := NewRegistry()

	if _, _, err := r.Upsert("", "/proj", &Config{}); err == nil {
		t.Error("Upsert() with empty name = nil error, want error")
	}
	if _, _, err := r.Upsert("dev", "", &Config{}); err == nil {
		t.Error("Upsert() with empty project path = nil error, want error")
	}
	if _, ok := r.Get("dev", ""); ok {
		t.Error("a rejected Upsert stored an entry")
	}
}

// Concurrent Upserts must converge on a single entry, and the registry must end
// up holding one of the configs that was actually written — no lost swap.
func TestUpsert_ConcurrentReplacementsConverge(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("dev", "/proj", &Config{Run: "original"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run := "changed-a"
			if i%2 == 0 {
				run = "changed-b"
			}
			if _, _, err := r.Upsert("dev", "/proj", &Config{Run: run}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Upsert() error = %v", err)
	}

	final, ok := r.Get("dev", "/proj")
	if !ok {
		t.Fatal("entry vanished under concurrent Upsert")
	}
	if final.Config.Run != "changed-a" && final.Config.Run != "changed-b" {
		t.Errorf("Config.Run = %q, want one of the concurrently written configs", final.Config.Run)
	}
	if n := len(r.List("/proj")); n != 1 {
		t.Errorf("List() = %d entries, want 1", n)
	}
}
