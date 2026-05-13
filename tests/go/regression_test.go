package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestPubSubAckDeadlineExpiryRedelivers(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project)

	if resp, _ := doJSON(t, http.MethodPut, base+"/topics/redeliver-topic", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("topic")
	}
	if resp, _ := doJSON(t, http.MethodPut, base+"/subscriptions/redeliver-sub", map[string]any{
		"topic":              "projects/" + project + "/topics/redeliver-topic",
		"ackDeadlineSeconds": 1,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sub")
	}
	if resp, _ := doJSON(t, http.MethodPost, base+"/topics/redeliver-topic:publish", map[string]any{
		"messages": []map[string]any{
			{"data": base64.StdEncoding.EncodeToString([]byte("once"))},
		},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("publish")
	}

	resp, body := doJSON(t, http.MethodPost, base+"/subscriptions/redeliver-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull1: %d %s", resp.StatusCode, body)
	}
	var pr struct {
		ReceivedMessages []struct{ AckID string } `json:"receivedMessages"`
	}
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 1 {
		t.Fatalf("expected 1 msg on first pull, got %d", len(pr.ReceivedMessages))
	}

	// don't ack; wait past ack deadline
	time.Sleep(1500 * time.Millisecond)

	resp, body = doJSON(t, http.MethodPost, base+"/subscriptions/redeliver-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull2: %d %s", resp.StatusCode, body)
	}
	pr.ReceivedMessages = nil
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 1 {
		t.Errorf("expected redelivery after deadline expiry, got %d msgs", len(pr.ReceivedMessages))
	}
}

func TestPubSubModifyAckDeadlineNackRedelivers(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project)

	if resp, _ := doJSON(t, http.MethodPut, base+"/topics/nack-topic", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("topic")
	}
	if resp, _ := doJSON(t, http.MethodPut, base+"/subscriptions/nack-sub", map[string]any{
		"topic":              "projects/" + project + "/topics/nack-topic",
		"ackDeadlineSeconds": 60,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sub")
	}
	if resp, _ := doJSON(t, http.MethodPost, base+"/topics/nack-topic:publish", map[string]any{
		"messages": []map[string]any{{"data": base64.StdEncoding.EncodeToString([]byte("nack-me"))}},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("publish")
	}

	resp, body := doJSON(t, http.MethodPost, base+"/subscriptions/nack-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull1: %d %s", resp.StatusCode, body)
	}
	var pr struct {
		ReceivedMessages []struct {
			AckID string `json:"ackId"`
		} `json:"receivedMessages"`
	}
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(pr.ReceivedMessages))
	}
	ackID := pr.ReceivedMessages[0].AckID

	if resp, body := doJSON(t, http.MethodPost, base+"/subscriptions/nack-sub:modifyAckDeadline", map[string]any{
		"ackIds":             []string{ackID},
		"ackDeadlineSeconds": 0,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("modack: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/subscriptions/nack-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull2: %d %s", resp.StatusCode, body)
	}
	pr.ReceivedMessages = nil
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 1 {
		t.Errorf("expected redelivery after nack, got %d", len(pr.ReceivedMessages))
	}
}

func TestPubSubAckedMessageDoesNotRedeliver(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project)

	doJSON(t, http.MethodPut, base+"/topics/ack-topic", nil)
	doJSON(t, http.MethodPut, base+"/subscriptions/ack-sub", map[string]any{
		"topic":              "projects/" + project + "/topics/ack-topic",
		"ackDeadlineSeconds": 1,
	})
	doJSON(t, http.MethodPost, base+"/topics/ack-topic:publish", map[string]any{
		"messages": []map[string]any{{"data": base64.StdEncoding.EncodeToString([]byte("ok"))}},
	})

	_, body := doJSON(t, http.MethodPost, base+"/subscriptions/ack-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	var pr struct {
		ReceivedMessages []struct {
			AckID string `json:"ackId"`
		} `json:"receivedMessages"`
	}
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(pr.ReceivedMessages))
	}
	if resp, _ := doJSON(t, http.MethodPost, base+"/subscriptions/ack-sub:acknowledge",
		map[string]any{"ackIds": []string{pr.ReceivedMessages[0].AckID}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ack")
	}

	time.Sleep(1500 * time.Millisecond)
	_, body = doJSON(t, http.MethodPost, base+"/subscriptions/ack-sub:pull",
		map[string]any{"maxMessages": 10, "returnImmediately": true})
	pr.ReceivedMessages = nil
	_ = json.Unmarshal(body, &pr)
	if len(pr.ReceivedMessages) != 0 {
		t.Errorf("acked message redelivered after deadline: got %d", len(pr.ReceivedMessages))
	}
}

func TestBigQueryRejectsHostileDatasetID(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "evil\"; DROP TABLE x;--"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on hostile datasetId, got %d %s", resp.StatusCode, body)
	}
}

func TestBigQueryRejectsHostileTableID(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	if resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "safe_ds"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("dataset: %d %s", resp.StatusCode, body)
	}

	resp, body := doJSON(t, http.MethodPost, base+"/datasets/safe_ds/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "evil\"; DROP TABLE x;--"},
		"schema": map[string]any{
			"fields": []map[string]any{{"name": "k", "type": "STRING"}},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on hostile tableId, got %d %s", resp.StatusCode, body)
	}
}

func TestBigQueryRejectsHostileFieldName(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "safe_ds2"},
	})
	resp, body := doJSON(t, http.MethodPost, base+"/datasets/safe_ds2/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "t"},
		"schema": map[string]any{
			"fields": []map[string]any{{"name": "k\"); DROP TABLE x;--", "type": "STRING"}},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on hostile field name, got %d %s", resp.StatusCode, body)
	}
}
