package gcplocaltest

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestCloudTasksHTTPDispatch(t *testing.T) {
	em := testutil.Start(t)

	var got atomic.Value // string
	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		got.Store(string(b))
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/queues"

	// create queue
	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name": "q1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d %s", resp.StatusCode, body)
	}

	// post a task
	resp, body = doJSON(t, http.MethodPost, base+"/q1/tasks", map[string]any{
		"task": map[string]any{
			"httpRequest": map[string]any{
				"url":        target.URL,
				"httpMethod": "POST",
				"body":       base64.StdEncoding.EncodeToString([]byte("ping")),
				"headers":    map[string]string{"X-Test": "1"},
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create task: %d %s", resp.StatusCode, body)
	}

	// wait for dispatch
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) < 1 {
		t.Fatal("task target never called")
	}
	if s, _ := got.Load().(string); s != "ping" {
		t.Errorf("body delivered = %q", s)
	}
}
