package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestBigQueryQueryReturnsTypedColumns verifies the query response schema
// reports the original BigQuery types (INTEGER, FLOAT, BOOLEAN, STRING)
// rather than coercing everything to STRING.
func TestBigQueryQueryReturnsTypedColumns(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	if resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "typed_ds"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("dataset: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/typed_ds/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "mixed"},
		"schema": map[string]any{
			"fields": []map[string]any{
				{"name": "id", "type": "INTEGER"},
				{"name": "ratio", "type": "FLOAT"},
				{"name": "active", "type": "BOOLEAN"},
				{"name": "label", "type": "STRING"},
			},
		},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("table: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/typed_ds/tables/mixed/insertAll", map[string]any{
		"rows": []map[string]any{{"json": map[string]any{"id": 1, "ratio": 1.5, "active": true, "label": "x"}}},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("insert: %d %s", resp.StatusCode, body)
	}

	resp, body := doJSON(t, http.MethodPost, base+"/queries", map[string]any{
		"query": "SELECT id, ratio, active, label FROM typed_ds.mixed",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query: %d %s", resp.StatusCode, body)
	}
	var qr struct {
		Schema struct {
			Fields []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(body, &qr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]string{
		"id":     "INTEGER",
		"ratio":  "FLOAT",
		"active": "BOOLEAN",
		"label":  "STRING",
	}
	if len(qr.Schema.Fields) != len(want) {
		t.Fatalf("expected %d fields, got %d (%+v)", len(want), len(qr.Schema.Fields), qr.Schema.Fields)
	}
	for _, f := range qr.Schema.Fields {
		if want[f.Name] != f.Type {
			t.Errorf("%s: got %s, want %s", f.Name, f.Type, want[f.Name])
		}
	}
}
