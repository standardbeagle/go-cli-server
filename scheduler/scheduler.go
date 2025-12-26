// Package scheduler provides scheduled task delivery for CLI servers.
//
// The scheduler allows tasks to be scheduled for future delivery with
// configurable retry logic, persistence, and delivery callbacks.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TaskStatus represents the current state of a scheduled task.
type TaskStatus string

const (
	// TaskStatusPending indicates the task is waiting to be delivered.
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusDelivered indicates the task was successfully delivered.
	TaskStatusDelivered TaskStatus = "delivered"
	// TaskStatusFailed indicates the task failed after max retries.
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCancelled indicates the task was cancelled.
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task represents a scheduled task for future delivery.
type Task struct {
	ID          string     `json:"id"`                   // Unique task ID (e.g., "task-abc123")
	TargetID    string     `json:"target_id"`            // Target identifier (e.g., session code)
	Payload     string     `json:"payload"`              // Payload to deliver (e.g., message)
	DeliverAt   time.Time  `json:"deliver_at"`           // Scheduled delivery time
	CreatedAt   time.Time  `json:"created_at"`           // When task was created
	ProjectPath string     `json:"project_path"`         // For project-scoped filtering
	Status      TaskStatus `json:"status"`               // Current status
	Attempts    int        `json:"attempts"`             // Delivery attempts
	LastError   string     `json:"last_error,omitempty"` // Last delivery error
}

// ToMap returns the task as a map for JSON serialization.
func (t *Task) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":           t.ID,
		"target_id":    t.TargetID,
		"payload":      t.Payload,
		"deliver_at":   t.DeliverAt.Format(time.RFC3339),
		"created_at":   t.CreatedAt.Format(time.RFC3339),
		"project_path": t.ProjectPath,
		"status":       string(t.Status),
		"attempts":     t.Attempts,
		"last_error":   t.LastError,
	}
}

// DeliverFunc is the callback function for task delivery.
// It receives the target ID and payload, and returns an error if delivery fails.
// The error message will be stored in the task's LastError field.
type DeliverFunc func(ctx context.Context, targetID, payload string) error

// ValidateTargetFunc validates that a target exists before scheduling.
// Returns nil if valid, error with reason if invalid.
type ValidateTargetFunc func(targetID string) error

// Config configures the scheduler.
type Config struct {
	// TickInterval is how often the scheduler checks for due tasks.
	// Default: 1 second
	TickInterval time.Duration
	// MaxRetries is the maximum number of delivery attempts.
	// Default: 3
	MaxRetries int
	// RetryDelay is the base delay between retries (exponential backoff).
	// Default: 5 seconds
	RetryDelay time.Duration
	// DeliveryTimeout is the timeout for each delivery attempt.
	// Default: 5 seconds
	DeliveryTimeout time.Duration
	// DeliverFunc is the callback for delivering tasks.
	// Required.
	DeliverFunc DeliverFunc
	// ValidateTarget validates targets before scheduling (optional).
	ValidateTarget ValidateTargetFunc
	// StateManager handles task persistence (optional).
	StateManager *StateManager
}

// DefaultConfig returns sensible defaults (DeliverFunc must still be set).
func DefaultConfig() Config {
	return Config{
		TickInterval:    1 * time.Second,
		MaxRetries:      3,
		RetryDelay:      5 * time.Second,
		DeliveryTimeout: 5 * time.Second,
	}
}

// Scheduler manages scheduled task delivery.
type Scheduler struct {
	config Config

	// Task storage (sync.Map for lock-free access)
	tasks sync.Map // map[string]*Task

	// Lifecycle management
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool

	// Statistics (atomics)
	totalScheduled atomic.Int64
	totalDelivered atomic.Int64
	totalFailed    atomic.Int64
	totalCancelled atomic.Int64

	// Task ID counter
	nextTaskID atomic.Int64
}

// New creates a new scheduler with the given configuration.
func New(config Config) (*Scheduler, error) {
	if config.DeliverFunc == nil {
		return nil, fmt.Errorf("DeliverFunc is required")
	}

	// Apply defaults for zero values
	if config.TickInterval == 0 {
		config.TickInterval = 1 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 5 * time.Second
	}
	if config.DeliveryTimeout == 0 {
		config.DeliveryTimeout = 5 * time.Second
	}

	return &Scheduler{
		config: config,
	}, nil
}

// Start begins the scheduler's tick loop.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("scheduler already started")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true

	// Load persisted tasks if state manager is configured
	if s.config.StateManager != nil {
		tasks := s.config.StateManager.LoadAllTasks()
		for _, task := range tasks {
			if task.Status == TaskStatusPending {
				s.tasks.Store(task.ID, task)
			}
		}
	}

	s.wg.Add(1)
	go s.run()

	return nil
}

// Stop stops the scheduler gracefully.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.cancel()
	s.mu.Unlock()

	s.wg.Wait()

	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
}

// run is the main scheduler loop.
func (s *Scheduler) run() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkDueTasks()
		}
	}
}

// checkDueTasks checks for and delivers due tasks.
func (s *Scheduler) checkDueTasks() {
	now := time.Now()
	s.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		if task.Status == TaskStatusPending && task.DeliverAt.Before(now) {
			// Attempt delivery in a goroutine
			go s.deliverTask(task)
		}
		return true
	})
}

// deliverTask attempts to deliver a scheduled task.
func (s *Scheduler) deliverTask(task *Task) {
	// Create delivery context with timeout
	ctx, cancel := context.WithTimeout(s.ctx, s.config.DeliveryTimeout)
	defer cancel()

	// Attempt delivery
	err := s.config.DeliverFunc(ctx, task.TargetID, task.Payload)
	if err != nil {
		task.Attempts++
		task.LastError = err.Error()

		if task.Attempts >= s.config.MaxRetries {
			task.Status = TaskStatusFailed
			s.totalFailed.Add(1)
			s.removeTaskFromStorage(task)
		} else {
			// Schedule retry with exponential backoff
			backoff := s.config.RetryDelay * time.Duration(1<<uint(task.Attempts-1))
			task.DeliverAt = time.Now().Add(backoff)
			s.persistTask(task)
		}
		return
	}

	// Success!
	task.Status = TaskStatusDelivered
	s.totalDelivered.Add(1)
	s.removeTaskFromStorage(task)
}

// persistTask saves the task state to persistent storage.
func (s *Scheduler) persistTask(task *Task) {
	if s.config.StateManager != nil {
		s.config.StateManager.SaveTask(task)
	}
}

// removeTaskFromStorage removes a completed/failed/cancelled task.
func (s *Scheduler) removeTaskFromStorage(task *Task) {
	s.tasks.Delete(task.ID)
	if s.config.StateManager != nil {
		s.config.StateManager.RemoveTask(task.ID, task.ProjectPath)
	}
}

// Schedule adds a new task to the scheduler.
func (s *Scheduler) Schedule(targetID string, duration time.Duration, payload string, projectPath string) (*Task, error) {
	if targetID == "" {
		return nil, fmt.Errorf("target ID is required")
	}
	if payload == "" {
		return nil, fmt.Errorf("payload is required")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}

	// Validate target if validator is configured
	if s.config.ValidateTarget != nil {
		if err := s.config.ValidateTarget(targetID); err != nil {
			return nil, fmt.Errorf("invalid target: %w", err)
		}
	}

	taskID := fmt.Sprintf("task-%d", s.nextTaskID.Add(1))
	now := time.Now()

	task := &Task{
		ID:          taskID,
		TargetID:    targetID,
		Payload:     payload,
		DeliverAt:   now.Add(duration),
		CreatedAt:   now,
		ProjectPath: projectPath,
		Status:      TaskStatusPending,
		Attempts:    0,
	}

	s.tasks.Store(task.ID, task)
	s.totalScheduled.Add(1)
	s.persistTask(task)

	return task, nil
}

// Cancel cancels a scheduled task.
func (s *Scheduler) Cancel(taskID string) error {
	val, ok := s.tasks.Load(taskID)
	if !ok {
		return fmt.Errorf("task %q not found", taskID)
	}

	task := val.(*Task)
	if task.Status != TaskStatusPending {
		return fmt.Errorf("task %q is not pending (status: %s)", taskID, task.Status)
	}

	task.Status = TaskStatusCancelled
	s.totalCancelled.Add(1)
	s.removeTaskFromStorage(task)

	return nil
}

// Get retrieves a task by ID.
func (s *Scheduler) Get(taskID string) (*Task, bool) {
	val, ok := s.tasks.Load(taskID)
	if !ok {
		return nil, false
	}
	return val.(*Task), true
}

// List returns all tasks, optionally filtered by project path.
func (s *Scheduler) List(projectPath string, global bool) []*Task {
	var result []*Task
	s.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		if global || projectPath == "" || task.ProjectPath == projectPath {
			result = append(result, task)
		}
		return true
	})
	return result
}

// ListPending returns only pending tasks.
func (s *Scheduler) ListPending(projectPath string, global bool) []*Task {
	var result []*Task
	s.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		if task.Status == TaskStatusPending {
			if global || projectPath == "" || task.ProjectPath == projectPath {
				result = append(result, task)
			}
		}
		return true
	})
	return result
}

// Info contains statistics about the scheduler.
type Info struct {
	TotalScheduled int64 `json:"total_scheduled"`
	TotalDelivered int64 `json:"total_delivered"`
	TotalFailed    int64 `json:"total_failed"`
	TotalCancelled int64 `json:"total_cancelled"`
	PendingCount   int64 `json:"pending_count"`
}

// Info returns statistics about the scheduler.
func (s *Scheduler) Info() Info {
	// Count pending tasks
	var pendingCount int64
	s.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		if task.Status == TaskStatusPending {
			pendingCount++
		}
		return true
	})

	return Info{
		TotalScheduled: s.totalScheduled.Load(),
		TotalDelivered: s.totalDelivered.Load(),
		TotalFailed:    s.totalFailed.Load(),
		TotalCancelled: s.totalCancelled.Load(),
		PendingCount:   pendingCount,
	}
}
