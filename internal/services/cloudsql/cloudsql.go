package cloudsql

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// CloudSQL is provided as a REST instance-management stub only. The PRD's
// goal of real Postgres wire compatibility (via pgembedded) is out of scope
// for this build; SQLite-as-Postgres-shim would require a full Postgres
// protocol implementation. We expose instance/database CRUD so tooling that
// talks to the admin API succeeds, but actual SQL connections are not handled.

const nsInstances = "cloudsql/instances"

type instance struct {
	Name            string    `json:"name"`
	Project         string    `json:"project"`
	DatabaseVersion string    `json:"databaseVersion"`
	State           string    `json:"state"`
	CreateTime      time.Time `json:"createTime"`
}

type Service struct {
	store   state.Store
	project string
	mu      sync.Mutex
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{store: store, project: cfg.Project}, nil
}

func (s *Service) Name() string { return "cloudsql" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/sql/v1beta4/projects/", s.dispatch)
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/sql/v1beta4/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "instances" {
		http.NotFound(w, r)
		return
	}
	switch len(parts) {
	case 2:
		s.collection(w, r, parts[0])
	case 3:
		s.item(w, r, parts[0], parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) collection(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodPost:
		var body instance
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		body.Project = project
		if body.DatabaseVersion == "" {
			body.DatabaseVersion = "POSTGRES_15"
		}
		body.State = "RUNNABLE"
		body.CreateTime = time.Now().UTC()
		key := fmt.Sprintf("projects/%s/instances/%s", project, body.Name)
		if _, err := s.store.Get(nsInstances, key); err == nil {
			writeErr(w, http.StatusConflict, "instance exists")
			return
		}
		data, _ := json.Marshal(body)
		_ = s.store.Put(nsInstances, key, data)
		writeJSON(w, http.StatusOK, body)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/instances/", project)
		all, _ := s.store.List(nsInstances, prefix)
		out := struct {
			Items []instance `json:"items"`
		}{Items: []instance{}}
		for _, v := range all {
			var i instance
			if json.Unmarshal(v, &i) == nil {
				out.Items = append(out.Items, i)
			}
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) item(w http.ResponseWriter, r *http.Request, project, name string) {
	key := fmt.Sprintf("projects/%s/instances/%s", project, name)
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsInstances, key)
		if errors.Is(err, state.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsInstances, key); err != nil {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
