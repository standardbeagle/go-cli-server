package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler_Schedule(t *testing.T) {
	var delivered atomic.Int32

	s, err := New(Config{
		TickInterval:    10 * time.Millisecond,
		DeliveryTimeout: 100 * time.Millisecond,
		MaxRetries:      3,
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			delivered.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Schedule a task for immediate delivery
	task, err := s.Schedule("session-1", 1*time.Millisecond, "hello", "/project")
	if err != nil {
		t.Fatal(err)
	}

	if task.ID == "" {
		t.Error("expected task ID to be set")
	}
	if task.Status != TaskStatusPending {
		t.Errorf("expected status pending, got %s", task.Status)
	}

	// Wait for delivery
	time.Sleep(50 * time.Millisecond)

	if delivered.Load() != 1 {
		t.Errorf("expected 1 delivery, got %d", delivered.Load())
	}

	info := s.Info()
	if info.TotalScheduled != 1 {
		t.Errorf("expected 1 scheduled, got %d", info.TotalScheduled)
	}
	if info.TotalDelivered != 1 {
		t.Errorf("expected 1 delivered, got %d", info.TotalDelivered)
	}
}

func TestScheduler_Cancel(t *testing.T) {
	s, err := New(Config{
		TickInterval: 1 * time.Second, // Slow tick so task won't be delivered
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			t.Error("task should not be delivered after cancellation")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Schedule a task far in the future
	task, err := s.Schedule("session-1", 1*time.Hour, "hello", "/project")
	if err != nil {
		t.Fatal(err)
	}

	// Cancel it
	if err := s.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}

	// Verify it's gone
	if _, ok := s.Get(task.ID); ok {
		t.Error("expected task to be removed after cancellation")
	}

	info := s.Info()
	if info.TotalCancelled != 1 {
		t.Errorf("expected 1 cancelled, got %d", info.TotalCancelled)
	}
}

func TestScheduler_Cancel_NotFound(t *testing.T) {
	s, err := New(Config{
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Cancel("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestScheduler_List(t *testing.T) {
	s, err := New(Config{
		TickInterval: 1 * time.Hour, // Slow tick
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Schedule tasks for different projects
	s.Schedule("session-1", 1*time.Hour, "msg1", "/project-a")
	s.Schedule("session-2", 1*time.Hour, "msg2", "/project-a")
	s.Schedule("session-3", 1*time.Hour, "msg3", "/project-b")

	// List all
	all := s.List("", true)
	if len(all) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(all))
	}

	// List by project
	projectA := s.List("/project-a", false)
	if len(projectA) != 2 {
		t.Errorf("expected 2 tasks for project-a, got %d", len(projectA))
	}

	projectB := s.List("/project-b", false)
	if len(projectB) != 1 {
		t.Errorf("expected 1 task for project-b, got %d", len(projectB))
	}
}

func TestScheduler_Validation(t *testing.T) {
	s, err := New(Config{
		DeliverFunc: func(ctx context.Context, targetID, payload string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Empty target
	_, err = s.Schedule("", 1*time.Hour, "msg", "/project")
	if err == nil {
		t.Error("expected error for empty target")
	}

	// Empty payload
	_, err = s.Schedule("target", 1*time.Hour, "", "/project")
	if err == nil {
		t.Error("expected error for empty payload")
	}

	// Non-positive duration
	_, err = s.Schedule("target", 0, "msg", "/project")
	if err == nil {
		t.Error("expected error for zero duration")
	}
}

func TestScheduler_RequiresDeliverFunc(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Error("expected error when DeliverFunc is not set")
	}
}
