package cloudrun

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// v1 serves the serving.knative.dev/v1 admin surface (Cloud Run v1):
// the regional form /v1/projects/{p}/locations/{l}/services[/{id}] and the
// namespaced form /v1/apis/serving.knative.dev/v1/namespaces/{p}/services[/{id}].
// Mutations return the real k8s-style shapes, never an operation envelope;
// each successful mutation also records a completed Operation.

const (
	nsOperations = "cloudrun/operations"
	v1Namespace  = "serving.knative.dev/v1"
)

const (
	segServices   = "services"
	segOperations = "operations"
)

// Shared JSON object keys. goconst flags repeated string literals.
const (
	keyName   = "name"
	keyKind   = "kind"
	keyStatus = "status"
	keyType   = "type"
)

func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if s.kind != KindService || len(parts) < 5 || parts[2] != "locations" {
		return false
	}
	switch {
	case parts[4] == segServices && len(parts) == 5:
		s.v1Collection(w, r, parts[1], parts[3])
	case parts[4] == segServices && len(parts) == 6:
		s.v1Item(w, r, parts[1], parts[3], parts[5])
	case parts[4] == segOperations && len(parts) == 5:
		s.listOperations(w, parts[1], parts[3])
	case parts[4] == segOperations && len(parts) == 6:
		s.getOperation(w, parts[1], parts[3], parts[5])
	case parts[4] == segServices || parts[4] == segOperations:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		return false
	}
	return true
}

// HandleNamespaced serves /v1/apis/serving.knative.dev/v1/namespaces/{p}/services[/{id}].
// Mounted on its own mux route. The URL carries no location, so the location
// is fixed to us-central1. Requests outside this prefix are ignored so other
// handlers can claim them.
func (s *Service) HandleNamespaced(w http.ResponseWriter, r *http.Request) {
	prefix := "/v1/apis/" + v1Namespace + "/namespaces/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(segments) < 2 || segments[1] != segServices {
		http.NotFound(w, r)
		return
	}
	project := segments[0]
	switch {
	case len(segments) == 2:
		s.v1Collection(w, r, project, defaultLocation)
	case len(segments) == 3:
		s.v1Item(w, r, project, defaultLocation, segments[2])
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) v1Collection(w http.ResponseWriter, r *http.Request, project, loc string) {
	switch r.Method {
	case http.MethodPost:
		s.v1Create(w, r, project, loc)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/locations/%s/%s/", project, loc, segServices)
		all, _ := s.store.List(s.ns, prefix)
		items := []map[string]any{}
		for _, v := range all {
			var res resource
			if json.Unmarshal(v, &res) == nil {
				items = append(items, s.buildService(res, project, r))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"apiVersion": v1Namespace,
			"kind":       "ServiceList",
			"items":      items,
			"metadata":   map[string]any{},
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) v1Item(w http.ResponseWriter, r *http.Request, project, loc, id string) {
	name := s.resourceName(project, loc, id)
	switch r.Method {
	case http.MethodGet:
		s.v1Get(w, r, name, project)
	case http.MethodPut:
		s.v1Replace(w, r, name, project, loc, id)
	case http.MethodDelete:
		if _, err := s.store.Get(s.ns, name); err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if err := s.store.Delete(s.ns, name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.runner.stop(name)
		s.recordOperation(w, project, loc, nil)
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) v1Get(w http.ResponseWriter, r *http.Request, name, project string) {
	data, err := s.store.Get(s.ns, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var res resource
	if err := json.Unmarshal(data, &res); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.buildService(res, project, r))
}

func (s *Service) v1Create(w http.ResponseWriter, r *http.Request, project, loc string) {
	var body resource
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := body.metadataName()
	if id == "" {
		writeErr(w, http.StatusBadRequest, "metadata.name required")
		return
	}
	name := s.resourceName(project, loc, id)
	if _, err := s.store.Get(s.ns, name); err == nil {
		writeErr(w, http.StatusConflict, "exists")
		return
	}
	body.Name = name
	body.Kind = s.kind
	if body.CreateTime.IsZero() {
		body.CreateTime = time.Now().UTC()
	}
	body.Generation = 1
	body.UID = randomHexID()
	body.specFromMetadata(id)
	data, _ := json.Marshal(body)
	if err := s.store.Put(s.ns, name, data); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := s.buildService(body, project, r)
	s.recordOperation(w, project, loc, svc)
	writeJSON(w, http.StatusOK, svc)
}

func (s *Service) v1Replace(w http.ResponseWriter, r *http.Request, name, project, loc, id string) {
	data, err := s.store.Get(s.ns, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var prev resource
	if err := json.Unmarshal(data, &prev); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body resource
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	body.Name = name
	body.Kind = s.kind
	if body.CreateTime.IsZero() {
		body.CreateTime = prev.CreateTime
	}
	body.Generation = prev.Generation + 1
	if body.UID == "" {
		body.UID = prev.UID
	}
	body.specFromMetadata(id)
	next, _ := json.Marshal(body)
	if err := s.store.Put(s.ns, name, next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := s.buildService(body, project, r)
	s.recordOperation(w, project, loc, svc)
	writeJSON(w, http.StatusOK, svc)
}

// specFromMetadata moves the request's metadata labels and annotations to
// the stored fields and pins metadata.name to the URL id.
func (res *resource) specFromMetadata(id string) {
	if res.Metadata != nil {
		res.Labels = res.Metadata.Labels
		res.Annotations = res.Metadata.Annotations
	}
	res.Metadata = &v1Metadata{Name: id}
}

// v2View strips v1-only fields so v2 responses keep their original shape.
func v2View(res resource) resource {
	res.Spec = nil
	res.Metadata = nil
	res.Labels = nil
	res.Annotations = nil
	res.Generation = 0
	res.UID = ""
	return res
}

func (s *Service) buildService(res resource, project string, r *http.Request) map[string]any {
	gen := res.Generation
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s", scheme, r.Host)
	id := res.metadataName()
	rev := fmt.Sprintf("%s-%05d", id, gen)
	return map[string]any{
		"apiVersion": v1Namespace,
		"kind":       "Service",
		"metadata": map[string]any{
			keyName:             id,
			"namespace":         project,
			"uid":               res.UID,
			"resourceVersion":   strconv.FormatInt(gen, 10),
			"generation":        gen,
			"creationTimestamp": res.CreateTime.UTC().Format(time.RFC3339),
			"labels":            nonNilMap(res.Labels),
			"annotations":       nonNilMap(res.Annotations),
			"selfLink":          fmt.Sprintf("%s/v1/projects/%s/locations/us-central1/services/%s", base, project, id),
		},
		"spec": res.Spec,
		"status": map[string]any{
			"observedGeneration":        gen,
			"conditions":                readyConditions(res.CreateTime),
			"latestCreatedRevisionName": rev,
			"latestReadyRevisionName":   rev,
			"url":                       base,
			"traffic":                   []map[string]any{{"percent": 100, "latestRevision": true}},
		},
	}
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func readyConditions(create time.Time) []map[string]any {
	when := create.UTC().Format(time.RFC3339)
	types := []string{"Ready", "ConfigurationsReady", "RoutesReady"}
	out := make([]map[string]any, 0, len(types))
	for _, t := range types {
		out = append(out, map[string]any{
			keyType:              t,
			keyStatus:            "True",
			"lastTransitionTime": when,
		})
	}
	return out
}

// recordOperation stores one completed Operation for a successful mutation.
// A nil svc marks a delete, which carries no response field.
func (s *Service) recordOperation(w http.ResponseWriter, project, loc string, svc map[string]any) {
	key := fmt.Sprintf("projects/%s/locations/%s/%s/%s", project, loc, segOperations, randomHexID())
	op := map[string]any{
		keyName: key,
		"done":  true,
	}
	if svc != nil {
		op["response"] = svc
	}
	data, _ := json.Marshal(op)
	if err := s.store.Put(nsOperations, key, data); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Service) listOperations(w http.ResponseWriter, project, loc string) {
	prefix := fmt.Sprintf("projects/%s/locations/%s/%s/", project, loc, segOperations)
	all, _ := s.store.List(nsOperations, prefix)
	ops := []map[string]any{}
	for _, v := range all {
		var op map[string]any
		if json.Unmarshal(v, &op) == nil {
			ops = append(ops, op)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
}

func (s *Service) getOperation(w http.ResponseWriter, project, loc, id string) {
	key := fmt.Sprintf("projects/%s/locations/%s/%s/%s", project, loc, segOperations, id)
	data, err := s.store.Get(nsOperations, key)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func randomHexID() string {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
