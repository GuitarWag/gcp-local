package testutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type Emulator struct {
	Host   string
	Port   int
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Start launches gcp-local on a random free port and waits until it's ready.
// Caller must have built a binary at the path returned by BinaryPath().
func Start(t *testing.T) *Emulator {
	t.Helper()
	binPath := BinaryPath(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, binPath, "start", "--port="+strconv.Itoa(port), "--no-daemon")
	// Discard the child's stdio. Inheriting the test runner's stderr keeps
	// the I/O pipes open after the child exits and trips go test's
	// WaitDelay under parallel CI execution.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("failed to start gcp-local: %v", err)
	}

	host := fmt.Sprintf("localhost:%d", port)
	if err := waitReady(host, 5*time.Second); err != nil {
		_ = cmd.Process.Kill()
		cancel()
		t.Fatalf("gcp-local not ready: %v", err)
	}
	em := &Emulator{Host: host, Port: port, cmd: cmd, cancel: cancel}
	t.Cleanup(em.Stop)
	return em
}

func (e *Emulator) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
	}
}

func (e *Emulator) URL(path string) string {
	return "http://" + e.Host + path
}

func waitReady(host string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + host + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("gcp-local not ready after %v", timeout)
}

var cachedBinPath string

// BinaryPath returns a path to a freshly built gcp-local binary, cached per test run.
func BinaryPath(t *testing.T) string {
	t.Helper()
	if cachedBinPath != "" {
		return cachedBinPath
	}
	tmpDir, err := os.MkdirTemp("", "gcp-local-bin-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	binPath := filepath.Join(tmpDir, "gcp-local")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/gcp-local")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build gcp-local: %v\n%s", err, out)
	}
	cachedBinPath = binPath
	return binPath
}

// repoRoot returns the gcp-local repo root assuming tests live at tests/go.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// from tests/go or tests/go/<subdir>, go up to repo root
	dir := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// check if this go.mod is the root module (has cmd/gcp-local)
			if _, err := os.Stat(filepath.Join(dir, "cmd", "gcp-local")); err == nil {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not find repo root from %s", wd)
	return ""
}
