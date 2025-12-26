package hub

import (
	"context"
	"testing"
	"time"
)

func TestSubprocessRouter_Register(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
		Commands: []string{
			"TEST *",
			"EXAMPLE GET",
		},
	}

	// Test successful registration
	err := router.Register(sp)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test duplicate registration
	err = router.Register(sp)
	if err == nil {
		t.Error("Expected error for duplicate registration, got nil")
	}

	// Verify subprocess can be retrieved
	retrieved, ok := router.Get("test-subprocess")
	if !ok {
		t.Error("Failed to retrieve registered subprocess")
	}
	if retrieved.ID != "test-subprocess" {
		t.Errorf("Retrieved subprocess ID = %s, want test-subprocess", retrieved.ID)
	}
}

func TestSubprocessRouter_RegisterEmptyID(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		Name: "No ID",
	}

	err := router.Register(sp)
	if err == nil {
		t.Error("Expected error for empty ID, got nil")
	}
}

func TestSubprocessRouter_Unregister(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
	}

	_ = router.Register(sp)

	// Test successful unregistration
	err := router.Unregister("test-subprocess")
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	// Verify subprocess is removed
	_, ok := router.Get("test-subprocess")
	if ok {
		t.Error("Subprocess still exists after unregistration")
	}

	// Test unregistering non-existent subprocess
	err = router.Unregister("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent subprocess, got nil")
	}
}

func TestSubprocessRouter_List(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	// Empty list
	list := router.List()
	if len(list) != 0 {
		t.Errorf("Expected empty list, got %d items", len(list))
	}

	// Add subprocesses
	sp1 := &ManagedSubprocess{ID: "sp1", Name: "Subprocess 1"}
	sp2 := &ManagedSubprocess{ID: "sp2", Name: "Subprocess 2"}

	_ = router.Register(sp1)
	_ = router.Register(sp2)

	list = router.List()
	if len(list) != 2 {
		t.Errorf("Expected 2 subprocesses, got %d", len(list))
	}
}

func TestSubprocessRouter_GetRoutes(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test",
		Commands: []string{
			"PROXY *",
			"SESSION GET",
			"EXACT MATCH",
		},
	}

	_ = router.Register(sp)

	routes := router.GetRoutes()

	// Check prefix route
	if _, ok := routes["PROXY *"]; !ok {
		t.Error("Missing PROXY * prefix route")
	}

	// Check exact routes
	if _, ok := routes["SESSION GET"]; !ok {
		t.Error("Missing SESSION GET exact route")
	}
	if _, ok := routes["EXACT MATCH"]; !ok {
		t.Error("Missing EXACT MATCH exact route")
	}
}

func TestSubprocessRouter_Stats(t *testing.T) {
	hub := New(Config{})
	router := NewSubprocessRouter(hub)

	sp := &ManagedSubprocess{
		ID:   "test-subprocess",
		Name: "Test Subprocess",
	}
	sp.state.Store(SubprocessPending)

	_ = router.Register(sp)

	stats := router.Stats()

	if stats.Total != 1 {
		t.Errorf("Total = %d, want 1", stats.Total)
	}
	if stats.Running != 0 {
		t.Errorf("Running = %d, want 0", stats.Running)
	}
	if len(stats.Subprocesses) != 1 {
		t.Errorf("Subprocesses count = %d, want 1", len(stats.Subprocesses))
	}
}

func TestManagedSubprocess_StateTransitions(t *testing.T) {
	sp := &ManagedSubprocess{
		ID:   "test",
		Name: "Test",
		Transport: SubprocessTransportConfig{
			Type:    "tcp",
			Address: "localhost:0", // Invalid, will fail to connect
		},
	}
	sp.state.Store(SubprocessPending)
	sp.ctx, sp.cancel = context.WithCancel(context.Background())

	// Start should fail with invalid transport
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := sp.start(ctx)
	if err == nil {
		t.Error("Expected start to fail with invalid address")
	}

	// State should be failed
	state := sp.state.Load().(ManagedSubprocessState)
	if state != SubprocessFailed {
		t.Errorf("State = %s, want %s", state, SubprocessFailed)
	}
}

func TestSubprocessTransportConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  SubprocessTransportConfig
		wantErr bool
	}{
		{
			name: "unix transport requires address",
			config: SubprocessTransportConfig{
				Type: "unix",
			},
			wantErr: true,
		},
		{
			name: "tcp transport requires address",
			config: SubprocessTransportConfig{
				Type: "tcp",
			},
			wantErr: true,
		},
		{
			name: "stdio transport requires command",
			config: SubprocessTransportConfig{
				Type: "stdio",
			},
			wantErr: true,
		},
		{
			name: "unsupported transport type",
			config: SubprocessTransportConfig{
				Type: "unknown",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &ManagedSubprocess{
				ID:        "test",
				Name:      "Test",
				Transport: tt.config,
			}
			sp.state.Store(SubprocessPending)
			sp.ctx, sp.cancel = context.WithCancel(context.Background())

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := sp.start(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("start() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultSubprocessHealthConfig(t *testing.T) {
	config := DefaultSubprocessHealthConfig()

	if !config.Enabled {
		t.Error("Expected Enabled = true by default")
	}
	if config.Interval != 10*time.Second {
		t.Errorf("Interval = %v, want 10s", config.Interval)
	}
	if config.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", config.Timeout)
	}
	if config.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", config.FailureThreshold)
	}
}
