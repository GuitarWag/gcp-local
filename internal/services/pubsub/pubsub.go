package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	nsTopics        = "pubsub/topics"
	nsSubscriptions = "pubsub/subscriptions"
)

var (
	ErrAlreadyExists = errors.New("already exists")
	ErrTopicMissing  = errors.New("topic missing")
	ErrSubMissing    = errors.New("subscription missing")
)

type topicResource struct {
	Name string `json:"name"`
}

type subscriptionResource struct {
	Name               string      `json:"name"`
	Topic              string      `json:"topic"`
	AckDeadlineSeconds int         `json:"ackDeadlineSeconds"`
	PushConfig         *pushConfig `json:"pushConfig,omitempty"`
}

type pushConfig struct {
	PushEndpoint string `json:"pushEndpoint"`
}

type publishRequest struct {
	Messages []pubsubMessage `json:"messages"`
}

type pubsubMessage struct {
	Data        string            `json:"data,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	MessageID   string            `json:"messageId,omitempty"`
	PublishTime time.Time         `json:"publishTime,omitempty"`
	OrderingKey string            `json:"orderingKey,omitempty"`
}

type publishResponse struct {
	MessageIDs []string `json:"messageIds"`
}

type pullRequest struct {
	ReturnImmediately bool `json:"returnImmediately"`
	MaxMessages       int  `json:"maxMessages"`
}

type pullResponse struct {
	ReceivedMessages []receivedMessage `json:"receivedMessages"`
}

type receivedMessage struct {
	AckID   string        `json:"ackId"`
	Message pubsubMessage `json:"message"`
}

type ackRequest struct {
	AckIDs []string `json:"ackIds"`
}

type storedMessage struct {
	AckID    string        `json:"ackId"`
	Message  pubsubMessage `json:"message"`
	Inflight bool          `json:"-"`
	Deadline time.Time     `json:"-"`
}

func (m *storedMessage) available(now time.Time) bool {
	return !m.Inflight || now.After(m.Deadline)
}

type Service struct {
	store   state.Store
	project string

	mu     sync.Mutex
	queues map[string][]storedMessage // subscription name -> messages

	msgSeq uint64
	ackSeq uint64
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	s := &Service{
		store:   store,
		project: cfg.Project,
		queues:  map[string][]storedMessage{},
	}
	for _, t := range cfg.Services.PubSub.Topics {
		topicName := fmt.Sprintf("projects/%s/topics/%s", cfg.Project, t.Name)
		if err := s.putTopic(topicName); err != nil {
			return nil, fmt.Errorf("seed topic %s: %w", t.Name, err)
		}
		for _, sub := range t.Subscriptions {
			subName := fmt.Sprintf("projects/%s/subscriptions/%s", cfg.Project, sub.Name)
			res := subscriptionResource{
				Name:               subName,
				Topic:              topicName,
				AckDeadlineSeconds: 10,
			}
			if sub.PushEndpoint != "" {
				res.PushConfig = &pushConfig{PushEndpoint: sub.PushEndpoint}
			}
			if err := s.putSubscription(res); err != nil {
				return nil, fmt.Errorf("seed subscription %s: %w", sub.Name, err)
			}
		}
	}
	return s, nil
}

func (s *Service) Name() string { return "pubsub" }

func (s *Service) Register(_ *http.ServeMux) {
	// V1 routes are dispatched centrally by the gateway via HandleV1.
}

// HandleV1 handles /v1/projects/{p}/(topics|subscriptions)/... segments.
// `parts` is the path split at "/", starting with "projects".
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 4 {
		return false
	}
	kind := parts[2]
	if kind != "topics" && kind != "subscriptions" {
		return false
	}
	s.dispatch(w, r)
	return true
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// dispatch routes /v1/projects/{project}/{topics|subscriptions}/{name}[:action]
func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 4 {
		s.writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// parts: ["projects", "{project}", "topics|subscriptions", "{name}[:action]"]
	kind := parts[2]
	resourceFull := fmt.Sprintf("%s/%s/%s/%s", parts[0], parts[1], parts[2], parts[3])

	// strip :action suffix
	resourceName := resourceFull
	action := ""
	if idx := strings.LastIndex(parts[3], ":"); idx >= 0 {
		action = parts[3][idx+1:]
		name := parts[3][:idx]
		resourceName = fmt.Sprintf("%s/%s/%s/%s", parts[0], parts[1], parts[2], name)
	}

	switch kind {
	case "topics":
		s.handleTopic(w, r, resourceName, action)
	case "subscriptions":
		s.handleSubscription(w, r, resourceName, action)
	default:
		s.writeErr(w, http.StatusNotFound, "unknown resource: "+kind)
	}
}

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

type Received struct {
	AckID   string
	Message Message
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
	return s.putSubscription(res)
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
	s.mu.Lock()
	delete(s.queues, name)
	s.mu.Unlock()
	return nil
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

func (s *Service) PullMessages(subName string, max int) ([]Received, error) {
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
	if max <= 0 {
		max = 10
	}
	now := time.Now()
	deadline := now.Add(time.Duration(ackSec) * time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[subName]
	out := make([]Received, 0, max)
	for i := range q {
		if len(out) >= max {
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

// DecodeData is exposed for tests; pub/sub message bodies are base64 in the REST API.
func DecodeData(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
