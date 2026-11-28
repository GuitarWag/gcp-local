package gcplocaltest

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func newPubSubClient(t *testing.T, em *testutil.Emulator) *pubsub.Client {
	t.Helper()
	t.Setenv("PUBSUB_EMULATOR_HOST", em.Host)
	t.Setenv("PUBSUB_PROJECT_ID", project)
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, err := pubsub.NewClient(context.Background(), project,
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestPubSubGRPCPublishAndReceive(t *testing.T) {
	em := testutil.Start(t)
	client := newPubSubClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	topic, err := client.CreateTopic(ctx, "grpc-topic")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	sub, err := client.CreateSubscription(ctx, "grpc-sub", pubsub.SubscriptionConfig{
		Topic:       topic,
		AckDeadline: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}

	results := []*pubsub.PublishResult{
		topic.Publish(ctx, &pubsub.Message{Data: []byte("alpha")}),
		topic.Publish(ctx, &pubsub.Message{Data: []byte("beta")}),
	}
	for _, r := range results {
		if _, err := r.Get(ctx); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	received := []string{}
	var mu sync.Mutex
	recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
	defer recvCancel()

	err = sub.Receive(recvCtx, func(_ context.Context, m *pubsub.Message) {
		mu.Lock()
		received = append(received, string(m.Data))
		got := len(received)
		mu.Unlock()
		m.Ack()
		if got >= 2 {
			recvCancel()
		}
	})
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		t.Fatalf("receive: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d (%v)", len(received), received)
	}
	if !((received[0] == "alpha" && received[1] == "beta") || (received[0] == "beta" && received[1] == "alpha")) {
		t.Errorf("unexpected messages: %v", received)
	}
}

// TestPubSubGRPCGetSubscriptionPushConfig covers the provider path (REST
// create with a pushConfig) read back through the SDK path (gRPC
// GetSubscription). The real API keeps pushConfig in the gRPC response.
func TestPubSubGRPCGetSubscriptionPushConfig(t *testing.T) {
	em := testutil.Start(t)
	client := newPubSubClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.CreateTopic(ctx, "push-topic"); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	base := "http://" + em.Host + "/v1/projects/" + project
	topicName := "projects/" + project + "/topics/push-topic"

	pushBody := map[string]any{
		"topic":              topicName,
		"ackDeadlineSeconds": 10,
		"pushConfig":         map[string]any{"pushEndpoint": "https://example.com/push"},
	}
	resp, body := doJSON(t, http.MethodPut, base+"/subscriptions/push-sub", pushBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create push subscription: %d %s", resp.StatusCode, body)
	}

	pullBody := map[string]any{
		"topic":              topicName,
		"ackDeadlineSeconds": 10,
	}
	resp, body = doJSON(t, http.MethodPut, base+"/subscriptions/pull-sub", pullBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create pull subscription: %d %s", resp.StatusCode, body)
	}

	pushSub := client.Subscription("push-sub")
	cfg, err := pushSub.Config(ctx)
	if err != nil {
		t.Fatalf("push sub config: %v", err)
	}
	if cfg.PushConfig.Endpoint != "https://example.com/push" {
		t.Errorf("push endpoint over gRPC = %q, want %q", cfg.PushConfig.Endpoint, "https://example.com/push")
	}

	pullSub := client.Subscription("pull-sub")
	cfg, err = pullSub.Config(ctx)
	if err != nil {
		t.Fatalf("pull sub config: %v", err)
	}
	if cfg.PushConfig.Endpoint != "" {
		t.Errorf("pull sub push endpoint over gRPC = %q, want empty", cfg.PushConfig.Endpoint)
	}
}
