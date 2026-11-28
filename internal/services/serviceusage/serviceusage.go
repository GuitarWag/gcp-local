// Package serviceusage emulates the GCP Service Usage API surface used by
// client libraries: project service list/get and the enable/disable verbs.
package serviceusage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	nsServices   = "serviceusage/services"
	nsOperations = "serviceusage/operations"
)

const (
	stateEnabled  = "ENABLED"
	stateDisabled = "DISABLED"

	statusInvalidArgument = "INVALID_ARGUMENT"
	statusNotFound        = "NOT_FOUND"
	statusFailedPrecond   = "FAILED_PRECONDITION"
	statusInternal        = "INTERNAL"

	respTypePrefix    = "type.googleapis.com/google.api.serviceusage.v1."
	servicesSegment   = "services"
	fieldResponseType = "@type"
	fieldName         = "name"
)

const errServiceNotFound = "Service %s not found."

// catalogEntry is one emulated service in the static catalog.
type catalogEntry struct {
	Name  string
	Title string
}

// catalog lists every emulated service, sorted by name.
var catalog = []catalogEntry{
	{"bigquery.googleapis.com", "BigQuery API"},
	{"bigtable.googleapis.com", "Cloud BigTable API"},
	{"cloudfunctions.googleapis.com", "Cloud Functions API"},
	{"cloudkms.googleapis.com", "Cloud Key Management Service (KMS) API"},
	{"cloudscheduler.googleapis.com", "Cloud Scheduler API"},
	{"cloudtasks.googleapis.com", "Cloud Tasks API"},
	{"firestore.googleapis.com", "Cloud Firestore API"},
	{"iam.googleapis.com", "Identity and Access Management (IAM) API"},
	{"iamcredentials.googleapis.com", "IAM Service Account Credentials API"},
	{"logging.googleapis.com", "Cloud Logging API"},
	{"monitoring.googleapis.com", "Cloud Monitoring API"},
	{"pubsub.googleapis.com", "Cloud Pub/Sub API"},
	{"redis.googleapis.com", "Google Cloud Memorystore for Redis API"},
	{"run.googleapis.com", "Cloud Run API"},
	{"secretmanager.googleapis.com", "Secret Manager API"},
	{"serviceusage.googleapis.com", "Service Usage API"},
	{"spanner.googleapis.com", "Cloud Spanner API"},
	{"sqladmin.googleapis.com", "Cloud SQL Admin API"},
	{"storage.googleapis.com", "Cloud Storage API"},
}

type Service struct {
	store state.Store
	opSeq atomic.Uint64
}

func New(store state.Store, _ *config.Config) *Service {
	s := &Service{store: store}
	// Resume the counter so ids survive restarts.
	var highest uint64
	if all, err := store.List(nsOperations, ""); err == nil {
		for k := range all {
			if n, err := strconv.ParseUint(k, 10, 64); err == nil && n > highest {
				highest = n
			}
		}
	}
	s.opSeq.Store(highest)
	return s
}

func (s *Service) Name() string { return "serviceusage" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/operations/", s.handleOperationGet)
}

func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[0] != "projects" {
		return false
	}
	switch len(parts) {
	case 3:
		if parts[2] == "services:batchEnable" {
			if r.Method != http.MethodPost {
				return false
			}
			s.handleBatchEnable(w, r, parts[1])
			return true
		}
		if parts[2] == servicesSegment && r.Method == http.MethodGet {
			s.handleList(w, r, parts[1])
			return true
		}
		return false
	case 4:
		if parts[2] != servicesSegment {
			return false
		}
		switch r.Method {
		case http.MethodGet:
			s.handleGet(w, r, parts[1], parts[3])
			return true
		case http.MethodPost:
			service, verb, ok := strings.Cut(parts[3], ":")
			if !ok {
				return false
			}
			switch verb {
			case "enable":
				s.handleEnable(w, r, parts[1], service)
				return true
			case "disable":
				s.handleDisable(w, r, parts[1], service)
				return true
			default:
				return false
			}
		}
	}
	return false
}

func (s *Service) handleOperationGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpresp.Err(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/operations/")
	if id == "" || strings.Contains(id, "/") {
		writeGCPError(w, http.StatusNotFound, statusNotFound, "Operation not found.")
		return
	}
	data, err := s.store.Get(nsOperations, id)
	if err != nil || len(data) == 0 {
		writeGCPError(w, http.StatusNotFound, statusNotFound, fmt.Sprintf("Operation operations/%s not found.", id))
		return
	}
	httpresp.JSON(w, http.StatusOK, json.RawMessage(data))
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request, project string) {
	filter := r.URL.Query().Get("filter")
	switch filter {
	case "", "state:" + stateEnabled, "state:" + stateDisabled:
	default:
		writeGCPError(w, http.StatusBadRequest, statusInvalidArgument, fmt.Sprintf("Invalid filter: %q.", filter))
		return
	}
	pageSize := 50
	if v := r.URL.Query().Get("pageSize"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 200 {
			writeGCPError(w, http.StatusBadRequest, statusInvalidArgument, fmt.Sprintf("Invalid pageSize: %q.", v))
			return
		}
		if n > 0 {
			// pageSize=0 means unspecified, like the real API.
			pageSize = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("pageToken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeGCPError(w, http.StatusBadRequest, statusInvalidArgument, fmt.Sprintf("Invalid pageToken: %q.", v))
			return
		}
		offset = n
	}
	want := ""
	switch filter {
	case "state:" + stateEnabled:
		want = stateEnabled
	case "state:" + stateDisabled:
		want = stateDisabled
	}
	all := make([]map[string]any, 0, len(catalog))
	for _, entry := range catalog {
		st := s.getServiceState(project, entry.Name)
		if want != "" && st != want {
			continue
		}
		all = append(all, s.serviceResource(project, entry, st))
	}
	if offset > len(all) {
		writeGCPError(w, http.StatusBadRequest, statusInvalidArgument,
			fmt.Sprintf("Invalid pageToken: %q.", r.URL.Query().Get("pageToken")))
		return
	}
	page := []map[string]any{}
	next := ""
	if offset < len(all) {
		end := min(offset+pageSize, len(all))
		page = all[offset:end]
		if end < len(all) {
			next = strconv.Itoa(end)
		}
	}
	resp := map[string]any{servicesSegment: page}
	if next != "" {
		resp["nextPageToken"] = next
	}
	httpresp.JSON(w, http.StatusOK, resp)
}

func (s *Service) handleGet(w http.ResponseWriter, _ *http.Request, project, service string) {
	entry, ok := catalogLookup(service)
	if !ok {
		writeGCPError(w, http.StatusNotFound, statusNotFound, fmt.Sprintf(errServiceNotFound, service))
		return
	}
	httpresp.JSON(w, http.StatusOK, s.serviceResource(project, entry, s.getServiceState(project, service)))
}

func (s *Service) handleEnable(w http.ResponseWriter, _ *http.Request, project, service string) {
	entry, ok := catalogLookup(service)
	if !ok {
		writeGCPError(w, http.StatusNotFound, statusNotFound, fmt.Sprintf(errServiceNotFound, service))
		return
	}
	if err := s.putServiceState(project, service, stateEnabled); err != nil {
		writeGCPError(w, http.StatusInternalServerError, statusInternal, err.Error())
		return
	}
	s.writeOperation(w, map[string]any{
		fieldResponseType: respTypePrefix + "EnableServiceResponse",
		"service":         s.serviceResource(project, entry, stateEnabled),
	})
}

func (s *Service) handleDisable(w http.ResponseWriter, _ *http.Request, project, service string) {
	entry, ok := catalogLookup(service)
	if !ok {
		writeGCPError(w, http.StatusNotFound, statusNotFound, fmt.Sprintf(errServiceNotFound, service))
		return
	}
	if st := s.getServiceState(project, service); st != stateEnabled {
		writeGCPError(w, http.StatusBadRequest, statusFailedPrecond, fmt.Sprintf("Service %s is not enabled.", service))
		return
	}
	if err := s.putServiceState(project, service, stateDisabled); err != nil {
		writeGCPError(w, http.StatusInternalServerError, statusInternal, err.Error())
		return
	}
	s.writeOperation(w, map[string]any{
		fieldResponseType: respTypePrefix + "DisableServiceResponse",
		"service":         s.serviceResource(project, entry, stateDisabled),
	})
}

func (s *Service) handleBatchEnable(w http.ResponseWriter, r *http.Request, project string) {
	var body struct {
		ServiceIDs []string `json:"serviceIds"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeGCPError(w, http.StatusBadRequest, statusInvalidArgument, "invalid request body.")
			return
		}
	}
	if len(body.ServiceIDs) > 20 {
		writeGCPError(w, http.StatusBadRequest, statusInvalidArgument, "batchEnable supports up to 20 services.")
		return
	}
	entries := make([]catalogEntry, 0, len(body.ServiceIDs))
	for _, id := range body.ServiceIDs {
		entry, ok := catalogLookup(id)
		if !ok {
			writeGCPError(w, http.StatusNotFound, statusNotFound, fmt.Sprintf(errServiceNotFound, id))
			return
		}
		entries = append(entries, entry)
	}
	resources := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if err := s.putServiceState(project, entry.Name, stateEnabled); err != nil {
			writeGCPError(w, http.StatusInternalServerError, statusInternal, err.Error())
			return
		}
		resources = append(resources, s.serviceResource(project, entry, stateEnabled))
	}
	s.writeOperation(w, map[string]any{
		fieldResponseType: respTypePrefix + "BatchEnableServicesResponse",
		servicesSegment:   resources,
	})
}

// writeOperation stores the completed operation under its id and returns it.
func (s *Service) writeOperation(w http.ResponseWriter, resp any) {
	id := strconv.FormatUint(s.opSeq.Add(1), 10)
	op := map[string]any{
		fieldName:  "operations/" + id,
		"done":     true,
		"response": resp,
	}
	data, err := json.Marshal(op)
	if err != nil {
		writeGCPError(w, http.StatusInternalServerError, statusInternal, err.Error())
		return
	}
	if err := s.store.Put(nsOperations, id, data); err != nil {
		writeGCPError(w, http.StatusInternalServerError, statusInternal, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, op)
}

func catalogLookup(name string) (catalogEntry, bool) {
	for _, entry := range catalog {
		if entry.Name == name {
			return entry, true
		}
	}
	return catalogEntry{}, false
}

func serviceKey(project, service string) string {
	return fmt.Sprintf("projects/%s/services/%s", project, service)
}

// getServiceState returns the stored state; an absent record means ENABLED.
func (s *Service) getServiceState(project, service string) string {
	data, err := s.store.Get(nsServices, serviceKey(project, service))
	if err != nil || len(data) == 0 {
		return stateEnabled
	}
	var rec struct {
		State string `json:"state"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.State == "" {
		return stateEnabled
	}
	return rec.State
}

func (s *Service) putServiceState(project, service, stateStr string) error {
	data, _ := json.Marshal(map[string]string{"state": stateStr})
	return s.store.Put(nsServices, serviceKey(project, service), data)
}

func (s *Service) serviceResource(project string, entry catalogEntry, stateStr string) map[string]any {
	return map[string]any{
		fieldName: serviceKey(project, entry.Name),
		"parent":  fmt.Sprintf("projects/%s", project),
		"state":   stateStr,
		"config": map[string]any{
			fieldName: entry.Name,
			"title":   entry.Title,
		},
	}
}

func writeGCPError(w http.ResponseWriter, code int, status, msg string) {
	httpresp.JSON(w, code, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
			"status":  status,
		},
	})
}
