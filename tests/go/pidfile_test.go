package gcplocaltest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// pidfileTestHome returns a temp HOME so the pidfile lands at
// tmp/.gcp-local/gcp-local.pid and never touches the real one.
func pidfileTestHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func pidfilePath(home string) string {
	return filepath.Join(home, ".gcp-local", "gcp-local.pid")
}

// runPidCmd runs the binary with HOME pointed at home and returns the
// combined output plus the exit code.
func runPidCmd(t *testing.T, bin, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("%s: %v", args[0], err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), code
}

// processAlive mirrors pidfile.Alive: signal 0 on the pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// deadPID returns a pid that is guaranteed not alive: spawn a process,
// reap it, and retry in the unlikely event the pid was reused already.
func deadPID(t *testing.T) int {
	t.Helper()
	for i := 0; i < 10; i++ {
		cmd := exec.Command("true")
		if err := cmd.Start(); err != nil {
			t.Fatalf("spawn: %v", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Wait()
		if !processAlive(pid) {
			return pid
		}
	}
	t.Fatal("no reusable dead pid")
	return 0
}

// writePidfile writes a daemon-shaped pidfile into home.
func writePidfile(t *testing.T, home string, pid, port int, host string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".gcp-local"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": pid, "port": port, "host": host})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidfilePath(home), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// killRecordedDaemon reads home's pidfile and SIGTERMs the recorded pid if
// it is still alive, so no daemon survives a failed run.
func killRecordedDaemon(home string) {
	data, err := os.ReadFile(pidfilePath(home))
	if err != nil {
		return
	}
	var info struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &info) != nil || !processAlive(info.PID) {
		return
	}
	if proc, err := os.FindProcess(info.PID); err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && processAlive(info.PID) {
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPidfileDaemonLifecycle(t *testing.T) {
	bin := testutil.BinaryPath(t)
	home := pidfileTestHome(t)
	t.Cleanup(func() { killRecordedDaemon(home) })
	port := freePort(t)

	t.Run("start daemonized", func(t *testing.T) {
		out, code := runPidCmd(t, bin, home, "start", "--port="+strconv.Itoa(port))
		if code != 0 {
			t.Fatalf("start: exit %d, output: %s", code, out)
		}
	})

	t.Run("status ok", func(t *testing.T) {
		out, code := runPidCmd(t, bin, home, "status")
		if code != 0 {
			t.Fatalf("status: exit %d, output: %s", code, out)
		}
		if !strings.Contains(out, "status: ok") {
			t.Errorf("status output %q, want \"status: ok\"", out)
		}
	})

	t.Run("stop terminates", func(t *testing.T) {
		out, code := runPidCmd(t, bin, home, "stop")
		if code != 0 {
			t.Fatalf("stop: exit %d, output: %s", code, out)
		}
		if !strings.Contains(out, "terminated") {
			t.Errorf("stop output %q, want \"terminated\"", out)
		}
		if _, err := os.Stat(pidfilePath(home)); !os.IsNotExist(err) {
			t.Errorf("pidfile after stop: stat err %v, want not exist", err)
		}
	})
}

func TestPidfileStaleBranch(t *testing.T) {
	bin := testutil.BinaryPath(t)
	home := pidfileTestHome(t)
	t.Cleanup(func() { killRecordedDaemon(home) })

	writePidfile(t, home, deadPID(t), 1, "localhost:1")

	out, code := runPidCmd(t, bin, home, "status")
	if code != 1 {
		t.Errorf("stale status: exit %d, want 1 (output: %s)", code, out)
	}
	if !strings.Contains(out, "stale pidfile") {
		t.Errorf("stale status output %q, want \"stale pidfile\"", out)
	}

	out, code = runPidCmd(t, bin, home, "stop")
	if code != 0 {
		t.Errorf("stale stop: exit %d, want 0 (output: %s)", code, out)
	}
	if !strings.Contains(out, "stale pidfile removed") {
		t.Errorf("stale stop output %q, want \"stale pidfile removed\"", out)
	}
	if _, err := os.Stat(pidfilePath(home)); !os.IsNotExist(err) {
		t.Errorf("pidfile after stale stop: stat err %v, want not exist", err)
	}
}

func TestPidfileStatusNoPidfile(t *testing.T) {
	bin := testutil.BinaryPath(t)
	home := pidfileTestHome(t)

	out, code := runPidCmd(t, bin, home, "status")
	if code != 1 {
		t.Errorf("no-pidfile status: exit %d, want 1 (output: %s)", code, out)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("no-pidfile status output %q, want \"not running\"", out)
	}
}
