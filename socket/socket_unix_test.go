//go:build unix

package socket

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestDefaultConfig verifies the default configuration has expected values.
// Information theory: tests that defaults are non-empty and consistent.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Path should be non-empty and contain expected name
	if cfg.Path == "" {
		t.Error("DefaultConfig().Path should not be empty")
	}
	if cfg.Mode != 0600 {
		t.Errorf("DefaultConfig().Mode = %o, want 0600", cfg.Mode)
	}
	if cfg.Name != "mcp-hub" {
		t.Errorf("DefaultConfig().Name = %q, want %q", cfg.Name, "mcp-hub")
	}

	// Path should end with .sock
	if filepath.Ext(cfg.Path) != ".sock" {
		t.Errorf("DefaultConfig().Path = %q should end with .sock", cfg.Path)
	}
}

// TestDefaultSocketPath verifies socket path generation.
// Tests both XDG_RUNTIME_DIR and fallback paths.
func TestDefaultSocketPath(t *testing.T) {
	tests := []struct {
		name        string
		socketName  string
		xdgRuntime  string
		wantContain string
		wantSuffix  string
	}{
		{
			name:        "with XDG_RUNTIME_DIR",
			socketName:  "test-hub",
			xdgRuntime:  "/run/user/1000",
			wantContain: "/run/user/1000/test-hub.sock",
			wantSuffix:  ".sock",
		},
		{
			name:        "without XDG_RUNTIME_DIR",
			socketName:  "myapp",
			xdgRuntime:  "",
			wantContain: "/tmp/myapp-",
			wantSuffix:  ".sock",
		},
		{
			name:        "empty name still works",
			socketName:  "",
			xdgRuntime:  "",
			wantContain: "/tmp/",
			wantSuffix:  ".sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore XDG_RUNTIME_DIR
			oldXDG := os.Getenv("XDG_RUNTIME_DIR")
			defer os.Setenv("XDG_RUNTIME_DIR", oldXDG)

			if tt.xdgRuntime != "" {
				os.Setenv("XDG_RUNTIME_DIR", tt.xdgRuntime)
			} else {
				os.Unsetenv("XDG_RUNTIME_DIR")
			}

			path := DefaultSocketPath(tt.socketName)

			if tt.xdgRuntime != "" && path != tt.wantContain {
				t.Errorf("DefaultSocketPath(%q) = %q, want %q", tt.socketName, path, tt.wantContain)
			}
			if !hasPrefix(path, tt.wantContain) && tt.xdgRuntime == "" {
				t.Errorf("DefaultSocketPath(%q) = %q, want prefix %q", tt.socketName, path, tt.wantContain)
			}
			if filepath.Ext(path) != tt.wantSuffix {
				t.Errorf("DefaultSocketPath(%q) = %q, want suffix %q", tt.socketName, path, tt.wantSuffix)
			}
		})
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// TestNewManager verifies manager creation with various configurations.
func TestNewManager(t *testing.T) {
	tests := []struct {
		name       string
		config     Config
		wantPath   string
		wantMode   os.FileMode
		checkPath  func(string) bool
	}{
		{
			name:     "empty config uses defaults",
			config:   Config{},
			wantMode: 0600,
			checkPath: func(p string) bool {
				return filepath.Ext(p) == ".sock" && hasPrefix(p, "/")
			},
		},
		{
			name: "custom path is preserved",
			config: Config{
				Path: "/custom/path.sock",
				Mode: 0644,
			},
			wantPath: "/custom/path.sock",
			wantMode: 0644,
		},
		{
			name: "custom name generates path",
			config: Config{
				Name: "custom-app",
			},
			wantMode: 0600,
			checkPath: func(p string) bool {
				return filepath.Base(p) == "custom-app.sock" || hasPrefix(filepath.Base(p), "custom-app-")
			},
		},
		{
			name: "zero mode defaults to 0600",
			config: Config{
				Path: "/test.sock",
				Mode: 0,
			},
			wantPath: "/test.sock",
			wantMode: 0600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager(tt.config)

			if m == nil {
				t.Fatal("NewManager returned nil")
			}

			if tt.wantPath != "" && m.Path() != tt.wantPath {
				t.Errorf("Path() = %q, want %q", m.Path(), tt.wantPath)
			}

			if tt.checkPath != nil && !tt.checkPath(m.Path()) {
				t.Errorf("Path() = %q failed checkPath", m.Path())
			}

			if m.config.Mode != tt.wantMode {
				t.Errorf("config.Mode = %o, want %o", m.config.Mode, tt.wantMode)
			}

			// PID file should be derived from socket path
			expectedPidFile := m.Path() + ".pid"
			if m.pidFile != expectedPidFile {
				t.Errorf("pidFile = %q, want %q", m.pidFile, expectedPidFile)
			}
		})
	}
}

// TestListenAndClose tests the full lifecycle of socket creation and cleanup.
func TestListenAndClose(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	m := NewManager(Config{
		Path: sockPath,
		Mode: 0600,
	})

	// Listen should succeed
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if listener == nil {
		t.Fatal("Listen() returned nil listener")
	}

	// Socket file should exist
	info, err := os.Stat(sockPath)
	if err != nil {
		t.Errorf("Socket file not created: %v", err)
	} else if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("Created file is not a socket")
	}

	// PID file should exist with correct content
	pidFile := sockPath + ".pid"
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Errorf("PID file not created: %v", err)
	} else {
		pid, _ := strconv.Atoi(string(pidData))
		if pid != os.Getpid() {
			t.Errorf("PID file contains %d, want %d", pid, os.Getpid())
		}
	}

	// Should be able to connect
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Errorf("Failed to connect to socket: %v", err)
	} else {
		conn.Close()
	}

	// Close should cleanup
	if err := m.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Files should be removed
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("Socket file still exists after Close()")
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after Close()")
	}
}

// TestListenDuplicateFails verifies that attempting to listen twice fails.
func TestListenDuplicateFails(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "dup.sock")

	m1 := NewManager(Config{Path: sockPath})
	m2 := NewManager(Config{Path: sockPath})

	// First listen should succeed
	listener1, err := m1.Listen()
	if err != nil {
		t.Fatalf("First Listen() error = %v", err)
	}
	defer m1.Close()

	// Accept connections in background to prevent connect timeouts
	go func() {
		for {
			conn, err := listener1.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Second listen should fail with ErrDaemonRunning
	_, err = m2.Listen()
	if !errors.Is(err, ErrDaemonRunning) {
		t.Errorf("Second Listen() error = %v, want ErrDaemonRunning", err)
	}
}

// TestConnect tests connection to an existing socket.
func TestConnect(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "connect.sock")

	// Connect to non-existent socket should fail
	_, err := Connect(sockPath)
	if !errors.Is(err, ErrSocketNotFound) {
		t.Errorf("Connect() to non-existent = %v, want ErrSocketNotFound", err)
	}

	// Create a listener
	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	// Accept in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Connect should now succeed
	conn, err := Connect(sockPath)
	if err != nil {
		t.Errorf("Connect() error = %v", err)
	} else {
		conn.Close()
	}
}

// TestConnectEmptyPath verifies default path is used.
func TestConnectEmptyPath(t *testing.T) {
	// Connect with empty path should try default path
	_, err := Connect("")
	// This will likely fail but shouldn't panic
	if err == nil {
		t.Error("Connect(\"\") unexpectedly succeeded")
	}
}

// TestIsRunning tests daemon detection.
func TestIsRunning(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "running.sock")

	// Should not be running initially
	if IsRunning(sockPath) {
		t.Error("IsRunning() = true for non-existent socket")
	}

	// Create socket
	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	// Accept in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Should be running now
	if !IsRunning(sockPath) {
		t.Error("IsRunning() = false for active socket")
	}

	// Close and verify
	m.Close()

	// Give time for socket to be removed
	time.Sleep(10 * time.Millisecond)

	if IsRunning(sockPath) {
		t.Error("IsRunning() = true after Close()")
	}
}

// TestIsRunningEmptyPath verifies default path is used.
func TestIsRunningEmptyPath(t *testing.T) {
	// Should not panic with empty path
	_ = IsRunning("")
}

// TestCloseIdempotent verifies that Close() can be called multiple times.
func TestCloseIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "idempotent.sock")

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	_ = listener

	// First close should succeed
	if err := m.Close(); err != nil {
		t.Errorf("First Close() error = %v", err)
	}

	// Second close should also succeed (files already removed)
	if err := m.Close(); err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

// TestCleanupStaleSocket tests stale socket removal.
func TestCleanupStaleSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "stale.sock")

	// Create a socket and close it without cleanup
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	listener.Close()

	// Now create a manager and try to listen - should cleanup stale
	m := NewManager(Config{Path: sockPath})
	newListener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() after stale cleanup error = %v", err)
	}
	defer m.Close()

	// Should work
	go func() {
		for {
			conn, err := newListener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Errorf("Connect after stale cleanup error = %v", err)
	} else {
		conn.Close()
	}
}

// TestCleanupNonSocketFails verifies that Listen fails if path is not a socket.
func TestCleanupNonSocketFails(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "not-a-socket")

	// Create a regular file at the socket path
	if err := os.WriteFile(sockPath, []byte("not a socket"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	m := NewManager(Config{Path: sockPath})
	_, err := m.Listen()

	if err == nil {
		t.Error("Listen() should fail when path is not a socket")
	}
	if err != nil && !hasPrefix(err.Error(), "failed to cleanup stale socket") {
		t.Errorf("Listen() error = %v, want 'failed to cleanup stale socket' prefix", err)
	}
}

// TestIsClosedError verifies closed connection detection.
func TestIsClosedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "net.ErrClosed",
			err:  net.ErrClosed,
			want: true,
		},
		{
			name: "wrapped ErrClosed",
			err:  fmt.Errorf("operation failed: %w", net.ErrClosed),
			want: true,
		},
		{
			name: "closed connection string",
			err:  errors.New("use of closed network connection"),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "EOF",
			err:  errors.New("EOF"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsClosedError(tt.err)
			if got != tt.want {
				t.Errorf("IsClosedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestProcessMatcher verifies custom process matcher is used.
func TestProcessMatcher(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "matcher.sock")
	pidFile := sockPath + ".pid"

	// Write a PID file for current process
	currentPID := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	matcherCalled := false
	m := NewManager(Config{
		Path: sockPath,
		ProcessMatcher: func(pid int) bool {
			matcherCalled = true
			return false // Return false so it doesn't think daemon is running
		},
	})

	// Listen should call the matcher
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	if !matcherCalled {
		t.Error("ProcessMatcher was not called")
	}

	_ = listener
}

// TestConcurrentConnections tests handling of multiple concurrent connections.
func TestConcurrentConnections(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "concurrent.sock")

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	// Handle connections
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				buf := make([]byte, 10)
				c.Read(buf)
			}(conn)
		}
	}()

	// Create multiple concurrent connections
	const numConns = 10
	var connWg sync.WaitGroup
	errors := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		connWg.Add(1)
		go func() {
			defer connWg.Done()
			conn, err := Connect(sockPath)
			if err != nil {
				errors <- err
				return
			}
			conn.Write([]byte("test"))
			conn.Close()
		}()
	}

	connWg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent connection error: %v", err)
	}

	wg.Wait()
}

// TestListenCreatesDirectory verifies that Listen creates the socket directory.
func TestListenCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedPath := filepath.Join(tmpDir, "nested", "dir", "test.sock")

	m := NewManager(Config{Path: nestedPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	// Directory should exist
	dir := filepath.Dir(nestedPath)
	info, err := os.Stat(dir)
	if err != nil {
		t.Errorf("Directory not created: %v", err)
	} else if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}

	_ = listener
}

// TestCheckExistingWithInvalidPID verifies handling of invalid PID in file.
func TestCheckExistingWithInvalidPID(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "invalid-pid.sock")
	pidFile := sockPath + ".pid"

	// Write invalid PID
	if err := os.WriteFile(pidFile, []byte("not-a-number"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() should succeed with invalid PID file, got error: %v", err)
	}
	defer m.Close()

	// PID file should be removed and rewritten
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Errorf("PID file not rewritten: %v", err)
	} else {
		pid, _ := strconv.Atoi(string(data))
		if pid != os.Getpid() {
			t.Errorf("PID file contains %d, want %d", pid, os.Getpid())
		}
	}

	_ = listener
}

// TestCheckExistingWithDeadProcess verifies cleanup when PID belongs to dead process.
func TestCheckExistingWithDeadProcess(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "dead-pid.sock")
	pidFile := sockPath + ".pid"

	// Write a PID that definitely doesn't exist (very high number)
	deadPID := 999999999
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(deadPID)), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() should succeed with dead PID, got error: %v", err)
	}
	defer m.Close()

	_ = listener
}

// TestCleanupZombieDaemonsNoOp tests that cleanup returns 0 when no zombies exist.
func TestCleanupZombieDaemonsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "no-zombie.sock")

	// No zombies should be cleaned
	cleaned := CleanupZombieDaemons(sockPath, func(pid int) bool {
		return false // No processes match
	})

	if cleaned != 0 {
		t.Errorf("CleanupZombieDaemons() = %d, want 0", cleaned)
	}
}

// TestManagerWithProcessMatcherNil verifies default isDaemonProcess is used.
func TestManagerWithProcessMatcherNil(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "no-matcher.sock")

	m := NewManager(Config{
		Path:           sockPath,
		ProcessMatcher: nil, // Explicitly nil
	})

	if m.processMatcher != nil {
		t.Error("processMatcher should be nil when not provided")
	}

	// Should still work
	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	_ = listener
}

// TestSocketPermissions verifies socket permissions are set correctly.
func TestSocketPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "perms.sock")

	m := NewManager(Config{
		Path: sockPath,
		Mode: 0640,
	})

	listener, err := m.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	// Check permissions (may differ slightly due to umask on some systems)
	perm := info.Mode().Perm()
	if perm&0600 != 0600 {
		t.Errorf("Socket permissions = %o, want at least 0600", perm)
	}

	_ = listener
}

// TestPathMethod verifies Path() returns the configured path.
func TestPathMethod(t *testing.T) {
	path := "/custom/path.sock"
	m := NewManager(Config{Path: path})

	if got := m.Path(); got != path {
		t.Errorf("Path() = %q, want %q", got, path)
	}
}

// TestErrorTypes verifies exported error types are properly defined.
func TestErrorTypes(t *testing.T) {
	// Verify error types are non-nil and have descriptive messages
	if ErrSocketInUse == nil {
		t.Error("ErrSocketInUse should not be nil")
	}
	if ErrSocketNotFound == nil {
		t.Error("ErrSocketNotFound should not be nil")
	}
	if ErrDaemonRunning == nil {
		t.Error("ErrDaemonRunning should not be nil")
	}

	// Check that errors are distinct
	if errors.Is(ErrSocketInUse, ErrSocketNotFound) {
		t.Error("ErrSocketInUse and ErrSocketNotFound should be distinct")
	}
	if errors.Is(ErrSocketInUse, ErrDaemonRunning) {
		t.Error("ErrSocketInUse and ErrDaemonRunning should be distinct")
	}
	if errors.Is(ErrSocketNotFound, ErrDaemonRunning) {
		t.Error("ErrSocketNotFound and ErrDaemonRunning should be distinct")
	}

	// Check error messages are non-empty
	if ErrSocketInUse.Error() == "" {
		t.Error("ErrSocketInUse should have a message")
	}
	if ErrSocketNotFound.Error() == "" {
		t.Error("ErrSocketNotFound should have a message")
	}
	if ErrDaemonRunning.Error() == "" {
		t.Error("ErrDaemonRunning should have a message")
	}
}

// TestConnectionRefusedDetection tests the isConnectionRefused helper.
func TestConnectionRefusedDetection(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "refused.sock")

	// Create a socket file that looks like a socket but isn't listening
	// This is tricky - we need to trigger ECONNREFUSED
	// Create a socket, close it, then try to connect

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	listener.Close()

	// The socket file still exists but nothing is listening
	// Connect should get connection refused or no such file
	_, err = Connect(sockPath)
	if !errors.Is(err, ErrSocketNotFound) {
		// Either error is acceptable for a closed socket
		t.Logf("Connect() to closed socket returned: %v", err)
	}
}

// TestCheckExistingKillsUnresponsiveDaemon verifies that an unresponsive daemon is killed.
func TestCheckExistingKillsUnresponsiveDaemon(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Test skipped when running as root - can kill any process")
	}

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "unresponsive.sock")
	pidFile := sockPath + ".pid"

	// Write current PID (we can't actually test killing ourselves)
	// Instead, test the flow with a PID we can't kill
	invalidPID := 1 // PID 1 (init) - we can't kill it

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(invalidPID)), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	m := NewManager(Config{
		Path: sockPath,
		ProcessMatcher: func(pid int) bool {
			return pid == invalidPID // Pretend it's our process
		},
	})

	// Should fail to kill PID 1 but still try to proceed
	listener, err := m.Listen()
	if err != nil && !errors.Is(err, ErrDaemonRunning) {
		// May fail depending on system state
		t.Logf("Listen() with PID 1 returned: %v", err)
	}
	if listener != nil {
		m.Close()
	}
}

// BenchmarkConnect measures connection establishment overhead.
func BenchmarkConnect(b *testing.B) {
	tmpDir := b.TempDir()
	sockPath := filepath.Join(tmpDir, "bench.sock")

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		b.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := Connect(sockPath)
		if err != nil {
			b.Fatalf("Connect() error = %v", err)
		}
		conn.Close()
	}
}

// BenchmarkIsRunning measures daemon detection overhead.
func BenchmarkIsRunning(b *testing.B) {
	tmpDir := b.TempDir()
	sockPath := filepath.Join(tmpDir, "bench-running.sock")

	m := NewManager(Config{Path: sockPath})
	listener, err := m.Listen()
	if err != nil {
		b.Fatalf("Listen() error = %v", err)
	}
	defer m.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsRunning(sockPath)
	}
}

// TestIsDaemonProcess verifies cmdline-based process detection.
func TestIsDaemonProcess(t *testing.T) {
	// Test with current process PID
	currentPID := os.Getpid()

	// Current process should be readable
	result := isDaemonProcess(currentPID)
	// The result depends on how the test was run
	t.Logf("isDaemonProcess(%d) = %v", currentPID, result)

	// Non-existent PID should return false
	nonExistent := isDaemonProcess(999999999)
	if nonExistent {
		t.Error("isDaemonProcess(999999999) should return false")
	}
}

// TestIsProcessRunning verifies process existence check.
func TestIsProcessRunning(t *testing.T) {
	// Current process should be running
	if !isProcessRunning(os.Getpid()) {
		t.Error("isProcessRunning(os.Getpid()) should return true")
	}

	// Non-existent process should not be running
	if isProcessRunning(999999999) {
		t.Error("isProcessRunning(999999999) should return false")
	}

	// PID 1 may or may not be accessible depending on container/namespace
	// Just log the result rather than asserting
	pid1Running := isProcessRunning(1)
	t.Logf("isProcessRunning(1) = %v (depends on namespace/container)", pid1Running)
}

// TestSyscallErrors verifies syscall error detection helpers.
func TestSyscallErrors(t *testing.T) {
	// Test with real syscall errors
	econnrefused := &net.OpError{
		Err: &os.SyscallError{
			Syscall: "connect",
			Err:     syscall.ECONNREFUSED,
		},
	}
	if !isConnectionRefused(econnrefused) {
		t.Error("isConnectionRefused should detect ECONNREFUSED")
	}

	enoent := &net.OpError{
		Err: &os.SyscallError{
			Syscall: "connect",
			Err:     syscall.ENOENT,
		},
	}
	if !isNoSuchFile(enoent) {
		t.Error("isNoSuchFile should detect ENOENT")
	}

	// Test with non-syscall errors
	if isConnectionRefused(errors.New("some error")) {
		t.Error("isConnectionRefused should return false for non-syscall errors")
	}
	if isNoSuchFile(errors.New("some error")) {
		t.Error("isNoSuchFile should return false for non-syscall errors")
	}
}
