package cloudrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// Cloud Run service stub. Real Cloud Run boots container images; we don't.
// What we offer:
//   - REST CRUD for service/function resources, so the admin SDK can drive lifecycle.
//   - An "invoke" endpoint that forwards requests to a configured backend URL.
//   The PRD's "process proxy" goal (subprocess per function) is documented as a gap.

const (
	nsServices  = "cloudrun/services"
	nsFunctions = "functions/functions"
)

type Kind string

const (
	KindService  Kind = "service"
	KindFunction Kind = "function"
)

type resource struct {
	Name       string            `json:"name"`
	BackendURL string            `json:"backendUrl,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	CreateTime time.Time         `json:"createTime"`
	Kind       Kind              `json:"-"`
}

type Service struct {
	store   state.Store
	project string
	kind    Kind
	ns      string
	client  *http.Client
	runner  *runner
}

func NewCloudRun(store state.Store, cfg *config.Config) (*Service, error) {
	return newSvc(store, cfg, KindService, nsServices), nil
}

func NewFunctions(store state.Store, cfg *config.Config) (*Service, error) {
	return newSvc(store, cfg, KindFunction, nsFunctions), nil
}

func newSvc(store state.Store, cfg *config.Config, kind Kind, ns string) *Service {
	return &Service{
		store:   store,
		project: cfg.Project,
		kind:    kind,
		ns:      ns,
		client:  &http.Client{Timeout: 30 * time.Second},
		runner:  newRunner(),
	}
}

// Stop terminates any child processes spawned for resources managed by this
// service. Called by the gateway during shutdown.
func (s *Service) Stop() {
	s.runner.stopAll()
}

func (s *Service) Name() string {
	if s.kind == KindService {
		return "cloudrun"
	}
	return "functions"
}

// HandleV2 dispatches /v2/projects/{p}/locations/{loc}/(services|functions)/...
func (s *Service) HandleV2(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 5 || parts[2] != "locations" {
		return false
	}
	kindSeg := s.collectionSeg()
	if parts[4] != kindSeg {
		return false
	}
	switch len(parts) {
	case 5:
		s.collection(w, r, parts)
		return true
	case 6:
		s.item(w, r, parts)
		return true
	case 7:
		if parts[6] == "invoke" {
			s.invoke(w, r, parts)
			return true
		}
	}
	return false
}

func (s *Service) collectionSeg() string {
	if s.kind == KindService {
		return "services"
	}
	return "functions"
}

func (s *Service) resourceName(project, loc, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/%s/%s", project, loc, s.collectionSeg(), id)
}

func (s *Service) collection(w http.ResponseWriter, r *http.Request, parts []string) {
	project, loc := parts[1], parts[3]
	switch r.Method {
	case http.MethodPost:
		var body resource
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		id := body.Name
		if strings.Contains(id, "/") {
			id = id[strings.LastIndex(id, "/")+1:]
		}
		if id == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		body.Name = s.resourceName(project, loc, id)
		body.Kind = s.kind
		body.CreateTime = time.Now().UTC()
		if _, err := s.store.Get(s.ns, body.Name); err == nil {
			writeErr(w, http.StatusConflict, "exists")
			return
		}
		data, _ := json.Marshal(body)
		_ = s.store.Put(s.ns, body.Name, data)
		writeJSON(w, http.StatusOK, body)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/locations/%s/%s/", project, loc, s.collectionSeg())
		all, _ := s.store.List(s.ns, prefix)
		items := []resource{}
		for _, v := range all {
			var r resource
			if json.Unmarshal(v, &r) == nil {
				items = append(items, r)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{s.collectionSeg(): items})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) item(w http.ResponseWriter, r *http.Request, parts []string) {
	name := s.resourceName(parts[1], parts[3], parts[5])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(s.ns, name)
		if errors.Is(err, state.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(s.ns, name); err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		s.runner.stop(name)
		w.WriteHeader(http.StatusOK)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) invoke(w http.ResponseWriter, r *http.Request, parts []string) {
	name := s.resourceName(parts[1], parts[3], parts[5])
	data, err := s.store.Get(s.ns, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var res resource
	if err := json.Unmarshal(data, &res); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	target := res.BackendURL
	if len(res.Command) > 0 {
		c, err := s.runner.startOrGet(r.Context(), name, res.Command, res.Env)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "spawn child: "+err.Error())
			return
		}
		target = c.baseURL
	}
	if target == "" {
		writeErr(w, http.StatusBadRequest, "no backendUrl or command set on resource")
		return
	}
	body, _ := io.ReadAll(r.Body)
	req, err := http.NewRequest(r.Method, target, bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
