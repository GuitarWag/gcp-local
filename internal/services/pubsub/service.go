package pubsub

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

type Service struct {
	store   state.Store
	project string

	mu     sync.Mutex
	queues map[string][]storedMessage // subscription name -> messages

	msgSeq uint64
	ackSeq uint64

	// Push delivery: one goroutine per push subscription. pushers maps
	// subscription name to its cancel func so DeleteSubscription / Stop can
	// reach in and tear individual workers down.
	pushClient *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	pushWG     sync.WaitGroup
	pushMu     sync.Mutex
	pushers    map[string]context.CancelFunc
	pushPoll   time.Duration // poll interval; small constant in production, tunable in tests
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		store:      store,
		project:    cfg.Project,
		queues:     map[string][]storedMessage{},
		pushClient: &http.Client{Timeout: 5 * time.Second},
		ctx:        ctx,
		cancel:     cancel,
		pushers:    map[string]context.CancelFunc{},
		pushPoll:   50 * time.Millisecond,
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
			if res.PushConfig != nil {
				s.startPusher(res)
			}
		}
	}
	return s, nil
}

// Stop cancels all push-delivery goroutines and waits for them to exit.
// Safe to call once; subsequent calls are no-ops because cancel idempotently
// closes the context.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.pushWG.Wait()
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
