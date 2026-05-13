package memorystore

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/alicebob/miniredis/v2"

	"github.com/GuitarWag/gcp-local/internal/config"
)

// Service embeds a miniredis server speaking the real Redis wire protocol so
// any redis client library connects natively. Listens on its own TCP port.
type Service struct {
	r    *miniredis.Miniredis
	port int
}

func New(_ any, cfg *config.Config) (*Service, error) {
	port := cfg.Services.Memorystore.Port
	if port == 0 {
		port = 6379
	}
	r := miniredis.NewMiniRedis()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := r.StartAddr(addr); err != nil {
		// Fall back to an OS-chosen port if the requested one is busy.
		if err2 := r.Start(); err2 != nil {
			return nil, fmt.Errorf("start miniredis (%v, %v)", err, err2)
		}
		// extract the chosen port
		if h, p, err := net.SplitHostPort(r.Addr()); err == nil {
			_ = h
			if pn, err := strconv.Atoi(p); err == nil {
				port = pn
			}
		}
	}
	return &Service{r: r, port: port}, nil
}

func (s *Service) Name() string              { return "memorystore" }
func (s *Service) Register(_ *http.ServeMux) {}
func (s *Service) Port() int                 { return s.port }
func (s *Service) Stop(_ context.Context)    { s.r.Close() }
