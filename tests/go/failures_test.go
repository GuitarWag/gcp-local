package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestKMSTamperedCiphertextRejected(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project + "/locations/us/keyRings")
	if resp, _ := doJSON(t, http.MethodPost, base+"?keyRingId=r", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("ring: %d", resp.StatusCode)
	}
	if resp, _ := doJSON(t, http.MethodPost, base+"/r/cryptoKeys?cryptoKeyId=k", map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("key: %d", resp.StatusCode)
	}
	resp, body := doJSON(t, http.MethodPost, base+"/r/cryptoKeys/k:encrypt", map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString([]byte("classified")),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", resp.StatusCode, body)
	}
	var er struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.Unmarshal(body, &er)

	// Flip a byte
	raw, _ := base64.StdEncoding.DecodeString(er.Ciphertext)
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)

	resp, _ = doJSON(t, http.MethodPost, base+"/r/cryptoKeys/k:decrypt", map[string]any{
		"ciphertext": tampered,
	})
	if resp.StatusCode == http.StatusOK {
		t.Error("expected non-OK on tampered ciphertext")
	}
}

func TestCloudRunInvokeMissingBackend(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v2/projects/" + project + "/locations/loc/services")
	resp, body := doJSON(t, http.MethodPost, base, map[string]any{"name": "no-backend"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, http.MethodPost, base+"/no-backend/invoke", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 (no backendUrl), got %d", resp.StatusCode)
	}
}

func TestCloudRunInvokePropagates5xx(t *testing.T) {
	em := testutil.Start(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "oops", http.StatusInternalServerError)
	}))
	defer backend.Close()

	base := em.URL("/v2/projects/" + project + "/locations/loc/services")
	if resp, _ := doJSON(t, http.MethodPost, base, map[string]any{
		"name":       "broken",
		"backendUrl": backend.URL,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	resp, _ := doJSON(t, http.MethodPost, base+"/broken/invoke", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 propagated, got %d", resp.StatusCode)
	}
}

// silence unused
var _ = testutil.Start
