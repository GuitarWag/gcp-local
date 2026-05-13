package dashboard

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/GuitarWag/gcp-local/internal/state"
)

//go:embed index.html
var indexHTML []byte

type Service struct {
	store state.Store
}

func New(store state.Store) *Service {
	return &Service{store: store}
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/dashboard", s.handleIndex)
	mux.HandleFunc("/dashboard/", s.handleIndex)
	mux.HandleFunc("/dashboard/api/state", s.handleState)
}

func (s *Service) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Service) handleState(w http.ResponseWriter, _ *http.Request) {
	resources := map[string][]string{}
	groups := map[string]string{
		"buckets":       "storage/buckets",
		"topics":        "pubsub/topics",
		"subscriptions": "pubsub/subscriptions",
		"secrets":       "secretmanager/secrets",
		"queues":        "tasks/queues",
		"tasks":         "tasks/tasks",
		"scheduler":     "scheduler/jobs",
		"keyRings":      "kms/keyrings",
		"cryptoKeys":    "kms/cryptokeys",
		"sqlInstances":  "cloudsql/instances",
		"firestoreDocs": "firestore/documents",
	}
	for label, ns := range groups {
		all, _ := s.store.List(ns, "")
		out := []string{}
		for k := range all {
			out = append(out, k)
		}
		resources[label] = out
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resources)
}
