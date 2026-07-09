package process

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// startPortBlocker starts a child that binds a port and ignores SIGTERM, so the
// kill path must escalate to SIGKILL — the same shape as a wedged dev server.
func startPortBlocker(t *testing.T) (pid, port int) {
	t.Helper()

	// Bind :0 to learn a free port, then hand it to the child by closing ours
	// first. The child re-binds and holds it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	script := fmt.Sprintf(
		`trap '' TERM; exec python3 -c 'import socket,time
s=socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", %d))
s.listen(1)
print("bound", flush=True)
time.sleep(300)'`, port)

	cmd := exec.Command("sh", "-c", script)
	// Own process group. The kill path escalates with SIGKILL to the whole group;
	// without this the blocker shares the test runner's group and takes it down.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Wait for the child to report that it holds the port.
	buf := make([]byte, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = stdout.Read(buf)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Skip("port blocker never bound (python3 unavailable?)")
	}

	return cmd.Process.Pid, port
}

// KillProcessByPort must free the port, not merely signal its owner. SIGKILL is
// delivered asynchronously and the kernel releases the listening socket only
// when the process exits, so returning at SIGKILL left the caller — a port
// preflight about to start a dev server — unable to bind the port it had just
// been told was freed.
func TestKillProcessByPort_PortIsBindableWhenItReturns(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("don't run as root")
	}
	_, port := startPortBlocker(t)

	// Sanity: the port really is taken.
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		ln.Close()
		t.Fatalf("port %d was never held", port)
	}

	pm := NewProcessManager(DefaultManagerConfig())
	defer func() { _ = pm.Shutdown(context.Background()) }()

	if _, err := pm.KillProcessByPort(context.Background(), port); err != nil {
		t.Fatalf("KillProcessByPort: %v", err)
	}

	// The moment it returns, the port must be bindable — no polling, because a
	// caller has no reason to poll a function that reports the port freed.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port still bound after KillProcessByPort returned: %v", err)
	}
	_ = ln.Close()
}

// KillProcessByPort must not return while its target is still alive. SIGKILL is
// delivered asynchronously, so returning immediately after signalling leaves the
// caller racing the kernel's teardown of the process — and of the listening
// socket it wanted freed.
func TestKillProcessByPort_TargetIsDeadWhenItReturns(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("don't run as root")
	}
	pid, port := startPortBlocker(t)

	pm := NewProcessManager(DefaultManagerConfig())
	defer func() { _ = pm.Shutdown(context.Background()) }()

	if _, err := pm.KillProcessByPort(context.Background(), port); err != nil {
		t.Fatalf("KillProcessByPort: %v", err)
	}

	// A zombie counts as dead: it has released its descriptors, and reaping it is
	// its parent's job, not the killer's.
	if !hasReleasedResources(pid) {
		t.Fatalf("pid %d still running when KillProcessByPort returned", pid)
	}
}
