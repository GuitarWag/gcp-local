package scheduler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// cronParser accepts the standard 5-field cron syntax plus robfig's "@every <dur>"
// shorthand. We also normalise our legacy "every <dur>" prefix to "@every <dur>"
// in parseSchedule so existing configs keep working.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

const nsJobs = "scheduler/jobs"

type jobResource struct {
	Name       string      `json:"name"`
	Schedule   string      `json:"schedule"`
	HTTPTarget *httpTarget `json:"httpTarget,omitempty"`
	State      string      `json:"state,omitempty"`
}

type httpTarget struct {
	URI        string            `json:"uri"`
	HTTPMethod string            `json:"httpMethod"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"` // base64
}

type runner struct {
	job    jobResource
	cancel context.CancelFunc
}

type Service struct {
	store   state.Store
	project string
	client  *http.Client

	mu     sync.Mutex
	jobs   map[string]*runner
	parent context.Context
	cancel context.CancelFunc
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store:   store,
		project: cfg.Project,
		client:  &http.Client{Timeout: 5 * time.Second},
		jobs:    map[string]*runner{},
		parent:  ctx,
		cancel:  cancel,
	}, nil
}

func (s *Service) Name() string              { return "scheduler" }
func (s *Service) Register(_ *http.ServeMux) {}
func (s *Service) Stop()                     { s.cancel() }

// HandleV1 handles /v1/projects/{p}/locations/{loc}/jobs/...
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 5 || parts[2] != "locations" || parts[4] != "jobs" {
		return false
	}
	switch len(parts) {
	case 5:
		s.jobCollection(w, r, parts)
		return true
	case 6:
		s.jobItem(w, r, parts)
		return true
	}
	return false
}

func jobName(project, loc, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/jobs/%s", project, loc, id)
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (s *Service) jobCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	project, loc := parts[1], parts[3]
	switch r.Method {
	case http.MethodPost:
		var body jobResource
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		id := body.Name
		if strings.Contains(id, "/") {
			id = id[strings.LastIndex(id, "/")+1:]
		}
		body.Name = jobName(project, loc, id)
		body.State = "ENABLED"
		data, _ := json.Marshal(body)
		if err := s.store.Put(nsJobs, body.Name, data); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.startRunner(body)
		s.writeJSON(w, http.StatusOK, body)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/locations/%s/jobs/", project, loc)
		all, _ := s.store.List(nsJobs, prefix)
		out := struct {
			Jobs []jobResource `json:"jobs"`
		}{Jobs: []jobResource{}}
		for _, v := range all {
			var j jobResource
			if json.Unmarshal(v, &j) == nil {
				out.Jobs = append(out.Jobs, j)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) jobItem(w http.ResponseWriter, r *http.Request, parts []string) {
	name := jobName(parts[1], parts[3], parts[5])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsJobs, name)
		if errors.Is(err, state.ErrNotFound) {
			s.writeErr(w, http.StatusNotFound, "job not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsJobs, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "job not found")
			return
		}
		s.stopRunner(name)
		w.WriteHeader(http.StatusOK)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) startRunner(j jobResource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.jobs[j.Name]; ok {
		existing.cancel()
	}
	sched, err := parseSchedule(j.Schedule)
	if err != nil {
		// invalid schedule: don't run
		return
	}
	ctx, cancel := context.WithCancel(s.parent)
	r := &runner{job: j, cancel: cancel}
	s.jobs[j.Name] = r
	go func() {
		for {
			next := sched.Next(time.Now())
			delay := time.Until(next)
			if delay < 0 {
				delay = 0
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.fire(r.job)
			}
		}
	}()
}

func (s *Service) stopRunner(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.jobs[name]; ok {
		r.cancel()
		delete(s.jobs, name)
	}
}

func (s *Service) fire(j jobResource) {
	if j.HTTPTarget == nil {
		return
	}
	method := j.HTTPTarget.HTTPMethod
	if method == "" {
		method = http.MethodPost
	}
	var body []byte
	if j.HTTPTarget.Body != "" {
		body, _ = base64.StdEncoding.DecodeString(j.HTTPTarget.Body)
	}
	req, err := http.NewRequestWithContext(s.parent, method, j.HTTPTarget.URI, bytes.NewReader(body))
	if err != nil {
		return
	}
	for k, v := range j.HTTPTarget.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// parseSchedule accepts standard 5-field cron expressions ("0 9 * * 1-5"),
// robfig's "@every <duration>" shorthand, and the legacy "every <duration>"
// form used by older configs (rewritten to "@every <duration>" before parsing).
func parseSchedule(spec string) (cron.Schedule, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "every ") {
		spec = "@every " + strings.TrimPrefix(spec, "every ")
	}
	return cronParser.Parse(spec)
}
