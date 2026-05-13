package secretmanager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	nsSecrets  = "secretmanager/secrets"
	nsVersions = "secretmanager/versions"
)

type secretResource struct {
	Name       string            `json:"name"`
	CreateTime time.Time         `json:"createTime"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type versionResource struct {
	Name       string    `json:"name"`
	CreateTime time.Time `json:"createTime"`
	State      string    `json:"state"`
	PayloadB64 string    `json:"-"`
}

type accessResponse struct {
	Name    string                `json:"name"`
	Payload accessResponsePayload `json:"payload"`
}

type accessResponsePayload struct {
	Data string `json:"data"`
}

type Service struct {
	store   state.Store
	project string
	mu      sync.Mutex
	verSeq  map[string]int
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{
		store:   store,
		project: cfg.Project,
		verSeq:  map[string]int{},
	}, nil
}

func (s *Service) Name() string              { return "secretmanager" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[2] != "secrets" {
		return false
	}
	switch len(parts) {
	case 3:
		// /v1/projects/{p}/secrets
		s.collection(w, r, parts)
		return true
	case 4:
		// /v1/projects/{p}/secrets/{id} or {id}:action
		s.secretItem(w, r, parts)
		return true
	case 5:
		if parts[4] == "versions" {
			s.listVersions(w, r, parts)
			return true
		}
		return false
	case 6:
		// /v1/projects/{p}/secrets/{id}/versions/{ver}[:action]
		s.versionItem(w, r, parts)
		return true
	}
	return false
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

func secretName(project, id string) string {
	return fmt.Sprintf("projects/%s/secrets/%s", project, id)
}

func (s *Service) collection(w http.ResponseWriter, r *http.Request, parts []string) {
	project := parts[1]
	switch r.Method {
	case http.MethodPost:
		id := r.URL.Query().Get("secretId")
		if id == "" {
			s.writeErr(w, http.StatusBadRequest, "secretId query parameter required")
			return
		}
		name := secretName(project, id)
		if _, err := s.store.Get(nsSecrets, name); err == nil {
			s.writeErr(w, http.StatusConflict, "secret exists")
			return
		}
		var body struct {
			Labels map[string]string `json:"labels"`
		}
		if r.ContentLength > 0 {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		res := secretResource{Name: name, CreateTime: time.Now().UTC(), Labels: body.Labels}
		data, _ := json.Marshal(res)
		if err := s.store.Put(nsSecrets, name, data); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, res)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/secrets/", project)
		all, _ := s.store.List(nsSecrets, prefix)
		out := struct {
			Secrets []secretResource `json:"secrets"`
		}{Secrets: []secretResource{}}
		for _, v := range all {
			var sec secretResource
			if err := json.Unmarshal(v, &sec); err == nil {
				out.Secrets = append(out.Secrets, sec)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) secretItem(w http.ResponseWriter, r *http.Request, parts []string) {
	project := parts[1]
	id, action := splitAction(parts[3])
	name := secretName(project, id)
	if action == "addVersion" {
		s.addVersion(w, r, name)
		return
	}
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsSecrets, name)
		if errors.Is(err, state.ErrNotFound) {
			s.writeErr(w, http.StatusNotFound, "secret not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(nsSecrets, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "secret not found")
			return
		}
		// cascade delete: drop all versions belonging to this secret
		verPrefix := name + "/versions/"
		if all, err := s.store.List(nsVersions, verPrefix); err == nil {
			for k := range all {
				_ = s.store.Delete(nsVersions, k)
			}
		}
		s.mu.Lock()
		delete(s.verSeq, name)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) addVersion(w http.ResponseWriter, r *http.Request, secretName string) {
	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := s.store.Get(nsSecrets, secretName); err != nil {
		s.writeErr(w, http.StatusNotFound, "secret not found")
		return
	}
	var body struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	s.mu.Lock()
	s.verSeq[secretName]++
	verNum := s.verSeq[secretName]
	s.mu.Unlock()
	verName := fmt.Sprintf("%s/versions/%d", secretName, verNum)
	v := versionResource{
		Name:       verName,
		CreateTime: time.Now().UTC(),
		State:      "ENABLED",
		PayloadB64: body.Payload.Data,
	}
	data, _ := json.Marshal(struct {
		versionResource
		PayloadB64 string `json:"payload_b64"`
	}{v, v.PayloadB64})
	if err := s.store.Put(nsVersions, verName, data); err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, v)
}

func (s *Service) listVersions(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	project := parts[1]
	id := parts[3]
	prefix := fmt.Sprintf("projects/%s/secrets/%s/versions/", project, id)
	all, _ := s.store.List(nsVersions, prefix)
	out := struct {
		Versions []versionResource `json:"versions"`
	}{Versions: []versionResource{}}
	for _, v := range all {
		var ver versionResource
		if err := json.Unmarshal(v, &ver); err == nil {
			out.Versions = append(out.Versions, ver)
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Service) versionItem(w http.ResponseWriter, r *http.Request, parts []string) {
	project := parts[1]
	id := parts[3]
	verPart := parts[5]
	verName, action := splitAction(verPart)

	// resolve latest -> max version number
	if verName == "latest" {
		prefix := fmt.Sprintf("projects/%s/secrets/%s/versions/", project, id)
		all, _ := s.store.List(nsVersions, prefix)
		highest := 0
		for k := range all {
			n, _ := strconv.Atoi(strings.TrimPrefix(k, prefix))
			if n > highest {
				highest = n
			}
		}
		if highest == 0 {
			s.writeErr(w, http.StatusNotFound, "no versions")
			return
		}
		verName = strconv.Itoa(highest)
	}

	full := fmt.Sprintf("projects/%s/secrets/%s/versions/%s", project, id, verName)
	if action == "access" {
		data, err := s.store.Get(nsVersions, full)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "version not found")
			return
		}
		var stored struct {
			versionResource
			PayloadB64 string `json:"payload_b64"`
		}
		_ = json.Unmarshal(data, &stored)
		s.writeJSON(w, http.StatusOK, accessResponse{
			Name:    stored.Name,
			Payload: accessResponsePayload{Data: stored.PayloadB64},
		})
		return
	}
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsVersions, full)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "version not found")
			return
		}
		var stored versionResource
		_ = json.Unmarshal(data, &stored)
		s.writeJSON(w, http.StatusOK, stored)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func splitAction(s string) (string, string) {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// DecodePayload is exposed for tests that need to decode base64 payload data.
func DecodePayload(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
