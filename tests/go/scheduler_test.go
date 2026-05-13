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

// TestCloudSchedulerAtEverySyntax verifies the "@every <dur>" syntax (robfig
// shorthand) accepted by the cron parser fires the job.
func TestCloudSchedulerAtEverySyntax(t *testing.T) {
	em := testutil.Start(t)

	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v1/projects/" + project + "/locations/us-central1/jobs"
	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "at-every",
		"schedule": "@every 100ms",
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
	t.Fatalf("@every scheduler fired only %d times in 2s", atomic.LoadInt32(&hits))
}

// TestCloudSchedulerCronSyntaxAcceptedAndStored verifies a standard 5-field
// cron expression is accepted at create-time without errors. We don't wait
// for a fire (the smallest cron interval is one minute) — the assertion is
// that POST returns 200 and the job is retrievable.
func TestCloudSchedulerCronSyntaxAcceptedAndStored(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/" + project + "/locations/us-central1/jobs"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "weekday-9am",
		"schedule": "0 9 * * 1-5",
		"httpTarget": map[string]any{
			"uri":        "http://example.invalid",
			"httpMethod": "POST",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create job with cron syntax: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, base+"/weekday-9am", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("get stored job: %d %s", resp.StatusCode, body)
	}
}

// TestCloudSchedulerInvalidScheduleStillStored confirms an invalid schedule
// is accepted on create (matching real Cloud Scheduler's behaviour of
// allowing the resource and failing the run) but the job never fires.
func TestCloudSchedulerInvalidScheduleStillStored(t *testing.T) {
	em := testutil.Start(t)

	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v1/projects/" + project + "/locations/us-central1/jobs"
	resp, _ := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "broken",
		"schedule": "nonsense schedule string",
		"httpTarget": map[string]any{
			"uri":        target.URL,
			"httpMethod": "POST",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create with invalid schedule: %d", resp.StatusCode)
	}

	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("expected 0 fires for invalid schedule, got %d", got)
	}
}
