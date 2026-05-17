package gcplocaltest

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

var (
	fixtureBuildOnce sync.Once
	fixtureBuildPath string
	fixtureBuildErr  error
)

// buildEchoFixture compiles tests/go/fixtures/echofn into a temp binary and
// caches the path. The fixture's source lives next to the tests so the build
// runs against the same module as the test, with no extra wiring.
func buildEchoFixture(t *testing.T) string {
	t.Helper()
	fixtureBuildOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			fixtureBuildErr = err
			return
		}
		tmp, err := os.MkdirTemp("", "gcp-local-echofn-")
		if err != nil {
			fixtureBuildErr = err
			return
		}
		out := filepath.Join(tmp, "echofn")
		cmd := exec.Command("go", "build", "-o", out, "./fixtures/echofn")
		cmd.Dir = wd
		if msg, err := cmd.CombinedOutput(); err != nil {
			fixtureBuildErr = &buildErr{out: msg, err: err}
			return
		}
		fixtureBuildPath = out
	})
	if fixtureBuildErr != nil {
		t.Fatalf("build echofn fixture: %v", fixtureBuildErr)
	}
	return fixtureBuildPath
}

type buildErr struct {
	out []byte
	err error
}

func (b *buildErr) Error() string {
	return b.err.Error() + ": " + string(b.out)
}

func TestCloudRunSpawnsSubprocess(t *testing.T) {
	em := testutil.Start(t)
	bin := buildEchoFixture(t)

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/services"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":    "echo-svc",
		"command": []string{bin},
		"env":     map[string]string{"ECHO_TOKEN": "abc123"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create service: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/echo-svc/invoke", map[string]any{"hi": "there"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke: %d %s", resp.StatusCode, body)
	}

	pidHeader := resp.Header.Get("X-Fixture-Pid")
	if pidHeader == "" {
		t.Fatalf("missing X-Fixture-Pid header; body=%s", body)
	}
	pid, err := strconv.Atoi(pidHeader)
	if err != nil || pid <= 0 {
		t.Fatalf("X-Fixture-Pid %q not a positive int: %v", pidHeader, err)
	}
	if pid == os.Getpid() {
		t.Fatalf("response served by test process; emulator did not spawn a child")
	}
	if got := resp.Header.Get("X-K-Service"); got != "echo-svc" {
		t.Errorf("K_SERVICE env not propagated: got %q want echo-svc", got)
	}
	if got := resp.Header.Get("X-Echo-Token"); got != "abc123" {
		t.Errorf("custom env not propagated: got %q want abc123", got)
	}
	if !strings.Contains(string(body), "hello from pid") {
		t.Errorf("unexpected body: %s", body)
	}

	// A second invoke should reuse the same child process.
	resp2, body2 := doJSON(t, http.MethodPost, base+"/echo-svc/invoke", map[string]any{"hi": "again"})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second invoke: %d %s", resp2.StatusCode, body2)
	}
	if resp2.Header.Get("X-Fixture-Pid") != pidHeader {
		t.Errorf("child not reused: pid1=%s pid2=%s", pidHeader, resp2.Header.Get("X-Fixture-Pid"))
	}
}
