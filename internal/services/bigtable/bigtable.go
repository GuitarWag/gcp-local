package bigtable

import (
	"net/http"

	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/grpc"

	"github.com/GuitarWag/gcp-local/internal/config"
)

// Service is a minimal Bigtable stub. All RPCs return UNIMPLEMENTED, but a
// gRPC client can still connect and discover the surface.
type Service struct {
	project string
}

func New(_ any, cfg *config.Config) (*Service, error) {
	return &Service{project: cfg.Project}, nil
}

func (s *Service) Name() string              { return "bigtable" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	btpb.RegisterBigtableServer(g, &server{})
}

type server struct {
	btpb.UnimplementedBigtableServer
}
