// Package iamadmin emulates the iam.googleapis.com service-account
// admin surface: projects.serviceAccounts CRUD and the .keys subresource.
// It is intentionally separate from internal/services/iamcredentials,
// which serves the impersonation endpoints on the same URL prefix.
package iamadmin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	// NamespaceAccounts holds projects/{p}/serviceAccounts/{email}.
	NamespaceAccounts = "iamadmin/serviceAccounts"
	// NamespaceKeys holds {accountName}/keys/{keyId}.
	NamespaceKeys = "iamadmin/serviceAccountKeys"
)

// serviceAccount mirrors the iam.v1.ServiceAccount shape SDKs decode.
type serviceAccount struct {
	Name           string `json:"name"`
	ProjectID      string `json:"projectId"`
	UniqueID       string `json:"uniqueId"`
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	Description    string `json:"description,omitempty"`
	OAuth2ClientID string `json:"oauth2ClientId,omitempty"`
	Disabled       bool   `json:"disabled,omitempty"`
	Etag           string `json:"etag,omitempty"`
}

type serviceAccountKey struct {
	Name            string `json:"name"`
	PrivateKeyType  string `json:"privateKeyType,omitempty"`
	KeyAlgorithm    string `json:"keyAlgorithm,omitempty"`
	PrivateKeyData  string `json:"privateKeyData,omitempty"`
	PublicKeyData   string `json:"publicKeyData,omitempty"`
	ValidAfterTime  string `json:"validAfterTime,omitempty"`
	ValidBeforeTime string `json:"validBeforeTime,omitempty"`
	KeyOrigin       string `json:"keyOrigin,omitempty"`
	KeyType         string `json:"keyType,omitempty"`
}

type Service struct {
	store   state.Store
	project string
}

func New(store state.Store, cfg *config.Config) *Service {
	return &Service{store: store, project: cfg.Project}
}

func (s *Service) Name() string              { return "iamadmin" }
func (s *Service) Register(_ *http.ServeMux) {}

// HandleV1 dispatches /v1/projects/{p}/serviceAccounts[/{email}[/keys[/{keyId}]]].
// Returns false for paths the impersonation service (iamcredentials)
// owns — those go through a colon-action and are handled there.
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[2] != "serviceAccounts" {
		return false
	}
	switch len(parts) {
	case 3:
		s.collection(w, r, parts)
		return true
	case 4:
		// /v1/projects/{p}/serviceAccounts/{email}[:action]
		if strings.Contains(parts[3], ":") {
			// Colon actions on a service-account resource (e.g.
			// :generateAccessToken) belong to iamcredentials. Let the
			// next handler claim it.
			return false
		}
		s.item(w, r, parts)
		return true
	case 5:
		if parts[4] == "keys" {
			s.keysCollection(w, r, parts)
			return true
		}
	case 6:
		if parts[4] == "keys" {
			s.keyItem(w, r, parts)
			return true
		}
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

func accountName(project, email string) string {
	return fmt.Sprintf("projects/%s/serviceAccounts/%s", project, email)
}

func (s *Service) collection(w http.ResponseWriter, r *http.Request, parts []string) {
	project := parts[1]
	switch r.Method {
	case http.MethodPost:
		var body struct {
			AccountID      string         `json:"accountId"`
			ServiceAccount serviceAccount `json:"serviceAccount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.AccountID == "" {
			s.writeErr(w, http.StatusBadRequest, "accountId required")
			return
		}
		email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", body.AccountID, project)
		name := accountName(project, email)
		if _, err := s.store.Get(NamespaceAccounts, name); err == nil {
			s.writeErr(w, http.StatusConflict, "service account exists")
			return
		}
		sa := serviceAccount{
			Name:        name,
			ProjectID:   project,
			UniqueID:    randomID(21),
			Email:       email,
			DisplayName: body.ServiceAccount.DisplayName,
			Description: body.ServiceAccount.Description,
		}
		data, _ := json.Marshal(sa)
		if err := s.store.Put(NamespaceAccounts, name, data); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, sa)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/serviceAccounts/", project)
		all, _ := s.store.List(NamespaceAccounts, prefix)
		out := struct {
			Accounts []serviceAccount `json:"accounts"`
		}{Accounts: []serviceAccount{}}
		for _, v := range all {
			var sa serviceAccount
			if json.Unmarshal(v, &sa) == nil {
				out.Accounts = append(out.Accounts, sa)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) item(w http.ResponseWriter, r *http.Request, parts []string) {
	name := accountName(parts[1], parts[3])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(NamespaceAccounts, name)
		if errors.Is(err, state.ErrNotFound) {
			s.writeErr(w, http.StatusNotFound, "service account not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(NamespaceAccounts, name); err != nil {
			s.writeErr(w, http.StatusNotFound, "service account not found")
			return
		}
		// Cascade-delete the account's keys.
		keyPrefix := name + "/keys/"
		all, _ := s.store.List(NamespaceKeys, keyPrefix)
		for k := range all {
			_ = s.store.Delete(NamespaceKeys, k)
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodPatch:
		// Update via field mask — we accept the merged shape and write
		// it back, ignoring updateMask. SDKs include displayName /
		// description here.
		var patch serviceAccount
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		data, err := s.store.Get(NamespaceAccounts, name)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "service account not found")
			return
		}
		var sa serviceAccount
		_ = json.Unmarshal(data, &sa)
		if patch.DisplayName != "" {
			sa.DisplayName = patch.DisplayName
		}
		if patch.Description != "" {
			sa.Description = patch.Description
		}
		out, _ := json.Marshal(sa)
		_ = s.store.Put(NamespaceAccounts, name, out)
		s.writeJSON(w, http.StatusOK, sa)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) keysCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	name := accountName(parts[1], parts[3])
	if _, err := s.store.Get(NamespaceAccounts, name); err != nil {
		s.writeErr(w, http.StatusNotFound, "service account not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		now := time.Now().UTC()
		keyID := randomID(40)
		fullName := name + "/keys/" + keyID
		// Emit a stable, obviously-fake "private key" payload so SDKs that
		// expect a base64 PEM see something well-formed. We do not generate
		// a real RSA keypair on purpose: keys minted here cannot be used
		// to sign tokens for real GCP. The metadata server's signing key
		// is the one tests should use when they need a verifiable JWT.
		key := serviceAccountKey{
			Name:            fullName,
			PrivateKeyType:  "TYPE_GOOGLE_CREDENTIALS_FILE",
			KeyAlgorithm:    "KEY_ALG_RSA_2048",
			PrivateKeyData:  base64.StdEncoding.EncodeToString([]byte(fakeCredJSON(parts[3], keyID))),
			ValidAfterTime:  now.Format(time.RFC3339),
			ValidBeforeTime: now.AddDate(10, 0, 0).Format(time.RFC3339),
			KeyOrigin:       "GOOGLE_PROVIDED",
			KeyType:         "USER_MANAGED",
		}
		data, _ := json.Marshal(key)
		_ = s.store.Put(NamespaceKeys, fullName, data)
		s.writeJSON(w, http.StatusOK, key)
	case http.MethodGet:
		prefix := name + "/keys/"
		all, _ := s.store.List(NamespaceKeys, prefix)
		out := struct {
			Keys []serviceAccountKey `json:"keys"`
		}{Keys: []serviceAccountKey{}}
		for _, v := range all {
			var k serviceAccountKey
			if json.Unmarshal(v, &k) == nil {
				out.Keys = append(out.Keys, k)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) keyItem(w http.ResponseWriter, r *http.Request, parts []string) {
	full := fmt.Sprintf("projects/%s/serviceAccounts/%s/keys/%s", parts[1], parts[3], parts[5])
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(NamespaceKeys, full)
		if err != nil {
			s.writeErr(w, http.StatusNotFound, "key not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if err := s.store.Delete(NamespaceKeys, full); err != nil {
			s.writeErr(w, http.StatusNotFound, "key not found")
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func randomID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0000000000"
	}
	// Crockford-ish alphabet keeps the result URL-safe and free of
	// characters that confuse downstream parsers.
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf)
}

func fakeCredJSON(email, keyID string) string {
	return fmt.Sprintf(`{"type":"service_account","client_email":%q,"private_key_id":%q,"private_key":"-----BEGIN PRIVATE KEY-----\nfake-emulator-key\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token"}`, email, keyID)
}
