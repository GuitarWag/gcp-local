package gcplocaltest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// bqRows decodes a /queries response into a slice of cell-value slices.
type bqRows struct {
	Rows []struct {
		F []struct {
			V string `json:"v"`
		} `json:"f"`
	} `json:"rows"`
}

func bqQuery(t *testing.T, base, q string) bqRows {
	t.Helper()
	resp, body := doJSON(t, http.MethodPost, base+"/queries", map[string]any{"query": q})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query %q: %d %s", q, resp.StatusCode, body)
	}
	var out bqRows
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	return out
}

// seedAdvanced creates two datasets with related tables used by the advanced
// query tests below. It is isolated per test by dataset prefix.
func seedAdvanced(t *testing.T, base, dsID string) {
	t.Helper()
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": dsID},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create dataset: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/"+dsID+"/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "users"},
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "id", "type": "INTEGER"},
			{"name": "name", "type": "STRING"},
		}},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create users table: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/"+dsID+"/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "orders"},
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "id", "type": "INTEGER"},
			{"name": "user_id", "type": "INTEGER"},
			{"name": "amount", "type": "INTEGER"},
		}},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create orders table: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/"+dsID+"/tables/users/insertAll", map[string]any{
		"rows": []map[string]any{
			{"json": map[string]any{"id": 1, "name": "alice"}},
			{"json": map[string]any{"id": 2, "name": "bob"}},
			{"json": map[string]any{"id": 3, "name": "carol"}},
		},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("insert users: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/"+dsID+"/tables/orders/insertAll", map[string]any{
		"rows": []map[string]any{
			{"json": map[string]any{"id": 10, "user_id": 1, "amount": 5}},
			{"json": map[string]any{"id": 11, "user_id": 1, "amount": 7}},
			{"json": map[string]any{"id": 12, "user_id": 2, "amount": 3}},
			{"json": map[string]any{"id": 13, "user_id": 2, "amount": 4}},
			{"json": map[string]any{"id": 14, "user_id": 2, "amount": 6}},
		},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("insert orders: %d %s", resp.StatusCode, body)
	}
}

func TestBigQueryJoinOnKey(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)
	seedAdvanced(t, base, "adv_join")

	// Aliased join with qualified column references — must not be mangled.
	rows := bqQuery(t, base, "SELECT u.name, o.amount FROM adv_join.users u JOIN adv_join.orders o ON u.id = o.user_id ORDER BY o.id")
	got := map[string]int{}
	for _, r := range rows.Rows {
		if len(r.F) != 2 {
			t.Fatalf("expected 2 cells, got %d: %+v", len(r.F), r)
		}
		got[r.F[0].V] += atoiOrZero(r.F[1].V)
	}
	want := map[string]int{"alice": 12, "bob": 13}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("join sum for %s: got %d want %d (all=%+v)", k, got[k], v, got)
		}
	}
}

func TestBigQueryGroupByCount(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)
	seedAdvanced(t, base, "adv_grp")

	rows := bqQuery(t, base, "SELECT user_id, COUNT(*) AS n FROM adv_grp.orders GROUP BY user_id ORDER BY user_id")
	if len(rows.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(rows.Rows), rows.Rows)
	}
	wantCounts := []string{"2", "3"}
	for i, r := range rows.Rows {
		if r.F[1].V != wantCounts[i] {
			t.Errorf("group %d count: got %s want %s", i, r.F[1].V, wantCounts[i])
		}
	}
}

func TestBigQueryHavingOnAggregate(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)
	seedAdvanced(t, base, "adv_having")

	rows := bqQuery(t, base, "SELECT user_id, SUM(amount) AS s FROM adv_having.orders GROUP BY user_id HAVING SUM(amount) > 12 ORDER BY user_id")
	if len(rows.Rows) != 1 {
		t.Fatalf("expected 1 row after HAVING, got %d: %+v", len(rows.Rows), rows.Rows)
	}
	if rows.Rows[0].F[0].V != "2" || rows.Rows[0].F[1].V != "13" {
		t.Errorf("having row: got %+v want user_id=2 sum=13", rows.Rows[0].F)
	}
}

func TestBigQueryConcatScalar(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)
	seedAdvanced(t, base, "adv_concat")

	rows := bqQuery(t, base, "SELECT CONCAT(name, '-', name) AS doubled FROM adv_concat.users WHERE id = 1")
	if len(rows.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows.Rows))
	}
	if got := rows.Rows[0].F[0].V; got != "alice-alice" {
		t.Errorf("concat: got %q want %q", got, "alice-alice")
	}
}

func TestBigQueryCurrentTimestamp(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	// No dataset/table needed — scalar-only SELECT.
	rows := bqQuery(t, base, "SELECT CURRENT_TIMESTAMP() AS ts")
	if len(rows.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows.Rows))
	}
	if rows.Rows[0].F[0].V == "" {
		t.Errorf("CURRENT_TIMESTAMP() returned empty value")
	}
}

func TestBigQueryIfnullPassthrough(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)
	seedAdvanced(t, base, "adv_ifnull")

	rows := bqQuery(t, base, "SELECT IFNULL(name, 'missing') FROM adv_ifnull.users WHERE id = 2")
	if len(rows.Rows) != 1 || rows.Rows[0].F[0].V != "bob" {
		t.Errorf("IFNULL passthrough: got %+v want bob", rows.Rows)
	}
}

func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
