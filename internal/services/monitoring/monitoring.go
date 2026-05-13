package monitoring

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const nsSeries = "monitoring/timeseries"

type timeSeries struct {
	Metric     map[string]any `json:"metric"`
	Resource   map[string]any `json:"resource"`
	MetricKind string         `json:"metricKind,omitempty"`
	ValueType  string         `json:"valueType,omitempty"`
	Points     []point        `json:"points"`
}

type point struct {
	Interval interval       `json:"interval"`
	Value    map[string]any `json:"value"`
}

type interval struct {
	EndTime   time.Time `json:"endTime"`
	StartTime time.Time `json:"startTime,omitempty"`
}

type createRequest struct {
	TimeSeries []timeSeries `json:"timeSeries"`
}

type listResponse struct {
	TimeSeries []timeSeries `json:"timeSeries"`
}

type Service struct {
	store state.Store
	seq   uint64
}

func New(store state.Store, _ *config.Config) (*Service, error) {
	return &Service{store: store}, nil
}

func (s *Service) Name() string              { return "monitoring" }
func (s *Service) Register(_ *http.ServeMux) {}

// HandleV3 handles /v3/projects/{p}/timeSeries
func (s *Service) HandleV3(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[0] != "projects" || parts[2] != "timeSeries" {
		return false
	}
	switch r.Method {
	case http.MethodPost:
		s.create(w, r, parts[1])
		return true
	case http.MethodGet:
		s.list(w, r, parts[1])
		return true
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}
}

func (s *Service) create(w http.ResponseWriter, r *http.Request, project string) {
	var body createRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for _, ts := range body.TimeSeries {
		id := fmt.Sprintf("projects/%s/%d", project, atomic.AddUint64(&s.seq, 1))
		data, _ := json.Marshal(ts)
		_ = s.store.Put(nsSeries, id, data)
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Service) list(w http.ResponseWriter, _ *http.Request, project string) {
	prefix := "projects/" + project + "/"
	all, _ := s.store.List(nsSeries, prefix)
	out := listResponse{TimeSeries: []timeSeries{}}
	for k, v := range all {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		var ts timeSeries
		if json.Unmarshal(v, &ts) == nil {
			out.TimeSeries = append(out.TimeSeries, ts)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
