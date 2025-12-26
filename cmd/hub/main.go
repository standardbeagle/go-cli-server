// Package main provides the entry point for the go-cli-server hub.
//
// The hub is a shared development infrastructure daemon that enables multiple
// local applications (MCP servers, CLI tools, GUI apps) to share resources
// like code indexes, reverse proxies, SSH terminals, and managed processes.
//
// Usage:
//
//	# Start the hub with default settings
//	hub
//
//	# Start with custom socket path
//	hub --socket /tmp/my-hub.sock
//
//	# Start with verbose logging
//	hub --verbose
//
// Clients connect to the hub via Unix socket and send commands in the
// text protocol format. Subprocesses register with the hub to provide
// additional capabilities.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/standardbeagle/go-cli-server/hub"
	"github.com/standardbeagle/go-cli-server/process"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// Parse flags
	socketPath := flag.String("socket", "", "Unix socket path (default: auto-generated)")
	socketName := flag.String("name", "go-cli-server", "Socket name for auto-generated path")
	maxClients := flag.Int("max-clients", 0, "Maximum concurrent clients (0 = unlimited)")
	enableProc := flag.Bool("enable-proc", true, "Enable process management commands")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("go-cli-server hub %s (%s)\n", version, commit)
		os.Exit(0)
	}

	// Configure logging
	if *verbose {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags)
	}

	// Create hub configuration
	config := hub.Config{
		SocketPath:        *socketPath,
		SocketName:        *socketName,
		MaxClients:        *maxClients,
		EnableProcessMgmt: *enableProc,
		Version:           version,
		ProcessConfig:     process.DefaultManagerConfig(),
	}

	// Create and start hub
	h := hub.New(config)

	if err := h.Start(); err != nil {
		log.Fatalf("Failed to start hub: %v", err)
	}

	log.Printf("Hub started on %s", h.SocketPath())
	if *verbose {
		log.Printf("Version: %s (%s)", version, commit)
		log.Printf("Max clients: %d (0=unlimited)", *maxClients)
		log.Printf("Process management: %v", *enableProc)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("Received %s, shutting down...", sig)

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Stop(ctx); err != nil {
		log.Printf("Error during shutdown: %v", err)
		os.Exit(1)
	}

	log.Println("Hub shutdown complete")
}
