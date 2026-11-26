package gateway

import (
	"time"

	"google.golang.org/grpc"
)

// stopGRPC drains RPCs, then forces a stop. GracefulStop alone waits
// forever on a client stream that never closes.
func stopGRPC(s *grpc.Server, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.Stop()
	}
}
