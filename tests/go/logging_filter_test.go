package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// writeLogEntries POSTs a batch of entries to entries:write and fails the test on non-200.
func writeLogEntries(t *testing.T, base string, payload map[string]any) {
	t.Helper()
	resp, body := doJSON(t, http.MethodPost, base+"/entries:write", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write: %d %s", resp.StatusCode, body)
	}
}

func listLogEntries(t *testing.T, base string, payload map[string]any) (int, []map[string]any, []byte) {
	t.Helper()
	resp, body := doJSON(t, http.MethodPost, base+"/entries:list", payload)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, body
	}
	var lr struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return resp.StatusCode, lr.Entries, body
}

func TestLoggingFilterBySeverity(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	logName := "projects/local-project/logs/sev-filter"
	writeLogEntries(t, base, map[string]any{
		"logName":  logName,
		"resource": map[string]any{"type": "global"},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "i"},
			{"severity": "WARNING", "textPayload": "w"},
			{"severity": "ERROR", "textPayload": "e"},
		},
	})

	status, entries, body := listLogEntries(t, base, map[string]any{
		"resourceNames": []string{"projects/local-project"},
		"filter":        `severity>=WARNING AND logName="` + logName + `"`,
	})
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (WARNING+ERROR), got %d: %s", len(entries), body)
	}
	for _, e := range entries {
		sev, _ := e["severity"].(string)
		if sev != "WARNING" && sev != "ERROR" {
			t.Errorf("unexpected severity %q in result", sev)
		}
	}
}

func TestLoggingFilterByLogName(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	a := "projects/local-project/logs/named-a"
	b := "projects/local-project/logs/named-b"
	writeLogEntries(t, base, map[string]any{
		"logName":  a,
		"resource": map[string]any{"type": "global"},
		"entries":  []map[string]any{{"severity": "INFO", "textPayload": "from-a"}},
	})
	writeLogEntries(t, base, map[string]any{
		"logName":  b,
		"resource": map[string]any{"type": "global"},
		"entries":  []map[string]any{{"severity": "INFO", "textPayload": "from-b"}},
	})

	status, entries, body := listLogEntries(t, base, map[string]any{
		"resourceNames": []string{"projects/local-project"},
		"filter":        `logName="` + a + `"`,
	})
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %s", len(entries), body)
	}
	if got, _ := entries[0]["logName"].(string); got != a {
		t.Errorf("logName = %q, want %q", got, a)
	}
}

func TestLoggingFilterByTimestamp(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	logName := "projects/local-project/logs/ts-filter"
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	recent := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	writeLogEntries(t, base, map[string]any{
		"logName":  logName,
		"resource": map[string]any{"type": "global"},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "old", "timestamp": old},
			{"severity": "INFO", "textPayload": "new", "timestamp": recent},
		},
	})

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	status, entries, body := listLogEntries(t, base, map[string]any{
		"resourceNames": []string{"projects/local-project"},
		"filter":        `logName="` + logName + `" AND timestamp>="` + cutoff + `"`,
	})
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after cutoff, got %d: %s", len(entries), body)
	}
	if txt, _ := entries[0]["textPayload"].(string); txt != "new" {
		t.Errorf("textPayload = %q, want %q", txt, "new")
	}
}

func TestLoggingFilterComposite(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	logName := "projects/local-project/logs/composite"
	writeLogEntries(t, base, map[string]any{
		"logName":  logName,
		"resource": map[string]any{"type": "k8s_container"},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "k8s-info"},
			{"severity": "WARNING", "textPayload": "k8s-warn"},
			{"severity": "ERROR", "textPayload": "k8s-err"},
		},
	})
	writeLogEntries(t, base, map[string]any{
		"logName":  logName + "-global",
		"resource": map[string]any{"type": "global"},
		"entries": []map[string]any{
			{"severity": "ERROR", "textPayload": "global-err"},
		},
	})

	status, entries, body := listLogEntries(t, base, map[string]any{
		"resourceNames": []string{"projects/local-project"},
		"filter":        `severity>=WARNING AND resource.type="k8s_container"`,
	})
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (k8s warn+err), got %d: %s", len(entries), body)
	}
	for _, e := range entries {
		res, _ := e["resource"].(map[string]any)
		typ, _ := res["type"].(string)
		if typ != "k8s_container" {
			t.Errorf("resource.type = %q, want k8s_container", typ)
		}
	}
}

func TestLoggingFilterInvalid(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v2"

	resp, body := doJSON(t, http.MethodPost, base+"/entries:list", map[string]any{
		"resourceNames": []string{"projects/local-project"},
		"filter":        "severity OR foo",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
	}
	var er struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("decode err: %v", err)
	}
	if er.Error.Code != http.StatusBadRequest {
		t.Errorf("error.code = %d, want 400", er.Error.Code)
	}
	if er.Error.Message == "" {
		t.Errorf("error.message should not be empty")
	}
}
