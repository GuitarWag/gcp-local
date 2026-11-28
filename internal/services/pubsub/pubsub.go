package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Received struct {
	AckID   string
	Message Message
}

func (s *Service) CreateSubscription(name, topic string, ackDeadline int, pushURL string) error {
	if _, err := s.store.Get(nsTopics, topic); err != nil {
		return ErrTopicMissing
	}
	if _, err := s.store.Get(nsSubscriptions, name); err == nil {
		return ErrAlreadyExists
	}
	if ackDeadline <= 0 {
		ackDeadline = 10
	}
	res := subscriptionResource{
		Name:               name,
		Topic:              topic,
		AckDeadlineSeconds: ackDeadline,
	}
	if pushURL != "" {
		res.PushConfig = &pushConfig{PushEndpoint: pushURL}
	}
	if err := s.putSubscription(res); err != nil {
		return err
	}
	if res.PushConfig != nil {
		s.startPusher(res)
	}
	return nil
}

func (s *Service) GetSubscription(name string) (string, int, error) {
	data, err := s.store.Get(nsSubscriptions, name)
	if err != nil {
		return "", 0, ErrSubMissing
	}
	var sub subscriptionResource
	_ = json.Unmarshal(data, &sub)
	return sub.Topic, sub.AckDeadlineSeconds, nil
}

func (s *Service) DeleteSubscription(name string) error {
	if err := s.store.Delete(nsSubscriptions, name); err != nil {
		return ErrSubMissing
	}
	s.stopPusher(name)
	s.mu.Lock()
	delete(s.queues, name)
	s.mu.Unlock()
	return nil
}

func (s *Service) PullMessages(subName string, limit int) ([]Received, error) {
	data, err := s.store.Get(nsSubscriptions, subName)
	if err != nil {
		return nil, ErrSubMissing
	}
	var sub subscriptionResource
	_ = json.Unmarshal(data, &sub)
	ackSec := sub.AckDeadlineSeconds
	if ackSec <= 0 {
		ackSec = 10
	}
	if limit <= 0 {
		limit = 10
	}
	now := time.Now()
	deadline := now.Add(time.Duration(ackSec) * time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[subName]
	out := make([]Received, 0, limit)
	for i := range q {
		if len(out) >= limit {
			break
		}
		if !q[i].available(now) {
			continue
		}
		ackID := fmt.Sprintf("ack-%d", atomic.AddUint64(&s.ackSeq, 1))
		q[i].Inflight = true
		q[i].Deadline = deadline
		q[i].AckID = ackID
		raw, _ := base64.StdEncoding.DecodeString(q[i].Message.Data)
		out = append(out, Received{
			AckID: ackID,
			Message: Message{
				ID:          q[i].Message.MessageID,
				Data:        raw,
				Attributes:  q[i].Message.Attributes,
				OrderingKey: q[i].Message.OrderingKey,
				PublishTime: q[i].Message.PublishTime,
			},
		})
	}
	return out, nil
}

func (s *Service) Acknowledge(subName string, ackIDs []string) error {
	if _, err := s.store.Get(nsSubscriptions, subName); err != nil {
		return ErrSubMissing
	}
	if len(ackIDs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		set[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[subName]
	kept := q[:0]
	for _, m := range q {
		if _, ok := set[m.AckID]; ok && m.Inflight {
			continue
		}
		kept = append(kept, m)
	}
	s.queues[subName] = kept
	return nil
}

// ModifyAckDeadline updates the ack deadline for inflight messages. sec<=0
// nacks the messages so they become immediately available for redelivery.
func (s *Service) ModifyAckDeadline(subName string, ackIDs []string, sec int) error {
	if _, err := s.store.Get(nsSubscriptions, subName); err != nil {
		return ErrSubMissing
	}
	if len(ackIDs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		set[id] = struct{}{}
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[subName]
	for i := range q {
		if _, ok := set[q[i].AckID]; !ok {
			continue
		}
		if sec <= 0 {
			q[i].Inflight = false
			q[i].Deadline = time.Time{}
		} else {
			q[i].Deadline = now.Add(time.Duration(sec) * time.Second)
		}
	}
	return nil
}

func (s *Service) ListSubscriptions(projectPrefix string) ([]string, error) {
	all, err := s.store.List(nsSubscriptions, projectPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for k := range all {
		out = append(out, k)
	}
	return out, nil
}

func (s *Service) handleSubscription(w http.ResponseWriter, r *http.Request, name, action string) {
	switch action {
	case "pull":
		if r.Method != http.MethodPost {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.pull(w, r, name)
		return
	case "acknowledge":
		if r.Method != http.MethodPost {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.acknowledge(w, r, name)
		return
	case "modifyAckDeadline":
		if r.Method != http.MethodPost {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.modifyAckDeadline(w, r, name)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body subscriptionResource
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		body.Name = name
		if body.AckDeadlineSeconds == 0 {
			body.AckDeadlineSeconds = 10
		}
		if body.Topic == "" {
			s.writeErr(w, http.StatusBadRequest, "topic required")
			return
		}
		if _, err := s.store.Get(nsTopics, body.Topic); err != nil {
			s.writeErr(w, http.StatusNotFound, "topic not found: "+body.Topic)
			return
		}
		if err := s.putSubscription(body); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if body.PushConfig != nil && body.PushConfig.PushEndpoint != "" {
			s.startPusher(body)
		}
		s.writeJSON(w, http.StatusOK, body)
	case http.MethodGet:
		data, err := s.store.Get(nsSubscriptions, name)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "subscription not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsSubscriptions, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "subscription not found")
			return
		}
		s.stopPusher(name)
		s.mu.Lock()
		delete(s.queues, name)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) putSubscription(sub subscriptionResource) error {
	data, _ := json.Marshal(sub)
	return s.store.Put(nsSubscriptions, sub.Name, data)
}

func (s *Service) pull(w http.ResponseWriter, r *http.Request, subName string) {
	var body pullRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	received, err := s.PullMessages(subName, body.MaxMessages)
	if err != nil {
		s.writeErr(w, http.StatusNotFound, "subscription not found")
		return
	}
	resp := pullResponse{ReceivedMessages: make([]receivedMessage, 0, len(received))}
	for _, m := range received {
		resp.ReceivedMessages = append(resp.ReceivedMessages, receivedMessage{
			AckID: m.AckID,
			Message: pubsubMessage{
				Data:        base64.StdEncoding.EncodeToString(m.Message.Data),
				Attributes:  m.Message.Attributes,
				MessageID:   m.Message.ID,
				PublishTime: m.Message.PublishTime,
				OrderingKey: m.Message.OrderingKey,
			},
		})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Service) acknowledge(w http.ResponseWriter, r *http.Request, subName string) {
	var body ackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.Acknowledge(subName, body.AckIDs); err != nil {
		s.writeErr(w, http.StatusNotFound, "subscription not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) modifyAckDeadline(w http.ResponseWriter, r *http.Request, subName string) {
	var body struct {
		AckIDs             []string `json:"ackIds"`
		AckDeadlineSeconds int      `json:"ackDeadlineSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.ModifyAckDeadline(subName, body.AckIDs, body.AckDeadlineSeconds); err != nil {
		s.writeErr(w, http.StatusNotFound, "subscription not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

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
			// Failure: hold the message inflight for `backoff` so it
			// doesn't reappear instantly. Cap at the ack deadline.
			delay := backoff
			if delay > maxBackoff {
				delay = maxBackoff
			}
			if delay < time.Second {
				delay = time.Second // ModifyAckDeadline is integer seconds; use the minimum
			}
			_ = s.ModifyAckDeadline(subName, []string{rec.AckID}, int(delay/time.Second))
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
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

// subAckSeconds looks up the current ack deadline for a subscription. The
// second return is false if the subscription no longer exists, which the
// caller uses as an exit signal.
func (s *Service) subAckSeconds(name string) (int, bool) {
	data, err := s.store.Get(nsSubscriptions, name)
	if err != nil {
		return 0, false
	}
	var sub subscriptionResource
	if err := json.Unmarshal(data, &sub); err != nil {
		return 0, false
	}
	if sub.AckDeadlineSeconds <= 0 {
		return 10, true
	}
	return sub.AckDeadlineSeconds, true
}
