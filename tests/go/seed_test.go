package gcplocaltest

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
	data2, _ := io.ReadAll(r)
	r.Close()
	if string(data2) != "nested" {
		t.Errorf("nested/file.txt body = %q", string(data2))
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

// startWithConfig launches gcp-local with --config on a random free port.
// It goes through testutil.StartArgsEnv so coverage builds get GOCOVERDIR
// and a SIGINT shutdown that flushes counters.
func startWithConfig(t *testing.T, cfgPath string) *testutil.Emulator {
	t.Helper()
	return testutil.StartArgsEnv(t, nil, "--config="+cfgPath)
}
