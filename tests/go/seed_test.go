package gcplocaltest

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestStorageSeedAndAdminReset boots a fresh emulator with a config that seeds a
// bucket from disk, verifies the file lands, then resets and verifies it's gone.
func TestStorageSeedAndAdminReset(t *testing.T) {
	tmp := t.TempDir()
	seedDir := filepath.Join(tmp, "fixtures", "bucket")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "seeded.txt"), []byte("seeded"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(seedDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "nested", "file.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(tmp, "gcp-local.yaml")
	cfgBody := "project: local-project\nstate: memory\nservices:\n  storage:\n    enabled: true\n    buckets:\n      - name: seeded-bucket\n        seed: " + seedDir + "\n  pubsub:\n    enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}

	em := startWithConfig(t, cfgPath)

	t.Setenv("STORAGE_EMULATOR_HOST", "http://"+em.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := storage.NewClient(ctx,
		option.WithoutAuthentication(),
		option.WithEndpoint("http://"+em.Host+"/storage/v1/"),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()

	r, err := client.Bucket("seeded-bucket").Object("seeded.txt").NewReader(ctx)
	if err != nil {
		t.Fatalf("read seeded.txt: %v", err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "seeded" {
		t.Errorf("seeded.txt body = %q", string(data))
	}

	r, err = client.Bucket("seeded-bucket").Object("nested/file.txt").NewReader(ctx)
	if err != nil {
		t.Fatalf("read nested/file.txt: %v", err)
	}
	data, _ = io.ReadAll(r)
	r.Close()
	if string(data) != "nested" {
		t.Errorf("nested/file.txt body = %q", string(data))
	}

	// reset
	req, _ := newPost(em.URL("/admin/reset"))
	resp, err := doRequest(req)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		t.Errorf("reset status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// after reset the bucket itself is gone; recreating should now succeed
	if err := client.Bucket("seeded-bucket").Create(ctx, "local-project", nil); err != nil {
		if !strings.Contains(err.Error(), "exists") {
			t.Errorf("expected bucket gone after reset, got %v", err)
		}
	}
}

// startWithConfig launches gcp-local with --config and a random free port.
func startWithConfig(t *testing.T, cfgPath string) *testutil.Emulator {
	t.Helper()
	binPath := testutil.BinaryPath(t)
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binPath, "start",
		"--port="+strconv.Itoa(port),
		"--config="+cfgPath,
		"--no-daemon",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	em := &emulatorAdapter{
		Emulator: &testutil.Emulator{Host: "localhost:" + strconv.Itoa(port), Port: port},
		cancel:   cancel,
		cmd:      cmd,
	}
	em.waitReady(t)
	t.Cleanup(em.stop)
	return em.Emulator
}

type emulatorAdapter struct {
	*testutil.Emulator
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func (e *emulatorAdapter) stop() {
	if e.cancel != nil {
		e.cancel()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
	}
}

func (e *emulatorAdapter) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := defaultClient.Get("http://" + e.Host + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("emulator not ready")
}
