package gcplocaltest

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestSeedScriptDrivesAllConsoleEndpoints runs scripts/seed.sh against a
// fresh emulator and then hits every /console/api/* endpoint, asserting
// that the seed produced the expected shape. The point is drift
// detection: when someone adds a new console page or renames a
// ConsoleX adapter, this test fails fast unless seed.sh is updated to
// match.
//
// This is deliberately one big test rather than many small ones —
// spinning up the emulator and shelling to bash is expensive enough
// (~10s wall time) that we share it across all assertions.
func TestSeedScriptDrivesAllConsoleEndpoints(t *testing.T) {
	em := testutil.Start(t)

	repo := repoRootFromTest(t)
	scriptPath := filepath.Join(repo, "scripts", "seed.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("seed script missing: %v", err)
	}

	cmd := exec.Command("bash", scriptPath, em.URL(""), "--quiet")
	cmd.Dir = repo
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("seed.sh failed: %v", err)
	}

	// Logging — 5 logs × 12 entries.
	expectArrayCount(t, em, "/console/api/logging/entries?limit=500", "entries", 50)

	// Storage — buckets, objects in a known bucket, preview body.
	expectArrayCount(t, em, "/console/api/storage/buckets", "buckets", 5)
	expectArrayCount(t, em, "/console/api/storage/objects?bucket=app-assets", "objects", 4)
	expectField(t, em, "/console/api/storage/preview?bucket=app-assets&object=styles.css", "body", func(v any) bool {
		s, _ := v.(string)
		return strings.Contains(s, "font-family")
	})

	// Pub/Sub — 5 topics, 9 subs, peek returns messages.
	expectArrayCount(t, em, "/console/api/pubsub/topics", "topics", 5)
	expectArrayCount(t, em, "/console/api/pubsub/subscriptions", "subscriptions", 9)
	expectArrayCount(t,
		em,
		"/console/api/pubsub/peek?subscription=projects/local-project/subscriptions/orders-fulfillment&limit=20",
		"messages", 12)

	// Firestore — 5 collections + 1 subcollection; users has 8 docs.
	expectArrayCount(t, em, "/console/api/firestore/collections", "collections", 6)
	expectArrayCount(t, em, "/console/api/firestore/documents?collection=users", "documents", 8)
	expectField(t, em,
		"/console/api/firestore/document?name=projects/local-project/databases/(default)/documents/users/u_1001",
		"fields", func(v any) bool {
			m, ok := v.(map[string]any)
			return ok && len(m) > 0
		})

	// Secret Manager — 10 secrets.
	expectArrayCount(t, em, "/console/api/secrets/secrets", "secrets", 10)
	expectArrayCount(t, em,
		"/console/api/secrets/versions?secret=projects/local-project/secrets/stripe-api-key",
		"versions", 1)

	// Cloud Tasks — 3 queues, 6 tasks in 'default'.
	expectArrayCount(t, em, "/console/api/tasks/queues", "queues", 3)
	expectArrayCount(t, em,
		"/console/api/tasks/tasks?queue=projects/local-project/locations/us-central1/queues/default",
		"tasks", 6)

	// Cloud Scheduler — 6 jobs.
	expectArrayCount(t, em, "/console/api/scheduler/jobs", "jobs", 6)

	// KMS — 3 keyrings, 3 keys in app-keys, encrypt/decrypt round trip.
	expectArrayCount(t, em, "/console/api/kms/keyrings", "keyRings", 3)
	expectArrayCount(t, em,
		"/console/api/kms/cryptokeys?keyring=projects/local-project/locations/global/keyRings/app-keys",
		"cryptoKeys", 3)

	keyName := "projects/local-project/locations/global/keyRings/app-keys/cryptoKeys/api-token-key"
	encResp, encBody := doJSON(t, http.MethodPost,
		em.URL("/console/api/kms/encrypt?key="+keyName),
		map[string]any{"plaintext": "round-trip"})
	if encResp.StatusCode != http.StatusOK {
		t.Fatalf("kms encrypt: %d %s", encResp.StatusCode, encBody)
	}
	var enc struct{ Ciphertext string }
	_ = json.Unmarshal(encBody, &enc)
	if enc.Ciphertext == "" {
		t.Fatal("kms encrypt returned empty ciphertext")
	}
	decResp, decBody := doJSON(t, http.MethodPost,
		em.URL("/console/api/kms/decrypt?key="+keyName),
		map[string]any{"ciphertext": enc.Ciphertext})
	if decResp.StatusCode != http.StatusOK {
		t.Fatalf("kms decrypt: %d %s", decResp.StatusCode, decBody)
	}
	var dec struct{ Plaintext string }
	_ = json.Unmarshal(decBody, &dec)
	if dec.Plaintext != "round-trip" {
		t.Errorf("kms round-trip: got %q want %q", dec.Plaintext, "round-trip")
	}

	// Monitoring — 4 metrics with 5 points each.
	expectArrayCount(t, em, "/console/api/monitoring/series", "series", 4)

	// BigQuery — 2 datasets, 2 tables in analytics, ad-hoc query.
	expectArrayCount(t, em, "/console/api/bigquery/datasets", "datasets", 2)
	expectArrayCount(t, em, "/console/api/bigquery/tables?dataset=analytics", "tables", 2)
	qResp, qBody := doJSON(t, http.MethodPost,
		em.URL("/console/api/bigquery/query"),
		map[string]any{"query": "SELECT 1+1 AS two"})
	if qResp.StatusCode != http.StatusOK {
		t.Fatalf("bigquery query: %d %s", qResp.StatusCode, qBody)
	}
	var q struct {
		Columns []string         `json:"columns"`
		Rows    []map[string]any `json:"rows"`
	}
	_ = json.Unmarshal(qBody, &q)
	if len(q.Rows) == 0 || len(q.Columns) == 0 {
		t.Errorf("bigquery query returned no rows/columns: %+v", q)
	}

	// Cloud SQL — 3 instances.
	expectArrayCount(t, em, "/console/api/cloudsql/instances", "instances", 3)

	// Cloud Run + Functions — 4 of each.
	expectArrayCount(t, em, "/console/api/cloudrun/list", "resources", 4)
	expectArrayCount(t, em, "/console/api/functions/list", "resources", 4)

	// Stub services — surface the "stub" state via the status endpoint.
	expectField(t, em, "/console/api/bigtable/status", "state", func(v any) bool {
		s, _ := v.(string)
		return s == "stub"
	})
	expectField(t, em, "/console/api/spanner/status", "state", func(v any) bool {
		s, _ := v.(string)
		return s == "stub"
	})
}

// expectArrayCount fetches `path`, decodes the response, and fails if
// the named array key has fewer than `min` elements.
func expectArrayCount(t *testing.T, em *testutil.Emulator, path, key string, min int) {
	t.Helper()
	resp, err := http.Get(em.URL(path))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", path, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("%s: decode: %v\nbody=%s", path, err, body)
	}
	arr, ok := generic[key].([]any)
	if !ok {
		t.Fatalf("%s: key %q missing or not an array; body=%s", path, key, body)
	}
	if len(arr) < min {
		t.Errorf("%s: %s has %d entries, want >= %d", path, key, len(arr), min)
	}
}

// expectField fetches `path` and runs `check` against the named field.
func expectField(t *testing.T, em *testutil.Emulator, path, key string, check func(any) bool) {
	t.Helper()
	resp, err := http.Get(em.URL(path))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", path, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("%s: decode: %v\nbody=%s", path, err, body)
	}
	if !check(generic[key]) {
		t.Errorf("%s: field %q did not match expectation; body=%s", path, key, body)
	}
}

// repoRootFromTest finds the repo root by walking up from cwd looking
// for the top-level go.mod (the one alongside cmd/gcp-local). tests/go
// has its own go.mod, so we keep walking past the tests/go module.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "gcp-local")); err == nil {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("repo root not found from %s", wd)
	return ""
}
