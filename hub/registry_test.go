package hub

import (
	"context"
	"testing"

	"github.com/standardbeagle/go-cli-server/protocol"
)

func TestCommandRegistryRegister(t *testing.T) {
	reg := NewCommandRegistry()

	handler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}

	err := reg.Register(CommandDefinition{
		Verb:        "TEST",
		SubVerbs:    []string{"ACTION1", "ACTION2"},
		Handler:     handler,
		Description: "Test command",
	})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Verify verb is registered
	if !reg.HasVerb("TEST") {
		t.Error("expected TEST to be registered")
	}
	if !reg.HasVerb("test") {
		t.Error("expected lowercase test to be registered (case-insensitive)")
	}
}

func TestCommandRegistryMultipleCommands(t *testing.T) {
	reg := NewCommandRegistry()

	handler1 := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}
	handler2 := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}

	reg.Register(CommandDefinition{
		Verb:    "CMD1",
		Handler: handler1,
	})
	reg.Register(CommandDefinition{
		Verb:    "CMD2",
		Handler: handler2,
	})

	// Both should be registered
	if !reg.HasVerb("CMD1") {
		t.Error("expected CMD1 to be registered")
	}
	if !reg.HasVerb("CMD2") {
		t.Error("expected CMD2 to be registered")
	}
}

func TestCommandRegistryRegisterEmpty(t *testing.T) {
	reg := NewCommandRegistry()

	err := reg.Register(CommandDefinition{
		Verb: "",
	})
	if err == nil {
		t.Error("expected error for empty verb")
	}
}

func TestCommandRegistryRegisterNilHandler(t *testing.T) {
	reg := NewCommandRegistry()

	err := reg.Register(CommandDefinition{
		Verb:    "TEST",
		Handler: nil,
	})
	if err == nil {
		t.Error("expected error for nil handler")
	}
}

func TestCommandDefinition(t *testing.T) {
	def := CommandDefinition{
		Verb:        "MYVERB",
		SubVerbs:    []string{"SUB1", "SUB2"},
		Description: "My test verb",
	}

	if def.Verb != "MYVERB" {
		t.Errorf("Verb = %q, want %q", def.Verb, "MYVERB")
	}
	if len(def.SubVerbs) != 2 {
		t.Errorf("len(SubVerbs) = %d, want 2", len(def.SubVerbs))
	}
	if def.Description != "My test verb" {
		t.Errorf("Description = %q, want %q", def.Description, "My test verb")
	}
}

func TestCommandRegistryValidSubVerbs(t *testing.T) {
	reg := NewCommandRegistry()

	handler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}

	reg.Register(CommandDefinition{
		Verb:     "PROC",
		SubVerbs: []string{"STATUS", "OUTPUT", "LIST"},
		Handler:  handler,
	})

	subVerbs := reg.ValidSubVerbs("PROC")
	if len(subVerbs) != 3 {
		t.Errorf("len(ValidSubVerbs) = %d, want 3", len(subVerbs))
	}

	// Check unknown verb returns nil
	subVerbs = reg.ValidSubVerbs("UNKNOWN")
	if subVerbs != nil {
		t.Error("expected nil for unknown verb")
	}
}

func TestCommandRegistryRegisterSubHandler(t *testing.T) {
	reg := NewCommandRegistry()

	mainHandler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}
	subHandler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}

	// Register main verb
	reg.Register(CommandDefinition{
		Verb:    "TEST",
		Handler: mainHandler,
	})

	// Add sub-handler
	err := reg.RegisterSubHandler("TEST", "SPECIAL", subHandler)
	if err != nil {
		t.Fatalf("RegisterSubHandler error: %v", err)
	}

	// Try to add sub-handler to non-existent verb
	err = reg.RegisterSubHandler("NONEXISTENT", "SUB", subHandler)
	if err == nil {
		t.Error("expected error for non-existent verb")
	}
}

func TestCommandRegistryHasVerb(t *testing.T) {
	reg := NewCommandRegistry()

	handler := func(ctx context.Context, conn *Connection, cmd *protocol.Command) error {
		return nil
	}

	// Should not have verb before registration
	if reg.HasVerb("TEST") {
		t.Error("should not have TEST before registration")
	}

	reg.Register(CommandDefinition{
		Verb:    "TEST",
		Handler: handler,
	})

	// Should have verb after registration
	if !reg.HasVerb("TEST") {
		t.Error("should have TEST after registration")
	}

	// Case insensitive
	if !reg.HasVerb("test") {
		t.Error("HasVerb should be case insensitive")
	}
}
