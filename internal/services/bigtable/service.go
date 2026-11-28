package bigtable

import (
	"net/http"
	"sync"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/grpc"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// Service implements the Bigtable data plane (google.bigtable.v2.Bigtable)
// and table admin (google.bigtable.admin.v2.BigtableTableAdmin) over the
// shared state.Store. ReadModifyWriteRow and the query/change-stream RPCs
// stay deliberately unimplemented.
type Service struct {
	project string
	store   state.Store

	// mu guards per-table write serialization so mutations on the same
	// table are atomic (state.Store has no cross-key transactions).
	mu sync.Mutex
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{project: cfg.Project, store: store}, nil
}

func (s *Service) Name() string { return "bigtable" }

func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	btpb.RegisterBigtableServer(g, &dataServer{svc: s})
	btapb.RegisterBigtableTableAdminServer(g, &adminServer{svc: s})
}
