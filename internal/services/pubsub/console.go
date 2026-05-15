package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"sort"
)

// ConsoleTopics lists every topic in store-key order. Only the topic
// name is exposed today — the resource has no other queryable fields.
func (s *Service) ConsoleTopics() ([]map[string]any, error) {
	all, err := s.store.List(nsTopics, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var t topicResource
		if json.Unmarshal(v, &t) != nil {
			continue
		}
		out = append(out, map[string]any{"name": t.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleSubscriptions lists subscriptions, optionally filtered by topic.
// An empty topic returns every subscription on every topic.
func (s *Service) ConsoleSubscriptions(topic string) ([]map[string]any, error) {
	all, err := s.store.List(nsSubscriptions, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var sub subscriptionResource
		if json.Unmarshal(v, &sub) != nil {
			continue
		}
		if topic != "" && sub.Topic != topic {
			continue
		}
		push := ""
		if sub.PushConfig != nil {
			push = sub.PushConfig.PushEndpoint
		}
		out = append(out, map[string]any{
			"name":               sub.Name,
			"topic":              sub.Topic,
			"ackDeadlineSeconds": sub.AckDeadlineSeconds,
			"pushEndpoint":       push,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsolePublish sends a single test message to a topic and returns the
// generated message ID. Thin wrapper around PublishMessages so the
// console doesn't have to construct a Message slice.
func (s *Service) ConsolePublish(topic string, data []byte, attrs map[string]string) (string, error) {
	ids, err := s.PublishMessages(topic, []Message{{Data: data, Attributes: attrs}})
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// ConsolePeekMessages returns up to `limit` messages currently sitting
// in the subscription's queue. It does not mark messages inflight or
// change ack state — repeated peeks return the same data while the
// queue is stable, which is what the UI wants.
func (s *Service) ConsolePeekMessages(subName string, limit int) ([]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[subName]
	if limit <= 0 || limit > len(q) {
		limit = len(q)
	}
	out := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		m := q[i].Message
		raw, _ := base64.StdEncoding.DecodeString(m.Data)
		out = append(out, map[string]any{
			"messageId":   m.MessageID,
			"publishTime": m.PublishTime,
			"attributes":  m.Attributes,
			"data":        string(raw),
			"inflight":    q[i].Inflight,
		})
	}
	return out, nil
}
