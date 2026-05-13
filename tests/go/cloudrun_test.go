package gcplocaltest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestCloudRunCreateAndInvoke(t *testing.T) {
	em := testutil.Start(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-From", "backend")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, r.Body)
	}))
	defer backend.Close()

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/services"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":       "svc1",
		"backendUrl": backend.URL,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create service: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/svc1/invoke", map[string]any{"hi": "there"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-From") != "backend" {
		t.Errorf("backend header not propagated, got %q", resp.Header.Get("X-From"))
	}
}
