package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestHealthzReturnsOK(t *testing.T) {
	em := testutil.Start(t)

	resp, err := http.Get(em.URL("/healthz"))
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Status   string            `json:"status"`
		Services map[string]string `json:"services"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status=ok, got %q", body.Status)
	}
	for _, name := range []string{"storage", "pubsub", "secretmanager", "tasks", "scheduler", "kms", "logging", "monitoring", "firestore", "bigquery", "bigtable", "spanner", "cloudsql", "cloudrun", "functions"} {
		if body.Services[name] != "ready" {
			t.Errorf("expected %s=ready, got %q", name, body.Services[name])
		}
	}
}
