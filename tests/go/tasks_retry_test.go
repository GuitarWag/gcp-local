package gcplocaltest

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestCloudTasksScheduleTimeDelay verifies that a task with a future
// scheduleTime is held until that time before being dispatched.
func TestCloudTasksScheduleTimeDelay(t *testing.T) {
	em := testutil.Start(t)

	var arrival atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrival.CompareAndSwap(0, time.Now().UnixNano())
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/queues"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{"name": "sched-q"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d %s", resp.StatusCode, body)
	}

	created := time.Now()
	scheduleAt := created.Add(500 * time.Millisecond)
	resp, body = doJSON(t, http.MethodPost, base+"/sched-q/tasks", map[string]any{
		"task": map[string]any{
			"scheduleTime": scheduleAt.UTC().Format(time.RFC3339Nano),
			"httpRequest": map[string]any{
				"url":        target.URL,
				"httpMethod": "POST",
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create task: %d %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if arrival.Load() != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := arrival.Load()
	if got == 0 {
		t.Fatal("task target never called within 2s")
	}
	delay := time.Duration(got - created.UnixNano())
	if delay < 400*time.Millisecond {
		t.Fatalf("dispatched too early: delay=%v (want >=400ms)", delay)
	}
	if delay > 2*time.Second {
		t.Fatalf("dispatched too late: delay=%v (want <=2s)", delay)
	}
}

// TestCloudTasksRetryOn5xxThenSuccess verifies that a 5xx response triggers
// a retry, and that the second attempt (200) ends the dispatch loop.
func TestCloudTasksRetryOn5xxThenSuccess(t *testing.T) {
	em := testutil.Start(t)

	var mu sync.Mutex
	var timestamps []time.Time
	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/queues"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{"name": "retry-q"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/retry-q/tasks", map[string]any{
		"task": map[string]any{
			"httpRequest": map[string]any{
				"url":        target.URL,
				"httpMethod": "POST",
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create task: %d %s", resp.StatusCode, body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Give it a beat to confirm no extra calls slip in.
	time.Sleep(300 * time.Millisecond)

	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected exactly 2 calls, got %d", got)
	}
	mu.Lock()
	gap := timestamps[1].Sub(timestamps[0])
	mu.Unlock()
	if gap > 1*time.Second {
		t.Fatalf("retry gap too large: %v (want <=1s)", gap)
	}
	if gap < 50*time.Millisecond {
		t.Fatalf("retry gap too small: %v (want >=50ms backoff)", gap)
	}
}

// TestCloudTasksRetryExhaustion verifies that persistent 5xx responses cause
// the task to be retried up to maxAttempts and then stop being called.
func TestCloudTasksRetryExhaustion(t *testing.T) {
	em := testutil.Start(t)

	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer target.Close()

	base := "http://" + em.Host + "/v2/projects/" + project + "/locations/us-central1/queues"

	resp, body := doJSON(t, http.MethodPost, base, map[string]any{"name": "exhaust-q"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/exhaust-q/tasks", map[string]any{
		"task": map[string]any{
			"httpRequest": map[string]any{
				"url":        target.URL,
				"httpMethod": "POST",
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create task: %d %s", resp.StatusCode, body)
	}

	// Within 5s we should see at least 3 attempts (base+200ms+400ms = ~700ms
	// for attempts 1..3; total budget for 5 attempts ~ 1.5s).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got < 3 {
		t.Fatalf("expected at least 3 attempts within 5s, got %d", got)
	}

	// Wait long enough for all attempts to finish. With 5 attempts the four
	// backoffs sum to 100+200+400+800 = 1500ms; pad it generously.
	time.Sleep(3 * time.Second)
	final := atomic.LoadInt32(&hits)
	if final > 5 {
		t.Fatalf("retries exceeded maxAttempts: got %d", final)
	}
	// Confirm no further calls arrive after we've passed the attempt window.
	time.Sleep(1 * time.Second)
	if got := atomic.LoadInt32(&hits); got != final {
		t.Fatalf("calls continued after exhaustion: %d -> %d", final, got)
	}
}
