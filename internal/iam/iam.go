// Package iam emulates the resource-level Cloud IAM policy surface
// (getIamPolicy / setIamPolicy / testIamPermissions). It is a thin
// policy store on top of state.Store keyed by fully qualified resource
// name, and a small set of HTTP helpers that services mount on their
// own dispatcher.
//
// No permission enforcement happens here. The point is that SDK init
// paths and Terraform providers that call getIamPolicy on a resource
// (bucket, topic, secret, key, …) before doing anything else can round-
// trip their policies through the emulator. Read-then-write of an
// otherwise unused policy returns what was written.
package iam

import (
	"crypto/sha1" //nolint:gosec // etag, not a security primitive.
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// Namespace is exported so /admin/reset can wipe it alongside the
// other per-service namespaces.
const Namespace = "iam/policies"

// Policy mirrors the google.iam.v1.Policy JSON shape. The Etag field
// is recomputed from the canonicalised binding set on every write so
// concurrent edits collide with a deterministic value.
type Policy struct {
	Version  int       `json:"version"`
	Bindings []Binding `json:"bindings"`
	Etag     string    `json:"etag,omitempty"`
}

// Binding is a (role -> members) pair. Condition expressions are
// accepted on input (stored as raw JSON) so policies that include them
// round-trip, but they have no behavioural effect.
type Binding struct {
	Role      string          `json:"role"`
	Members   []string        `json:"members"`
	Condition json.RawMessage `json:"condition,omitempty"`
}

// Store is the per-resource policy persistence layer. Construct one
// per gateway and share it across services.
type Store struct {
	backend state.Store
}

// NewStore wires a Store onto the gateway's shared state.Store.
func NewStore(backend state.Store) *Store {
	return &Store{backend: backend}
}

// Get returns the policy persisted for resource. When nothing has been
// written yet it returns the empty policy (version 1, no bindings),
// matching real GCP behaviour for resources that have never had IAM
// touched.
func (s *Store) Get(resource string) (Policy, error) {
	data, err := s.backend.Get(Namespace, resource)
	if errors.Is(err, state.ErrNotFound) {
		return Policy{Version: 1, Bindings: []Binding{}, Etag: emptyEtag()}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return Policy{}, err
	}
	if p.Bindings == nil {
		p.Bindings = []Binding{}
	}
	return p, nil
}

// Set replaces the policy for resource. The Etag in p is ignored on
// write; we compute a fresh one. The returned policy is what callers
// should echo back to clients.
func (s *Store) Set(resource string, p Policy) (Policy, error) {
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Bindings == nil {
		p.Bindings = []Binding{}
	}
	p.Etag = computeEtag(p)
	data, err := json.Marshal(p)
	if err != nil {
		return Policy{}, err
	}
	if err := s.backend.Put(Namespace, resource, data); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Delete removes the policy for resource. Used by service deletion
// paths so that recreating a resource doesn't inherit stale bindings.
func (s *Store) Delete(resource string) error {
	err := s.backend.Delete(Namespace, resource)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return err
}

// IsVerb reports whether action is one of the three standard IAM
// colon-actions. Services use it from their dispatcher switch.
func IsVerb(action string) bool {
	switch action {
	case "getIamPolicy", "setIamPolicy", "testIamPermissions":
		return true
	}
	return false
}

// Handle services a colon-action IAM request against resource. It
// returns true when the action was an IAM verb (even if the request
// itself failed), so the caller can stop dispatching.
func (s *Store) Handle(w http.ResponseWriter, r *http.Request, resource, action string) bool {
	switch action {
	case "getIamPolicy":
		s.handleGet(w, r, resource)
		return true
	case "setIamPolicy":
		s.handleSet(w, r, resource)
		return true
	case "testIamPermissions":
		s.handleTest(w, r)
		return true
	}
	return false
}

func (s *Store) handleGet(w http.ResponseWriter, r *http.Request, resource string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Body fields (options.requestedPolicyVersion) are accepted and
	// ignored; we always return version 1 policies.
	_, _ = io.Copy(io.Discard, r.Body)
	p, err := s.Get(resource)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, p)
}

type setRequest struct {
	Policy     Policy `json:"policy"`
	UpdateMask string `json:"updateMask,omitempty"`
}

func (s *Store) handleSet(w http.ResponseWriter, r *http.Request, resource string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	out, err := s.Set(resource, req.Policy)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, out)
}

type testRequest struct {
	Permissions []string `json:"permissions"`
}

type testResponse struct {
	Permissions []string `json:"permissions"`
}

// handleTest echoes the requested permissions back. Real GCP filters
// the list down to the caller's actual grant; we have no notion of a
// caller, so granting everything is the closest harmless lie.
func (s *Store) handleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req testRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Permissions == nil {
		req.Permissions = []string{}
	}
	httpresp.JSON(w, http.StatusOK, testResponse(req))
}

// computeEtag derives a stable etag from the persisted bindings.
// Clients that round-trip the policy get back a value that changes
// only when the bindings change, which is enough for the
// CheckAndMutate flow real GCP advertises.
func computeEtag(p Policy) string {
	// Marshal a normalised copy (no etag) so the digest is stable.
	norm := Policy{Version: p.Version, Bindings: p.Bindings}
	data, _ := json.Marshal(norm)
	sum := sha1.Sum(data) //nolint:gosec
	return base64.StdEncoding.EncodeToString(sum[:8])
}

func emptyEtag() string {
	return computeEtag(Policy{Version: 1, Bindings: []Binding{}})
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	httpresp.JSON(w, code, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// HandleGCSPolicy services the GET/PUT /b/{bucket}/iam shape used by
// the Cloud Storage JSON API. PUT accepts a Policy directly (no
// "policy" envelope), unlike the v1 mixin used by every other service.
func (s *Store) HandleGCSPolicy(w http.ResponseWriter, r *http.Request, resource string) {
	switch r.Method {
	case http.MethodGet:
		p, err := s.Get(resource)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		httpresp.JSON(w, http.StatusOK, p)
	case http.MethodPut:
		var p Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		out, err := s.Set(resource, p)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		httpresp.JSON(w, http.StatusOK, out)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// HandleGCSTestPermissions services GET /b/{bucket}/iam/testPermissions
// ?permissions=p1&permissions=p2 used by the Cloud Storage JSON API.
func (s *Store) HandleGCSTestPermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	perms := r.URL.Query()["permissions"]
	if perms == nil {
		perms = []string{}
	}
	httpresp.JSON(w, http.StatusOK, testResponse{Permissions: perms})
}
