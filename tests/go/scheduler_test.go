package gcplocaltest

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestCloudSchedulerFiresJob(t *testing.T) {
	em := testutil.Start(t)

	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v1/projects/" + project + "/locations/us-central1/jobs"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "j1",
		"schedule": "every 100ms",
		"httpTarget": map[string]any{
			"uri":        target.URL,
			"httpMethod": "POST",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create job: %d %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("scheduler fired only %d times in 2s", atomic.LoadInt32(&hits))
}
