package workflows

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/GuitarWag/gcp-local/internal/httpresp"
)

// googleRPCStatus mirrors the google.rpc.Status wire shape used in a done
// Operation's `error` field.
type googleRPCStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// operationSeq mints operation ids: zero-padded, monotonic, unique within
// a process lifetime.
var operationSeq atomic.Int64

// operationName builds the resource name of an operation.
func operationName(project, loc string, seq int64) string {
	return fmt.Sprintf("projects/%s/locations/%s/operations/%012d", project, loc, seq)
}

// doneOperation is the stored JSON of an immediately-done LRO. Response
// holds the Any-encoded payload with its required "@type" field; the real
// LRO helpers deserialize `response` from that encoding.
type doneOperation struct {
	Name     string           `json:"name"`
	Done     bool             `json:"done"`
	Response map[string]any   `json:"response,omitempty"`
	Error    *googleRPCStatus `json:"error,omitempty"`
}

// storeDoneOperation mints an operation id, stores the done Operation in
// NamespaceOperations, and writes it to w. For create and patch, wf is
// the workflow record and status is nil. For delete, wf is nil and the
// response is a google.protobuf.Empty. For a duplicate create, status is
// the google.rpc.Status (ALREADY_EXISTS) that the operation carries in
// place of a response.
func (s *Service) storeDoneOperation(w http.ResponseWriter, project, loc string, wf *Workflow, status *googleRPCStatus) {
	name := operationName(project, loc, operationSeq.Add(1))
	op := doneOperation{Name: name, Done: true}
	if status != nil {
		op.Error = status
	} else if wf == nil {
		op.Response = map[string]any{anyTypeKey: anyTypeEmpty}
	} else {
		op.Response = workflowAny(wf)
	}
	data, err := json.Marshal(op)
	if err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.Put(NamespaceOperations, name, data); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, op)
}

// workflowAny encodes a workflow as the Any payload of an Operation
// response, with the required "@type" discriminator. A marshal failure
// degrades to the type-only payload rather than failing the whole LRO.
func workflowAny(wf *Workflow) map[string]any {
	data, err := json.Marshal(wf)
	if err != nil {
		return map[string]any{anyTypeKey: anyTypeWorkflow}
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{anyTypeKey: anyTypeWorkflow}
	}
	m[anyTypeKey] = "type.googleapis.com/google.cloud.workflows.v1.Workflow"
	return m
}

// operationGet implements GET .../operations/{op} for
// workflows.googleapis.com/v1.
func (s *Service) operationGet(w http.ResponseWriter, _ *http.Request, project, loc, opID string) {
	name := fmt.Sprintf("projects/%s/locations/%s/operations/%s", project, loc, opID)
	data, err := s.store.Get(NamespaceOperations, name)
	if err != nil {
		httpresp.Err(w, http.StatusNotFound, "operation not found")
		return
	}
	var op doneOperation
	if err := json.Unmarshal(data, &op); err != nil {
		httpresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, op)
}
