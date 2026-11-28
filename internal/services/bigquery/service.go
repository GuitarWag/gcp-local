package bigquery

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
)

type Service struct {
	project  string
	db       *sql.DB
	mu       sync.Mutex
	datasets map[string]*dataset
	tables   map[string]map[string]*table // datasetID -> tableID -> table
}

func New(_ any, cfg *config.Config) (*Service, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &Service{
		project:  cfg.Project,
		db:       db,
		datasets: map[string]*dataset{},
		tables:   map[string]map[string]*table{},
	}, nil
}

func (s *Service) Name() string { return "bigquery" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/bigquery/v2/projects/", s.dispatch)
}

func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/bigquery/v2/projects/")
	parts := strings.Split(rest, "/")
	// parts: project, datasets[, dsId[, tables[, tblId[, action]]]]
	// or:    project, queries  (jobs.query)
	// or:    project, jobs
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "datasets":
		s.handleDatasets(w, r, parts)
	case "queries":
		s.handleQuery(w, r)
	case "jobs":
		s.handleJobs(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
