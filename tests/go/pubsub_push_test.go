package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// pushBody mirrors the envelope shape the emulator should POST to a push
// subscription endpoint.
type pushBody struct {
	Subscription string `json:"subscription"`
	Message      struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
}

// createTopicSub provisions a topic and a push subscription whose endpoint
// is `endpoint`. Returns the topic publish URL.
func createTopicSub(t *testing.T, em *testutil.Emulator, topic, sub, endpoint string) string {
	t.Helper()
	base := "http://" + em.Host + "/v1/projects/" + project
	if resp, body := doJSON(t, http.MethodPut, base+"/topics/"+topic, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("create topic: %d %s", resp.StatusCode, body)
	}
	subReq := map[string]any{
		"topic":              "projects/" + project + "/topics/" + topic,
		"ackDeadlineSeconds": 10,
		"pushConfig":         map[string]any{"pushEndpoint": endpoint},
	}
	if resp, body := doJSON(t, http.MethodPut, base+"/subscriptions/"+sub, subReq); resp.StatusCode != http.StatusOK {
		t.Fatalf("create push subscription: %d %s", resp.StatusCode, body)
	}
	return base + "/topics/" + topic + ":publish"
}

func TestPubSubPushDelivery(t *testing.T) {
	em := testutil.Start(t)

	var mu sync.Mutex
	var got []pushBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b pushBody
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Errorf("push body parse: %v: %s", err, raw)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	publishURL := createTopicSub(t, em, "push-topic", "push-sub", srv.URL)

	pubBody := map[string]any{
		"messages": []map[string]any{
			{"data": base64.StdEncoding.EncodeToString([]byte("hello-push"))},
		},
	}
	if resp, body := doJSON(t, http.MethodPost, publishURL, pubBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, body)
	}

	// Wait up to 2s for delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 1 {
		t.Fatalf("expected push delivery within 2s, got 0")
	}
	first := got[0]
	if first.Subscription != "projects/"+project+"/subscriptions/push-sub" {
		t.Errorf("subscription mismatch: %q", first.Subscription)
	}
	decoded, err := base64.StdEncoding.DecodeString(first.Message.Data)
	if err != nil {
		t.Fatalf("data b64: %v", err)
	}
	if string(decoded) != "hello-push" {
		t.Errorf("payload mismatch: %q", decoded)
	}
	if first.Message.MessageID == "" {
		t.Errorf("expected messageId, got empty")
	}

	// Message should no longer be pullable — pusher acked it.
	pullURL := "http://" + em.Host + "/v1/projects/" + project + "/subscriptions/push-sub:pull"
	resp, body := doJSON(t, http.MethodPost, pullURL, map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull: %d %s", resp.StatusCode, body)
	}
	var pullResp struct {
		ReceivedMessages []any `json:"receivedMessages"`
	}
	_ = json.Unmarshal(body, &pullResp)
	if len(pullResp.ReceivedMessages) != 0 {
		t.Errorf("expected no pullable messages, got %d", len(pullResp.ReceivedMessages))
	}
}

func TestPubSubPushRetryAfter503(t *testing.T) {
	em := testutil.Start(t)

	var calls int32
	doneCh := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain body so the connection can be reused.
		_, _ = io.Copy(io.Discard, r.Body)
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	publishURL := createTopicSub(t, em, "retry-topic", "retry-sub", srv.URL)

	pubBody := map[string]any{
		"messages": []map[string]any{
			{"data": base64.StdEncoding.EncodeToString([]byte("retry-me"))},
		},
	}
	if resp, body := doJSON(t, http.MethodPost, publishURL, pubBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, body)
	}

	// First attempt should arrive quickly; retry takes ~1s because the
	// minimum modifyAckDeadline backoff is 1 second.
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("did not observe successful retry within 5s (calls=%d)", atomic.LoadInt32(&calls))
	}

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected at least 2 POSTs (503 then 200), got %d", got)
	}

	// After success, the message should not be redelivered or pullable.
	pullURL := "http://" + em.Host + "/v1/projects/" + project + "/subscriptions/retry-sub:pull"
	// Give the pusher a beat to ack and stop redelivering.
	time.Sleep(200 * time.Millisecond)
	resp, body := doJSON(t, http.MethodPost, pullURL, map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull: %d %s", resp.StatusCode, body)
	}
	var pullResp struct {
		ReceivedMessages []any `json:"receivedMessages"`
	}
	_ = json.Unmarshal(body, &pullResp)
	if len(pullResp.ReceivedMessages) != 0 {
		t.Errorf("expected message acked after retry, still pullable: %d", len(pullResp.ReceivedMessages))
	}
}
