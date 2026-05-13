package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestLoggingWriteAndList(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	resp, body := doJSON(t, http.MethodPost, base+"/entries:write", map[string]any{
		"logName": "projects/local-project/logs/test",
		"resource": map[string]any{
			"type": "global",
		},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "hello"},
			{"severity": "WARNING", "textPayload": "world"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/entries:list", map[string]any{
		"resourceNames": []string{"projects/local-project"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	var lr struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(lr.Entries) < 2 {
		t.Errorf("expected ≥2 entries, got %d", len(lr.Entries))
	}
}
