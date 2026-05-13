package gcplocaltest

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestBoltDBPersistenceAcrossRestart starts the emulator with BoltDB state,
// creates a bucket and a secret, terminates the process, restarts pointing at
// the same DB file, and verifies the data is still there.
func TestBoltDBPersistenceAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "state.db")
	cfgPath := filepath.Join(tmp, "gcp-local.yaml")
	cfgBody := "project: local-project\nstate: boltdb\nstate_dir: " + dbPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}

	em1 := startEmulator(t, cfgPath)
	base := em1.URL("/v1/projects/" + project)

	resp, body := doJSON(t, http.MethodPost, base+"/secrets?secretId=stash", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create secret: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPost, em1.URL("/storage/v1/b?project="+project), map[string]any{"name": "persist-bucket"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket: %d %s", resp.StatusCode, body)
	}
	stopEmulator(em1)

	em2 := startEmulator(t, cfgPath)
	resp, body = doJSON(t, http.MethodGet, em2.URL("/v1/projects/"+project+"/secrets/stash"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("secret missing after restart: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodGet, em2.URL("/storage/v1/b/persist-bucket"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bucket missing after restart: %d %s", resp.StatusCode, body)
	}
}

func startEmulator(t *testing.T, cfgPath string) *managedEmulator {
	t.Helper()
	bin := testutil.BinaryPath(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "start",
		"--port="+strconv.Itoa(port),
		"--config="+cfgPath,
		"--no-daemon",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	em := &managedEmulator{
		Emulator: &testutil.Emulator{Host: "localhost:" + strconv.Itoa(port), Port: port},
		cancel:   cancel,
		cmd:      cmd,
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := defaultClient.Get("http://" + em.Host + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return em
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("not ready")
	return em
}

func stopEmulator(em *managedEmulator) {
	if em.cmd != nil && em.cmd.Process != nil {
		_ = em.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = em.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = em.cmd.Process.Kill()
			<-done
		}
	}
	if em.cancel != nil {
		em.cancel()
	}
}

type managedEmulator struct {
	*testutil.Emulator
	cancel context.CancelFunc
	cmd    *exec.Cmd
}
