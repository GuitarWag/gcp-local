package logging

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const nsEntries = "logging/entries"

type logEntry struct {
	LogName     string         `json:"logName"`
	Resource    map[string]any `json:"resource,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
	Severity    string         `json:"severity,omitempty"`
	TextPayload string         `json:"textPayload,omitempty"`
	JSONPayload map[string]any `json:"jsonPayload,omitempty"`
	Labels      map[string]any `json:"labels,omitempty"`
	InsertID    string         `json:"insertId,omitempty"`
}

type writeRequest struct {
	LogName  string         `json:"logName"`
	Resource map[string]any `json:"resource"`
	Entries  []logEntry     `json:"entries"`
}

type listRequest struct {
	ResourceNames []string `json:"resourceNames"`
	Filter        string   `json:"filter"`
	OrderBy       string   `json:"orderBy"`
	PageSize      int      `json:"pageSize"`
}

type listResponse struct {
	Entries []logEntry `json:"entries"`
}

type Service struct {
	store state.Store
	seq   uint64
	mu    sync.Mutex
}

func New(store state.Store, _ *config.Config) (*Service, error) {
	return &Service{store: store}, nil
}

func (s *Service) Name() string { return "logging" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v2/entries:write", s.handleWrite)
	mux.HandleFunc("/v2/entries:list", s.handleList)
}

func (s *Service) handleWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body writeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for _, e := range body.Entries {
		if e.LogName == "" {
			e.LogName = body.LogName
		}
		if e.Resource == nil {
			e.Resource = body.Resource
		}
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		id := fmt.Sprintf("%d-%d", e.Timestamp.UnixNano(), atomic.AddUint64(&s.seq, 1))
		data, _ := json.Marshal(e)
		_ = s.store.Put(nsEntries, id, data)
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req listRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	all, _ := s.store.List(nsEntries, "")
	out := listResponse{Entries: []logEntry{}}
	for _, v := range all {
		var e logEntry
		if json.Unmarshal(v, &e) == nil {
			if matchFilter(req, e) {
				out.Entries = append(out.Entries, e)
			}
		}
	}
	if req.PageSize > 0 && len(out.Entries) > req.PageSize {
		out.Entries = out.Entries[:req.PageSize]
	}
	writeJSON(w, http.StatusOK, out)
}

func matchFilter(req listRequest, e logEntry) bool {
	if len(req.ResourceNames) == 0 {
		return true
	}
	for _, rn := range req.ResourceNames {
		if strings.HasPrefix(e.LogName, rn) || rn == "" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
