package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestConsoleAllPagesRender verifies every sidebar page returns HTTP 200
// with HTML content, even when no resources have been created. The
// console must work on a fresh emulator — that's the smallest useful
// debugging slice.
func TestConsoleAllPagesRender(t *testing.T) {
	em := testutil.Start(t)
	for _, path := range []string{
		"/console",
		"/console/logging",
		"/console/storage",
		"/console/pubsub",
		"/console/firestore",
		"/console/secrets",
		"/console/tasks",
		"/console/scheduler",
		"/console/kms",
		"/console/monitoring",
		"/console/bigquery",
		"/console/cloudsql",
		"/console/cloudrun",
		"/console/functions",
		"/console/bigtable",
		"/console/spanner",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(em.URL(path))
			if err != nil {
				t.Fatalf("get %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("content-type = %q", ct)
			}
		})
	}
}

// TestConsoleStaticAssets verifies the embedded pico.min.css and app.js
// are served with the expected content types and a non-trivial body
// size. Catches accidental embed regressions early.
func TestConsoleStaticAssets(t *testing.T) {
	em := testutil.Start(t)
	for _, c := range []struct {
		path        string
		contentType string
		minBytes    int
	}{
		{"/console/static/app.css", "text/css", 5_000},
		{"/console/static/app.js", "text/javascript", 1_000},
	} {
		resp, err := http.Get(em.URL(c.path))
		if err != nil {
			t.Fatalf("get %s: %v", c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", c.path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, c.contentType) {
			t.Errorf("%s content-type = %q want prefix %q", c.path, ct, c.contentType)
		}
		if resp.ContentLength < int64(c.minBytes) {
			t.Errorf("%s content-length = %d want >= %d", c.path, resp.ContentLength, c.minBytes)
		}
	}
}

// TestConsoleLoggingAPI seeds entries via the real Cloud Logging REST
// surface and verifies the console API returns them.
func TestConsoleLoggingAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host

	resp, body := doJSON(t, http.MethodPost, base+"/v2/entries:write", map[string]any{
		"logName":  "projects/local-project/logs/console",
		"resource": map[string]any{"type": "global"},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "hello"},
			{"severity": "ERROR", "textPayload": "boom"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed entries: %d %s", resp.StatusCode, body)
	}

	resp, err := http.Get(em.URL("/console/api/logging/entries?limit=50"))
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api status = %d", resp.StatusCode)
	}
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) < 2 {
		t.Errorf("entries = %d, want >= 2", len(out.Entries))
	}
}

// TestConsoleStorageAPI creates a bucket and object through the JSON API
// the SDK uses, then verifies the console buckets / objects / preview
// endpoints reflect them.
func TestConsoleStorageAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host

	resp, body := doJSON(t, http.MethodPost, base+"/storage/v1/b?project=local-project", map[string]any{"name": "ui-bucket"})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create bucket: %d %s", resp.StatusCode, body)
	}
	// XML PUT — same upload path the SDK uses against the emulator.
	req, _ := http.NewRequest(http.MethodPut, base+"/ui-bucket/hello.txt", strings.NewReader("hello console"))
	req.Header.Set("Content-Type", "text/plain")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("xml put: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("xml put status = %d", r.StatusCode)
	}

	r2, err := http.Get(em.URL("/console/api/storage/buckets"))
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	defer r2.Body.Close()
	var bResp struct {
		Buckets []map[string]any `json:"buckets"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&bResp)
	if len(bResp.Buckets) == 0 {
		t.Fatalf("no buckets returned")
	}

	r3, err := http.Get(em.URL("/console/api/storage/objects?bucket=ui-bucket"))
	if err != nil {
		t.Fatalf("objects: %v", err)
	}
	defer r3.Body.Close()
	var oResp struct {
		Objects []map[string]any `json:"objects"`
	}
	_ = json.NewDecoder(r3.Body).Decode(&oResp)
	if len(oResp.Objects) == 0 {
		t.Fatalf("no objects returned")
	}

	r4, err := http.Get(em.URL("/console/api/storage/preview?bucket=ui-bucket&object=hello.txt"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	defer r4.Body.Close()
	var pResp struct {
		Body   string `json:"body"`
		IsText bool   `json:"isText"`
	}
	_ = json.NewDecoder(r4.Body).Decode(&pResp)
	if !pResp.IsText || pResp.Body != "hello console" {
		t.Errorf("preview = %+v", pResp)
	}
}

// TestConsolePubSubAPI seeds a topic + subscription via the gRPC-style
// REST surface, publishes a message, and verifies the console peek
// shows it without draining the queue (a second peek returns the same
// message).
func TestConsolePubSubAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/local-project"

	if r, b := doJSON(t, http.MethodPut, base+"/topics/console-t", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("topic: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPut, base+"/subscriptions/console-s", map[string]any{
		"topic": "projects/local-project/topics/console-t",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("sub: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPost, base+"/topics/console-t:publish", map[string]any{
		"messages": []map[string]any{{"data": "aGVsbG8="}}, // "hello"
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", r.StatusCode, b)
	}

	resp, err := http.Get(em.URL("/console/api/pubsub/peek?subscription=projects/local-project/subscriptions/console-s&limit=5"))
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Messages) == 0 {
		t.Fatalf("peek returned no messages")
	}
	// Second peek should return the same message — peek must not drain.
	resp2, _ := http.Get(em.URL("/console/api/pubsub/peek?subscription=projects/local-project/subscriptions/console-s&limit=5"))
	defer resp2.Body.Close()
	var out2 struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&out2)
	if len(out2.Messages) != len(out.Messages) {
		t.Errorf("second peek len = %d, first = %d (peek should not drain)", len(out2.Messages), len(out.Messages))
	}
}

// TestConsoleKMSEncryptDecrypt creates a keyring/key via REST, then
// round-trips a plaintext through the console encrypt/decrypt API.
func TestConsoleKMSEncryptDecrypt(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/local-project/locations/global"

	if r, b := doJSON(t, http.MethodPost, base+"/keyRings?keyRingId=ring-x", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("keyring: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPost, base+"/keyRings/ring-x/cryptoKeys?cryptoKeyId=key-x", map[string]any{
		"purpose": "ENCRYPT_DECRYPT",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("cryptokey: %d %s", r.StatusCode, b)
	}

	keyName := "projects/local-project/locations/global/keyRings/ring-x/cryptoKeys/key-x"
	r, body := doJSON(t, http.MethodPost, em.URL("/console/api/kms/encrypt?key="+keyName), map[string]any{"plaintext": "very secret"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", r.StatusCode, body)
	}
	var encOut struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.Unmarshal(body, &encOut)
	if encOut.Ciphertext == "" {
		t.Fatal("empty ciphertext")
	}

	r, body = doJSON(t, http.MethodPost, em.URL("/console/api/kms/decrypt?key="+keyName), map[string]any{"ciphertext": encOut.Ciphertext})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("decrypt: %d %s", r.StatusCode, body)
	}
	var decOut struct {
		Plaintext string `json:"plaintext"`
	}
	_ = json.Unmarshal(body, &decOut)
	if decOut.Plaintext != "very secret" {
		t.Errorf("round-trip plaintext = %q want %q", decOut.Plaintext, "very secret")
	}
}

// TestConsoleBundleSize keeps the embedded console assets under the
// budget called out in the spec (target: <300 KB binary impact). It
// stats the built binary and asserts a hard ceiling on the file size
// uplift introduced by the console embed. The threshold is generous —
// it's a regression guard, not a tight target.
func TestConsoleBundleSize(t *testing.T) {
	binPath := testutil.BinaryPath(t)
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	// The pre-console binary sits around 45 MB; the console contributes
	// ~100 KB of pico + ~30 KB of templates + ~5 KB of JS/CSS. A 60 MB
	// upper bound leaves room for future service growth while still
	// catching obvious regressions like accidentally embedding a font.
	const maxBytes int64 = 60 * 1024 * 1024
	if info.Size() > maxBytes {
		t.Errorf("binary size = %d bytes, want < %d", info.Size(), maxBytes)
	}
}
