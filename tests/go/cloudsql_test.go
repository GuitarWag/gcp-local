package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestCloudSQLInstanceCRUD(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/sql/v1beta4/projects/" + project + "/instances"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":            "main",
		"databaseVersion": "POSTGRES_15",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, base+"/main", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d %s", resp.StatusCode, body)
	}
	var inst struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &inst)
	if inst.Name != "main" || inst.State != "RUNNABLE" {
		t.Errorf("instance = %+v", inst)
	}

	resp, body = doJSON(t, http.MethodDelete, base+"/main", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
}
