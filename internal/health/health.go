package health

import (
	"encoding/json"
	"net/http"
	"sync"
)

type Status string

const (
	StatusStarting Status = "starting"
	StatusReady    Status = "ready"
	StatusError    Status = "error"
)

type Registry struct {
	mu       sync.RWMutex
	services map[string]Status
}

func New() *Registry {
	return &Registry{services: map[string]Status{}}
}

func (r *Registry) Set(name string, s Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[name] = s
}

// Lookup returns the current status for a registered service. The second
// return is false if the service was never registered.
func (r *Registry) Lookup(name string) (Status, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.services[name]
	return s, ok
}

func (r *Registry) snapshot() (map[string]Status, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]Status{}
	allReady := true
	for k, v := range r.services {
		out[k] = v
		if v != StatusReady {
			allReady = false
		}
	}
	if len(r.services) == 0 {
		allReady = false
	}
	return out, allReady
}

func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		services, ready := r.snapshot()
		status := "ok"
		code := http.StatusOK
		if !ready {
			status = "starting"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   status,
			"services": services,
		})
	}
}
