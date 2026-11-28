package workflows

import (
	"net/http"
	"strings"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// State namespaces. Both go into the /admin/reset namespace list. They
// are exported because /admin/reset in the gateway needs them before the
// CRUD routes land.
const (
	NamespaceWorkflows  = "workflows/workflows"
	NamespaceOperations = "workflows/operations"
)

// Wire literals shared by the CRUD and operations handlers.
const (
	kindWorkflows   = "workflows"
	anyTypeKey      = "@type"
	anyTypeWorkflow = "type.googleapis.com/google.cloud.workflows.v1.Workflow"
	anyTypeEmpty    = "type.googleapis.com/google.protobuf.Empty"
)

// maxSourceBytes is the real workflows:v1 limit on sourceContents
// (128KB). Defined here because it is only used by the CRUD handlers.
const maxSourceBytes = 128 * 1024

// Service implements the workflows:v1 definition surface: workflow CRUD
// plus the operations.get endpoint that the real LRO helpers poll.
type Service struct {
	store   state.Store
	project string
	client  *http.Client
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{
		store:   store,
		project: cfg.Project,
		// Shared REST client shape; per-call timeout is applied by the
		// execution runner, not here (executions are issue #98).
		client: &http.Client{},
	}, nil
}

func (s *Service) Name() string              { return kindWorkflows }
func (s *Service) Register(_ *http.ServeMux) {}
func (s *Service) Stop()                     {}

// HandleV1 dispatches /v1/projects/{p}/locations/{l}/workflows/... and
// .../operations/... routes for workflows.googleapis.com/v1. It returns
// false when the path does not belong to this service, so later services
// keep dispatching. A matched collection or item with a wrong method is a
// 405, not a false.
//
// parts: [projects, p, locations, l, workflows|operations, ...]
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 5 || parts[2] != "locations" {
		return false
	}
	switch parts[4] {
	case "workflows":
		return s.dispatchWorkflows(w, r, parts)
	case "operations":
		return s.dispatchOperations(w, r, parts)
	default:
		return false
	}
}

// dispatchWorkflows routes the workflow collection and item paths.
func (s *Service) dispatchWorkflows(w http.ResponseWriter, r *http.Request, parts []string) bool {
	project, loc := parts[1], parts[3]
	switch len(parts) {
	case 5:
		s.workflowCollection(w, r, project, loc)
		return true
	case 6:
		if parts[5] == "" || strings.Contains(parts[5], ":") {
			// Executions live under .../workflows/{w}/executions and
			// colon-verbs belong to the gateway; leave both unclaimed.
			return false
		}
		s.workflowItem(w, r, project, loc, parts[5])
		return true
	default:
		return false
	}
}

// dispatchOperations routes the operations item path. The collection
// (list operations) is not in the v1 surface, so deeper paths fall
// through unclaimed.
func (s *Service) dispatchOperations(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) != 6 || parts[5] == "" {
		return false
	}
	s.operationGet(w, r, parts[1], parts[3], parts[5])
	return true
}

// workflowCollection handles POST (create) and GET (list) on
// .../locations/{l}/workflows.
func (s *Service) workflowCollection(w http.ResponseWriter, r *http.Request, project, loc string) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreate(w, r, project, loc)
	case http.MethodGet:
		s.handleList(w, r, project, loc)
	default:
		httpresp.Err(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// workflowItem handles GET, PATCH and DELETE on .../workflows/{w}.
func (s *Service) workflowItem(w http.ResponseWriter, r *http.Request, project, loc, id string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGet(w, project, loc, id)
	case http.MethodPatch:
		s.handlePatch(w, r, project, loc, id)
	case http.MethodDelete:
		s.handleDelete(w, r, project, loc, id)
	default:
		httpresp.Err(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
