package tasks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	nsQueues = "tasks/queues"
	nsTasks  = "tasks/tasks"
)

type queueResource struct {
	Name string `json:"name"`
}

type taskResource struct {
	Name         string       `json:"name"`
	HTTPRequest  *httpRequest `json:"httpRequest,omitempty"`
	CreateTime   time.Time    `json:"createTime"`
	ScheduleTime time.Time    `json:"scheduleTime,omitempty"`
}

type httpRequest struct {
	URL        string            `json:"url"`
	HTTPMethod string            `json:"httpMethod"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"` // base64
}

type Service struct {
	store   state.Store
	project string
	client  *http.Client

	mu       sync.Mutex
	taskSeq  uint64
	ctx      context.Context
	cancel   context.CancelFunc
	inflight sync.WaitGroup
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store:   store,
		project: cfg.Project,
		client:  &http.Client{Timeout: 5 * time.Second},
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

func (s *Service) Name() string              { return "tasks" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) Stop() {
	s.cancel()
	s.inflight.Wait()
}

// HandleV2 handles /v2/projects/{p}/locations/{loc}/queues/...
func (s *Service) HandleV2(w http.ResponseWriter, r *http.Request, parts []string) bool {
	// parts: [projects, p, locations, loc, queues, ...]
	if len(parts) < 5 || parts[2] != "locations" || parts[4] != "queues" {
		return false
	}
	switch len(parts) {
	case 5:
		s.queueCollection(w, r, parts)
		return true
	case 6:
		s.queueItem(w, r, parts)
		return true
	case 7:
		if parts[6] == "tasks" {
			s.taskCollection(w, r, parts)
			return true
		}
		return false
	case 8:
		if parts[6] == "tasks" {
			s.taskItem(w, r, parts)
			return true
		}
		return false
	}
	return false
}

func queueName(project, loc, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/queues/%s", project, loc, id)
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (s *Service) queueCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	project, loc := parts[1], parts[3]
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		// body.Name may be full or short id
		id := body.Name
		if strings.Contains(id, "/") {
			id = id[strings.LastIndex(id, "/")+1:]
		}
		name := queueName(project, loc, id)
		if _, err := s.store.Get(nsQueues, name); err == nil {
			s.writeErr(w, http.StatusConflict, "queue exists")
			return
		}
		q := queueResource{Name: name}
		data, err := json.Marshal(q)
		if err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.Put(nsQueues, name, data); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, q)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/locations/%s/queues/", project, loc)
		all, _ := s.store.List(nsQueues, prefix)
		out := struct {
			Queues []queueResource `json:"queues"`
		}{Queues: []queueResource{}}
		for _, v := range all {
			var q queueResource
			if json.Unmarshal(v, &q) == nil {
				out.Queues = append(out.Queues, q)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) queueItem(w http.ResponseWriter, r *http.Request, parts []string) {
	name := queueName(parts[1], parts[3], parts[5])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsQueues, name)
		if errors.Is(err, state.ErrNotFound) {
			s.writeErr(w, http.StatusNotFound, "queue not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsQueues, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "queue not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) taskCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	qName := queueName(parts[1], parts[3], parts[5])
	if _, err := s.store.Get(nsQueues, qName); err != nil {
		s.writeErr(w, http.StatusNotFound, "queue not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Task taskResource `json:"task"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		id := fmt.Sprintf("task-%d", atomic.AddUint64(&s.taskSeq, 1))
		t := body.Task
		t.Name = fmt.Sprintf("%s/tasks/%s", qName, id)
		t.CreateTime = time.Now().UTC()
		data, err := json.Marshal(t)
		if err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.Put(nsTasks, t.Name, data); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if t.HTTPRequest != nil {
			s.inflight.Add(1)
			go func(tk taskResource) {
				defer s.inflight.Done()
				defer func() { _ = recover() }()
				s.dispatchHTTP(tk)
			}(t)
		}
		s.writeJSON(w, http.StatusOK, t)
	case http.MethodGet:
		prefix := qName + "/tasks/"
		all, _ := s.store.List(nsTasks, prefix)
		out := struct {
			Tasks []taskResource `json:"tasks"`
		}{Tasks: []taskResource{}}
		for _, v := range all {
			var t taskResource
			if json.Unmarshal(v, &t) == nil {
				out.Tasks = append(out.Tasks, t)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) taskItem(w http.ResponseWriter, r *http.Request, parts []string) {
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s", parts[1], parts[3], parts[5], parts[7])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsTasks, name)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsTasks, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) dispatchHTTP(t taskResource) {
	if t.HTTPRequest == nil || t.HTTPRequest.URL == "" {
		return
	}
	method := t.HTTPRequest.HTTPMethod
	if method == "" {
		method = http.MethodPost
	}
	var body []byte
	if t.HTTPRequest.Body != "" {
		body, _ = base64.StdEncoding.DecodeString(t.HTTPRequest.Body)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, t.HTTPRequest.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	for k, v := range t.HTTPRequest.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	_ = err
	// Task removed after dispatch (best-effort) — matches real Cloud Tasks behaviour after success.
	_ = s.store.Delete(nsTasks, t.Name)
}
