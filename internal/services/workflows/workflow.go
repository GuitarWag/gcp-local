package workflows

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// revisionSeq is the sequence counter for revision ids. Real revision ids
// are per-workflow ("000001-xxx"); a package counter keeps the format and
// uniqueness without per-workflow bookkeeping. Atomic because create and
// patch handlers run on concurrent HTTP goroutines.
var revisionSeq atomic.Int64

// workflowName builds the resource name of a workflow.
func workflowName(project, loc, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/workflows/%s", project, loc, id)
}

// validateSource checks the YAML source the way real GCP does at deploy:
// it must parse, the top level must be a map that has `main`, and `main`
// must have a non-empty `steps` list.
func validateSource(source string) error {
	if len(source) > maxSourceBytes {
		return ErrSourceTooLarge
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		return fmt.Errorf("invalid workflow source: %w", err)
	}
	main, ok := doc["main"]
	if !ok {
		return errors.New("invalid workflow source: top level must have a 'main' key")
	}
	mainMap, ok := main.(map[string]any)
	if !ok {
		return errors.New("invalid workflow source: 'main' must be a map")
	}
	steps, ok := mainMap["steps"]
	if !ok {
		return errors.New("invalid workflow source: 'main' must have a 'steps' key")
	}
	stepsList, ok := steps.([]any)
	if !ok {
		return errors.New("invalid workflow source: 'main.steps' must be a list")
	}
	if len(stepsList) == 0 {
		return errors.New("invalid workflow source: 'main.steps' must not be empty")
	}
	return nil
}

// handleCreate implements POST .../workflows?workflowId={id}. It always
// answers 200 with a done Operation; a duplicate workflowId surfaces as
// google.rpc.Status code 6 (ALREADY_EXISTS) in the operation error,
// never as an HTTP 409.
func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request, project, loc string) {
	id := r.URL.Query().Get("workflowId")
	if !ValidateWorkflowID(id) {
		httpresp.Err(w, http.StatusBadRequest, ErrInvalidID.Error())
		return
	}
	var wf Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		httpresp.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateSource(wf.SourceContents); err != nil {
		httpresp.Err(w, http.StatusBadRequest, sourceError(err))
		return
	}
	name := workflowName(project, loc, id)
	now := time.Now().UTC()
	wf.Name = name
	wf.State = "ACTIVE"
	wf.CreateTime = now
	wf.UpdateTime = now
	wf.RevisionID = NewRevisionID(int(revisionSeq.Add(1)))

	if _, err := s.store.Get(NamespaceWorkflows, name); err == nil {
		status := &googleRPCStatus{Code: 6, Message: fmt.Sprintf("Workflow %q already exists", name)}
		s.storeDoneOperation(w, project, loc, &wf, status)
		return
	}
	if err := s.putWorkflow(&wf); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.storeDoneOperation(w, project, loc, &wf, nil)
}

// handleList implements GET .../workflows?pageSize&pageToken, newest
// first. The page token encodes the last returned record's createTime and
// name, so pagination is stable across calls.
func (s *Service) handleList(w http.ResponseWriter, r *http.Request, project, loc string) {
	prefix := fmt.Sprintf("projects/%s/locations/%s/workflows/", project, loc)
	all, err := s.store.List(NamespaceWorkflows, prefix)
	if err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []Workflow{}
	for _, v := range all {
		var wf Workflow
		if json.Unmarshal(v, &wf) == nil {
			items = append(items, wf)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreateTime.Equal(items[j].CreateTime) {
			return items[i].CreateTime.After(items[j].CreateTime)
		}
		return items[i].Name > items[j].Name
	})
	if tok := r.URL.Query().Get("pageToken"); tok != "" {
		for i, wf := range items {
			if listToken(wf) == tok {
				items = items[i+1:]
				break
			}
		}
	}
	pageSize := 0
	if ps := r.URL.Query().Get("pageSize"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil {
			pageSize = n
		}
	}
	nextPageToken := ""
	if pageSize > 0 && len(items) > pageSize {
		items = items[:pageSize]
		nextPageToken = listToken(items[len(items)-1])
	}
	resp := struct {
		Workflows     []Workflow `json:"workflows"`
		NextPageToken string     `json:"nextPageToken,omitempty"`
	}{Workflows: items, NextPageToken: nextPageToken}
	httpresp.JSON(w, http.StatusOK, resp)
}

// listToken encodes the pagination cursor for one workflow: its createTime
// and name, base64'd so the token is opaque on the wire.
func listToken(wf Workflow) string {
	raw := wf.CreateTime.Format(time.RFC3339Nano) + "|" + wf.Name
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// handleGet implements GET .../workflows/{w}.
func (s *Service) handleGet(w http.ResponseWriter, project, loc, id string) {
	data, err := s.store.Get(NamespaceWorkflows, workflowName(project, loc, id))
	if errors.Is(err, state.ErrNotFound) {
		httpresp.Err(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, wf)
}

// handlePatch implements PATCH .../workflows/{w}?updateMask. It applies
// the mask fields (or every writable field when the mask is absent),
// bumps updateTime, and mints a new revisionId when sourceContents
// changes. Source validation matches create.
func (s *Service) handlePatch(w http.ResponseWriter, r *http.Request, project, loc, id string) {
	name := workflowName(project, loc, id)
	data, err := s.store.Get(NamespaceWorkflows, name)
	if errors.Is(err, state.ErrNotFound) {
		httpresp.Err(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var existing Workflow
	if err := json.Unmarshal(data, &existing); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var update Workflow
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		httpresp.Err(w, http.StatusBadRequest, "invalid json")
		return
	}
	applyUpdate(&existing, &update, r.URL.Query().Get("updateMask"))
	if err := validateSource(existing.SourceContents); err != nil {
		httpresp.Err(w, http.StatusBadRequest, sourceError(err))
		return
	}
	existing.UpdateTime = time.Now().UTC()
	if sourceChanged(data, &existing) {
		existing.RevisionID = NewRevisionID(int(revisionSeq.Add(1)))
	}
	if err := s.putWorkflow(&existing); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.storeDoneOperation(w, project, loc, &existing, nil)
}

// applyUpdate copies the fields named in the updateMask (real v1 mask
// fields: sourceContents, description, labels, callLogLevel) from update
// into wf. An absent or empty mask copies every writable field.
func applyUpdate(wf *Workflow, update *Workflow, mask string) {
	if mask == "" {
		wf.SourceContents = update.SourceContents
		wf.Description = update.Description
		wf.Labels = update.Labels
		wf.CallLogLevel = update.CallLogLevel
		return
	}
	for _, f := range strings.Split(mask, ",") {
		switch strings.TrimSpace(f) {
		case "sourceContents":
			wf.SourceContents = update.SourceContents
		case "description":
			wf.Description = update.Description
		case "labels":
			wf.Labels = update.Labels
		case "callLogLevel":
			wf.CallLogLevel = update.CallLogLevel
		}
	}
}

// sourceChanged reports whether wf's source differs from the stored
// record's sourceContents.
func sourceChanged(stored []byte, wf *Workflow) bool {
	var old Workflow
	if json.Unmarshal(stored, &old) != nil {
		return true
	}
	return old.SourceContents != wf.SourceContents
}

// handleDelete implements DELETE .../workflows/{w}. Executions cascade in
// issue #98; here the record just goes away.
func (s *Service) handleDelete(w http.ResponseWriter, _ *http.Request, project, loc, id string) {
	name := workflowName(project, loc, id)
	if _, err := s.store.Get(NamespaceWorkflows, name); errors.Is(err, state.ErrNotFound) {
		httpresp.Err(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := s.store.Delete(NamespaceWorkflows, name); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.storeDoneOperation(w, project, loc, nil, nil)
}

// putWorkflow marshals and stores a workflow record.
func (s *Service) putWorkflow(wf *Workflow) error {
	data, err := json.Marshal(wf)
	if err != nil {
		return err
	}
	return s.store.Put(NamespaceWorkflows, wf.Name, data)
}

// sourceError maps a source-validation failure to its HTTP status code
// and message. Shared by create and patch.
func sourceError(err error) string {
	return err.Error()
}
