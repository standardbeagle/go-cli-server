package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestScheduler_RestoresPersistedTasks verifies that pending tasks written to a
// project's state file are restored on startup (with the ID counter advanced so
// new tasks don't overwrite them).
func TestScheduler_RestoresPersistedTasks(t *testing.T) {
	projectDir := t.TempDir()
	sm := NewStateManager(DefaultStateConfig())

	// Persist a pending task directly, as a prior run would have.
	persisted := &Task{
		ID:          "task-7",
		TargetID:    "session-1",
		Payload:     "restored",
		DeliverAt:   time.Now().Add(time.Hour),
		CreatedAt:   time.Now(),
		ProjectPath: projectDir,
		Status:      TaskStatusPending,
	}
	if err := sm.SaveTask(persisted); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	// Fresh StateManager + scheduler, as after a restart.
	sm2 := NewStateManager(DefaultStateConfig())
	s, err := New(Config{
		TickInterval:   time.Hour, // never fire during the test
		StateManager:   sm2,
		StateScanPaths: []string{projectDir},
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	got, ok := s.Get("task-7")
	if !ok {
		t.Fatal("persisted task not restored")
	}
	if got.Payload != "restored" {
		t.Errorf("payload = %q, want restored", got.Payload)
	}

	// New task IDs must not collide with the restored task-7.
	newTask, err := s.Schedule("session-2", time.Hour, "fresh", projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if newTask.ID == "task-7" {
		t.Error("new task reused restored ID task-7")
	}
	if n, _ := parseTaskIDNum(newTask.ID); n <= 7 {
		t.Errorf("new task ID %q not advanced past restored max (7)", newTask.ID)
	}
}

// TestScheduler_NoRedelivery verifies a slow delivery is dispatched only once,
// not re-dispatched on every tick while it is in flight.
func TestScheduler_NoRedelivery(t *testing.T) {
	var calls atomic.Int32

	s, err := New(Config{
		TickInterval:    5 * time.Millisecond,
		DeliveryTimeout: time.Second,
		MaxRetries:      3,
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			calls.Add(1)
			time.Sleep(80 * time.Millisecond) // spans many ticks
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if _, err := s.Schedule("session-1", time.Millisecond, "hi", "/p"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(150 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Errorf("delivery dispatched %d times, want exactly 1", got)
	}
}
