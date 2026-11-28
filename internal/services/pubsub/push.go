package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// startPusher launches the per-subscription delivery goroutine. Safe to call
// repeatedly for the same sub; duplicate calls are dropped.
func (s *Service) startPusher(sub subscriptionResource) {
	if sub.PushConfig == nil || sub.PushConfig.PushEndpoint == "" {
		return
	}
	s.pushMu.Lock()
	if _, exists := s.pushers[sub.Name]; exists {
		s.pushMu.Unlock()
		return
	}
	// cancel is stored in s.pushers; stopPusher and Stop invoke it.
	ctx, cancel := context.WithCancel(s.ctx)
	s.pushers[sub.Name] = cancel
	s.pushMu.Unlock()

	s.pushWG.Add(1)
	go func() {
		defer s.pushWG.Done()
		s.runPusher(ctx, sub.Name, sub.PushConfig.PushEndpoint)
	}()
}

// stopPusher signals the goroutine for `name` to exit. It does not wait —
// Stop() does that on shutdown. Lookup-by-name keeps callers from needing
// to know whether a pusher actually exists.
func (s *Service) stopPusher(name string) {
	s.pushMu.Lock()
	cancel, ok := s.pushers[name]
	if ok {
		delete(s.pushers, name)
	}
	s.pushMu.Unlock()
	if ok {
		cancel()
	}
}

// runPusher polls a subscription's queue and POSTs each available message to
// the configured push endpoint. 2xx → ack (drop from queue). Anything else →
// extend the in-flight deadline by `backoff` so the message becomes available
// for retry later. Backoff doubles after each failure, capped at the sub's
// ack deadline (matching real Pub/Sub's behavior of bounding redelivery
// timing by the ack deadline).
func (s *Service) runPusher(ctx context.Context, subName, endpoint string) {
	const initialBackoff = 100 * time.Millisecond
	backoff := initialBackoff

	ticker := time.NewTicker(s.pushPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Re-read the sub each loop: ackDeadline can change and the sub may
		// have been deleted out from under us.
		ackSec, ok := s.subAckSeconds(subName)
		if !ok {
			return
		}
		maxBackoff := time.Duration(ackSec) * time.Second

		received, err := s.PullMessages(subName, 10)
		if err != nil {
			return
		}
		if len(received) == 0 {
			backoff = initialBackoff
			continue
		}

		for _, rec := range received {
			if ctx.Err() != nil {
				return
			}
			if s.deliver(ctx, subName, endpoint, rec) {
				_ = s.Acknowledge(subName, []string{rec.AckID})
				backoff = initialBackoff
				continue
			}
			backoff = s.holdForRetry(subName, rec.AckID, backoff, maxBackoff)
		}
	}
}

// holdForRetry holds one message inflight for backoff so it doesn't
// reappear instantly, then doubles backoff. Delay and backoff are both
// capped at the sub's ack deadline. It returns the updated backoff.
func (s *Service) holdForRetry(subName, ackID string, backoff, maxBackoff time.Duration) time.Duration {
	delay := backoff
	if delay > maxBackoff {
		delay = maxBackoff
	}
	if delay < time.Second {
		delay = time.Second // ModifyAckDeadline is integer seconds; use the minimum
	}
	_ = s.ModifyAckDeadline(subName, []string{ackID}, int(delay/time.Second))
	backoff *= 2
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}

// deliver POSTs one message envelope to the endpoint. Returns true on 2xx.
func (s *Service) deliver(ctx context.Context, subName, endpoint string, rec Received) bool {
	env := pushEnvelope{
		Subscription: subName,
		Message: pubsubMessage{
			Data:        base64.StdEncoding.EncodeToString(rec.Message.Data),
			Attributes:  rec.Message.Attributes,
			MessageID:   rec.Message.ID,
			PublishTime: rec.Message.PublishTime,
			OrderingKey: rec.Message.OrderingKey,
		},
	}
	body, err := json.Marshal(env)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.pushClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
