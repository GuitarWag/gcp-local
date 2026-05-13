package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestBigQueryDatasetTableQuery(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/bigquery/v2/projects/" + project

	// create dataset
	resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "ds1"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create dataset: %d %s", resp.StatusCode, body)
	}

	// create table
	resp, body = doJSON(t, http.MethodPost, base+"/datasets/ds1/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "t1"},
		"schema": map[string]any{
			"fields": []map[string]any{
				{"name": "id", "type": "INTEGER"},
				{"name": "label", "type": "STRING"},
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create table: %d %s", resp.StatusCode, body)
	}

	// insertAll
	resp, body = doJSON(t, http.MethodPost, base+"/datasets/ds1/tables/t1/insertAll", map[string]any{
		"rows": []map[string]any{
			{"json": map[string]any{"id": 1, "label": "alpha"}},
			{"json": map[string]any{"id": 2, "label": "beta"}},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("insertAll: %d %s", resp.StatusCode, body)
	}

	// query
	resp, body = doJSON(t, http.MethodPost, base+"/queries", map[string]any{
		"query": "SELECT label FROM ds1.t1 ORDER BY id",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query: %d %s", resp.StatusCode, body)
	}
	var qr struct {
		Rows []struct {
			F []struct {
				V string `json:"v"`
			} `json:"f"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(body, &qr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(qr.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(qr.Rows))
	}
	if qr.Rows[0].F[0].V != "alpha" || qr.Rows[1].F[0].V != "beta" {
		t.Errorf("rows = %+v", qr.Rows)
	}
}
