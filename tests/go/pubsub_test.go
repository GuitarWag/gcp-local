package gcplocaltest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

const project = "local-project"

func doJSON(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestPubSubPublishAndPull(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/" + project

	// create topic
	resp, body := doJSON(t, http.MethodPut, base+"/topics/orders", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create topic: %d %s", resp.StatusCode, body)
	}

	// create subscription
	subBody := map[string]any{
		"topic":              "projects/" + project + "/topics/orders",
		"ackDeadlineSeconds": 10,
	}
	resp, body = doJSON(t, http.MethodPut, base+"/subscriptions/orders-sub", subBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create subscription: %d %s", resp.StatusCode, body)
	}

	// publish
	pubBody := map[string]any{
		"messages": []map[string]any{
			{"data": base64.StdEncoding.EncodeToString([]byte("order-1"))},
			{"data": base64.StdEncoding.EncodeToString([]byte("order-2"))},
		},
	}
	resp, body = doJSON(t, http.MethodPost, base+"/topics/orders:publish", pubBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, body)
	}
	var pubResp struct {
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal(body, &pubResp); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	if len(pubResp.MessageIDs) != 2 {
		t.Errorf("expected 2 message ids, got %d", len(pubResp.MessageIDs))
	}

	// pull
	pullBody := map[string]any{"maxMessages": 10, "returnImmediately": true}
	resp, body = doJSON(t, http.MethodPost, base+"/subscriptions/orders-sub:pull", pullBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull: %d %s", resp.StatusCode, body)
	}
	var pullResp struct {
		ReceivedMessages []struct {
			AckID   string `json:"ackId"`
			Message struct {
				Data string `json:"data"`
			} `json:"message"`
		} `json:"receivedMessages"`
	}
	if err := json.Unmarshal(body, &pullResp); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	if len(pullResp.ReceivedMessages) != 2 {
		t.Fatalf("expected 2 received, got %d", len(pullResp.ReceivedMessages))
	}
	got := []string{}
	ackIDs := []string{}
	for _, m := range pullResp.ReceivedMessages {
		dec, err := base64.StdEncoding.DecodeString(m.Message.Data)
		if err != nil {
			t.Fatalf("decode data: %v", err)
		}
		got = append(got, string(dec))
		ackIDs = append(ackIDs, m.AckID)
	}
	if !contains(got, "order-1") || !contains(got, "order-2") {
		t.Errorf("expected order-1 and order-2 in %v", got)
	}

	// ack
	ackBody := map[string]any{"ackIds": ackIDs}
	resp, body = doJSON(t, http.MethodPost, base+"/subscriptions/orders-sub:acknowledge", ackBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack: %d %s", resp.StatusCode, body)
	}

	// second pull is empty
	resp, body = doJSON(t, http.MethodPost, base+"/subscriptions/orders-sub:pull", pullBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull2: %d %s", resp.StatusCode, body)
	}
	_ = json.Unmarshal(body, &pullResp)
	if len(pullResp.ReceivedMessages) != 0 {
		t.Errorf("expected empty pull, got %d", len(pullResp.ReceivedMessages))
	}
}

func TestPubSubPublishToMissingTopic(t *testing.T) {
	em := testutil.Start(t)
	url := "http://" + em.Host + "/v1/projects/" + project + "/topics/missing:publish"
	resp, _ := doJSON(t, http.MethodPost, url, map[string]any{"messages": []any{}})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
