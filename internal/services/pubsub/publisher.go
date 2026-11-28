package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/GuitarWag/gcp-local/internal/state"
)

func (s *Service) handleTopic(w http.ResponseWriter, r *http.Request, name, action string) {
	if action == "publish" {
		if r.Method != http.MethodPost {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.publish(w, r, name)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if err := s.putTopic(name); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, topicResource{Name: name})
	case http.MethodGet:
		if _, err := s.store.Get(nsTopics, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "topic not found")
			return
		}
		s.writeJSON(w, http.StatusOK, topicResource{Name: name})
	case http.MethodDelete:
		if err := s.store.Delete(nsTopics, name); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				s.writeErr(w, http.StatusNotFound, "topic not found")
				return
			}
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) putTopic(name string) error {
	t := topicResource{Name: name}
	data, _ := json.Marshal(t)
	return s.store.Put(nsTopics, name, data)
}

// ---- Pure-Go API used by both REST and gRPC layers ----

type Message struct {
	ID          string
	Data        []byte
	Attributes  map[string]string
	OrderingKey string
	PublishTime time.Time
}

func (s *Service) CreateTopic(name string) error {
	if _, err := s.store.Get(nsTopics, name); err == nil {
		return ErrAlreadyExists
	}
	return s.putTopic(name)
}

func (s *Service) GetTopic(name string) (bool, error) {
	if _, err := s.store.Get(nsTopics, name); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Service) DeleteTopic(name string) error {
	return s.store.Delete(nsTopics, name)
}

func (s *Service) PublishMessages(topic string, msgs []Message) ([]string, error) {
	if _, err := s.store.Get(nsTopics, topic); err != nil {
		return nil, ErrTopicMissing
	}
	subs, err := s.subsForTopic(topic)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(msgs))
	for i := range msgs {
		id := fmt.Sprintf("msg-%d", atomic.AddUint64(&s.msgSeq, 1))
		msgs[i].ID = id
		msgs[i].PublishTime = time.Now().UTC()
		ids = append(ids, id)
	}
	s.mu.Lock()
	for _, sub := range subs {
		for _, m := range msgs {
			ackID := fmt.Sprintf("ack-%d", atomic.AddUint64(&s.ackSeq, 1))
			stored := storedMessage{AckID: ackID, Message: pubsubMessage{
				Data:        base64.StdEncoding.EncodeToString(m.Data),
				Attributes:  m.Attributes,
				MessageID:   m.ID,
				PublishTime: m.PublishTime,
				OrderingKey: m.OrderingKey,
			}}
			s.queues[sub.Name] = append(s.queues[sub.Name], stored)
		}
	}
	s.mu.Unlock()
	return ids, nil
}

func (s *Service) ListTopics(projectPrefix string) ([]string, error) {
	all, err := s.store.List(nsTopics, projectPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for k := range all {
		out = append(out, k)
	}
	return out, nil
}

func (s *Service) publish(w http.ResponseWriter, r *http.Request, topic string) {
	var body publishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	msgs := make([]Message, 0, len(body.Messages))
	for _, m := range body.Messages {
		raw, _ := base64.StdEncoding.DecodeString(m.Data)
		msgs = append(msgs, Message{
			Data:        raw,
			Attributes:  m.Attributes,
			OrderingKey: m.OrderingKey,
		})
	}
	ids, err := s.PublishMessages(topic, msgs)
	if err != nil {
		if errors.Is(err, ErrTopicMissing) {
			s.writeErr(w, http.StatusNotFound, "topic not found")
			return
		}
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, publishResponse{MessageIDs: ids})
}

func (s *Service) subsForTopic(topic string) ([]subscriptionResource, error) {
	all, err := s.store.List(nsSubscriptions, "")
	if err != nil {
		return nil, err
	}
	var out []subscriptionResource
	for _, v := range all {
		var sub subscriptionResource
		if err := json.Unmarshal(v, &sub); err != nil {
			continue
		}
		if sub.Topic == topic {
			out = append(out, sub)
		}
	}
	return out, nil
}
