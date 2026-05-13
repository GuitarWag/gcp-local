package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestSecretManagerCRUDAndAccess(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/" + project

	// create
	resp, body := doJSON(t, http.MethodPost, base+"/secrets?secretId=api-key", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}

	// add version
	payload := base64.StdEncoding.EncodeToString([]byte("super-secret"))
	resp, body = doJSON(t, http.MethodPost, base+"/secrets/api-key:addVersion", map[string]any{
		"payload": map[string]any{"data": payload},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("addVersion: %d %s", resp.StatusCode, body)
	}

	// access latest
	resp, body = doJSON(t, http.MethodGet, base+"/secrets/api-key/versions/latest:access", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("access: %d %s", resp.StatusCode, body)
	}
	var ar struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &ar); err != nil {
		t.Fatalf("decode: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(ar.Payload.Data)
	if string(decoded) != "super-secret" {
		t.Errorf("payload = %q", string(decoded))
	}

	// list
	resp, body = doJSON(t, http.MethodGet, base+"/secrets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}

	// delete
	resp, body = doJSON(t, http.MethodDelete, base+"/secrets/api-key", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
}
