package gcplocaltest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestPubSubConcurrentPublish verifies that with N goroutines publishing M
// messages each, the subscription pulls exactly N*M unique messages — no loss,
// no duplicates.
func TestPubSubConcurrentPublish(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/" + project

	resp, body := doJSON(t, http.MethodPut, base+"/topics/concurrent-topic", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topic: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPut, base+"/subscriptions/concurrent-sub", map[string]any{
		"topic":              "projects/" + project + "/topics/concurrent-topic",
		"ackDeadlineSeconds": 10,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sub: %d %s", resp.StatusCode, body)
	}

	const workers = 8
	const perWorker = 50
	expected := workers * perWorker

	var wg sync.WaitGroup
	errs := make(chan string, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				payload := fmt.Sprintf("w%d-m%d", wid, i)
				body := map[string]any{
					"messages": []map[string]any{
						{"data": base64.StdEncoding.EncodeToString([]byte(payload))},
					},
				}
				b, _ := json.Marshal(body)
				req, _ := http.NewRequest(http.MethodPost,
					base+"/topics/concurrent-topic:publish",
					bytesReader(b))
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errs <- err.Error()
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("status %d", resp.StatusCode)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("publish: %s", e)
	}

	seen := map[string]int{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for len(seen) < expected {
		select {
		case <-ctx.Done():
			t.Fatalf("only saw %d/%d messages", len(seen), expected)
		default:
		}
		resp, b := doJSON(t, http.MethodPost, base+"/subscriptions/concurrent-sub:pull",
			map[string]any{"maxMessages": 100, "returnImmediately": true})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("pull: %d %s", resp.StatusCode, b)
		}
		var pr struct {
			ReceivedMessages []struct {
				Message struct {
					Data string `json:"data"`
				} `json:"message"`
			} `json:"receivedMessages"`
		}
		if err := json.Unmarshal(b, &pr); err != nil {
			t.Fatal(err)
		}
		for _, m := range pr.ReceivedMessages {
			d, _ := base64.StdEncoding.DecodeString(m.Message.Data)
			seen[string(d)]++
		}
		if len(pr.ReceivedMessages) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if len(seen) != expected {
		t.Errorf("unique = %d, want %d", len(seen), expected)
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("duplicate %s: %d", k, n)
		}
	}
}
