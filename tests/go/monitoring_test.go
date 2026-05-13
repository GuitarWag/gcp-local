package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestMonitoringCreateAndListTimeSeries(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v3/projects/" + project + "/timeSeries"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"timeSeries": []map[string]any{
			{
				"metric":   map[string]any{"type": "custom.googleapis.com/test"},
				"resource": map[string]any{"type": "global"},
				"points": []map[string]any{
					{"interval": map[string]any{"endTime": "2026-01-01T00:00:00Z"}, "value": map[string]any{"doubleValue": 1.5}},
				},
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, base, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	var lr struct {
		TimeSeries []map[string]any `json:"timeSeries"`
	}
	_ = json.Unmarshal(body, &lr)
	if len(lr.TimeSeries) < 1 {
		t.Errorf("expected ≥1 series, got %d", len(lr.TimeSeries))
	}
}
