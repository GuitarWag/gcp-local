package gcplocaltest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	fs "cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestDeleteEndpointsReturn204(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project)

	if resp, _ := doJSON(t, http.MethodPut, base+"/topics/del-topic", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("topic")
	}
	if resp, _ := doJSON(t, http.MethodPut, base+"/subscriptions/del-sub", map[string]any{
		"topic": "projects/" + project + "/topics/del-topic",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sub")
	}

	checks := []struct {
		name string
		url  string
	}{
		{"pubsub sub", base + "/subscriptions/del-sub"},
		{"pubsub topic", base + "/topics/del-topic"},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			resp, body := doJSON(t, http.MethodDelete, c.url, nil)
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("expected 204, got %d %s", resp.StatusCode, body)
			}
		})
	}
}

func TestTasksDeleteReturns204(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v2/projects/" + project + "/locations/us-central1/queues")

	if resp, body := doJSON(t, http.MethodPost, base, map[string]any{"name": "qdel"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	resp, body := doJSON(t, http.MethodDelete, base+"/qdel", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("queue delete expected 204, got %d %s", resp.StatusCode, body)
	}
}

func TestSecretManagerDeleteCascadesVersions(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project)

	if resp, _ := doJSON(t, http.MethodPost, base+"/secrets?secretId=cascade-s", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("create secret")
	}
	for _, p := range []string{"v1-data", "v2-data"} {
		if resp, _ := doJSON(t, http.MethodPost, base+"/secrets/cascade-s:addVersion", map[string]any{
			"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(p))},
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("addVersion %s", p)
		}
	}

	resp, _ := doJSON(t, http.MethodGet, base+"/secrets/cascade-s/versions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list versions before")
	}

	if resp, _ := doJSON(t, http.MethodDelete, base+"/secrets/cascade-s", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete secret")
	}

	// recreate with same id, list versions: must be empty (no orphaned versions remain)
	if resp, _ := doJSON(t, http.MethodPost, base+"/secrets?secretId=cascade-s", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("recreate")
	}
	resp, body := doJSON(t, http.MethodGet, base+"/secrets/cascade-s/versions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list versions after: %d %s", resp.StatusCode, body)
	}
	var vl struct {
		Versions []any `json:"versions"`
	}
	_ = json.Unmarshal(body, &vl)
	if len(vl.Versions) != 0 {
		t.Errorf("expected zero versions after cascade delete, got %d (%s)", len(vl.Versions), body)
	}
}

func TestBigQueryTableMissingDatasetReturns404(t *testing.T) {
	em := testutil.Start(t)
	resp, _ := doJSON(t, http.MethodGet,
		em.URL("/bigquery/v2/projects/"+project+"/datasets/nope_ds/tables/nope_t"), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing dataset, got %d", resp.StatusCode)
	}
}

func TestFirestoreConcurrentCommitsNoRace(t *testing.T) {
	em := testutil.Start(t)
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := fs.NewClient(ctx, project,
		option.WithEndpoint(em.Host),
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("fs client: %v", err)
	}
	defer client.Close()

	docRef := client.Collection("counters").Doc("c1")
	if _, err := docRef.Set(ctx, map[string]any{"v": 0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = docRef.Set(ctx, map[string]any{"v": n})
		}(i)
	}
	wg.Wait()

	snap, err := docRef.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !snap.Exists() {
		t.Error("doc missing after concurrent commits")
	}
}

func TestAdminResetSucceeds(t *testing.T) {
	em := testutil.Start(t)
	if resp, _ := doJSON(t, http.MethodPost,
		em.URL("/storage/v1/b?project="+project),
		map[string]any{"name": "reset-me"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("bucket")
	}
	resp, body := doJSON(t, http.MethodPost, em.URL("/admin/reset"), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d %s", resp.StatusCode, body)
	}
	// bucket is gone
	resp, _ = doJSON(t, http.MethodGet, em.URL("/storage/v1/b/reset-me"), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bucket survived reset: %d", resp.StatusCode)
	}
}
