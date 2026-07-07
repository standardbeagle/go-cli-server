package client

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsGoTestBinary(t *testing.T) {
	sep := string(os.PathSeparator)
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "dotTestSuffix",
			path: filepath.Join("usr", "local", "bin", "mypkg.test"),
			want: true,
		},
		{
			name: "dotTestExeSuffix",
			path: filepath.Join("C:", "Users", "x", "mypkg.test.exe"),
			want: true,
		},
		{
			name: "goBuildTempDir",
			path: filepath.Join("tmp", "go-build1234567890", "b001", "daemon.test"),
			want: true,
		},
		{
			name: "goBuildTempDirExe",
			path: filepath.Join("tmp", "go-build1234567890", "b001", "daemon.test.exe"),
			want: true,
		},
		{
			name: "regularBinary",
			path: filepath.Join("usr", "local", "bin", "myapp"),
			want: false,
		},
		{
			name: "regularBinaryWithDashDaemon",
			path: filepath.Join("usr", "local", "bin", "myapp-daemon"),
			want: false,
		},
		{
			name: "empty",
			path: "",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize separators so the test works on both unix and windows.
			// The implementation uses os.PathSeparator, so we mirror it here.
			p := tc.path
			if sep != "/" {
				// filepath.Join already produced the right separator; nothing to do.
				_ = p
			}
			if got := isGoTestBinary(p); got != tc.want {
				t.Errorf("isGoTestBinary(%q) = %v, want %v", p, got, tc.want)
			}
		})
	}
}

func TestDefaultHubArgsUseHubFlags(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hub.sock")
	got := defaultHubArgs(socketPath)
	want := []string{"--socket", socketPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultHubArgs() = %#v, want %#v", got, want)
	}
}

func TestAcquireStartupLockCreatesParentDir(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "missing", "hub.sock.startup.lock")
	f, err := acquireStartupLock(lockPath)
	if err != nil {
		t.Fatalf("acquireStartupLock() error = %v", err)
	}
	releaseStartupLock(f, lockPath)

	if _, err := os.Stat(filepath.Dir(lockPath)); err != nil {
		t.Fatalf("lock parent dir was not created: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file should be released and removed, stat err = %v", err)
	}
}
