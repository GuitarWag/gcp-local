package spanner

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	spb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/GuitarWag/gcp-local/internal/config"
)

// Service is a Spanner stub. CreateSession works (so clients can connect),
// ExecuteSql etc. return UNIMPLEMENTED via the embedded UnimplementedSpannerServer.
type Service struct {
	project string
}

func New(_ any, cfg *config.Config) (*Service, error) {
	return &Service{project: cfg.Project}, nil
}

func (s *Service) Name() string              { return "spanner" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	spb.RegisterSpannerServer(g, &server{})
}

type server struct {
	spb.UnimplementedSpannerServer
	seq uint64
}

func (s *server) CreateSession(_ context.Context, req *spb.CreateSessionRequest) (*spb.Session, error) {
	n := atomic.AddUint64(&s.seq, 1)
	return &spb.Session{Name: fmt.Sprintf("%s/sessions/sess-%d", req.GetDatabase(), n)}, nil
}

func (s *server) DeleteSession(_ context.Context, _ *spb.DeleteSessionRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
