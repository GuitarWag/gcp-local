package workflows

import (
	"net/http"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// State namespaces. Both go into the /admin/reset namespace list. They
// are exported because /admin/reset in the gateway needs them before the
// CRUD routes land.
const (
	NamespaceWorkflows  = "workflows/workflows"
	NamespaceOperations = "workflows/operations"
)

// Service implements the workflows:v1 definition surface. CRUD routes and
// the operations endpoint land in the next subtask; HandleV1 returns false
// for everything for now.
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

func (s *Service) Name() string              { return "workflows" }
func (s *Service) Register(_ *http.ServeMux) {}
func (s *Service) Stop()                     {}

// HandleV1 dispatches /v1/projects/{p}/locations/{l}/workflows/... and
// .../operations/... routes. It claims nothing yet — the CRUD routes land
// in the next subtask.
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	_ = w
	_ = r
	_ = parts
	return false
}
