// Package iamcredentials emulates the iamcredentials.googleapis.com
// surface SDKs hit when impersonating a service account. Only the two
// token endpoints exist; everything else returns 404.
package iamcredentials

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/services/metadata"
	"github.com/GuitarWag/gcp-local/internal/state"
)

type Service struct {
	meta *metadata.Service
}

func New(_ state.Store, _ *config.Config, meta *metadata.Service) (*Service, error) {
	return &Service{meta: meta}, nil
}

func (s *Service) Name() string              { return "iamcredentials" }
func (s *Service) Register(_ *http.ServeMux) {}

// HandleV1 matches /v1/projects/-/serviceAccounts/{email}:{method}. The
// project segment is normally "-" (the wildcard real GCP uses) but we
// accept anything to stay compatible with non-standard callers.
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) != 4 || parts[2] != "serviceAccounts" {
		return false
	}
	email, action := splitAction(parts[3])
	if email == "" || action == "" {
		return false
	}
	switch action {
	case "generateAccessToken":
		s.generateAccessToken(w, r, email)
	case "generateIdToken":
		s.generateIDToken(w, r, email)
	default:
		return false
	}
	return true
}

type accessTokenResp struct {
	AccessToken string `json:"accessToken"`
	ExpireTime  string `json:"expireTime"`
}

func (s *Service) generateAccessToken(w http.ResponseWriter, r *http.Request, _ string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Body fields (scope, lifetime, delegates) are accepted and ignored;
	// the emulator's tokens are static.
	tok, ttl := s.meta.SignAccessToken()
	expire := time.Now().Add(time.Duration(ttl) * time.Second).UTC().Format(time.RFC3339)
	httpresp.JSON(w, http.StatusOK, accessTokenResp{AccessToken: tok, ExpireTime: expire})
}

type idTokenReq struct {
	Audience     string   `json:"audience"`
	Delegates    []string `json:"delegates"`
	IncludeEmail bool     `json:"includeEmail"`
}

type idTokenResp struct {
	Token string `json:"token"`
}

func (s *Service) generateIDToken(w http.ResponseWriter, r *http.Request, email string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body idTokenReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Audience == "" {
		writeErr(w, http.StatusBadRequest, "audience required")
		return
	}
	signEmail := ""
	if body.IncludeEmail {
		signEmail = email
	}
	jwt, err := s.meta.SignIDToken(body.Audience, email, signEmail)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpresp.JSON(w, http.StatusOK, idTokenResp{Token: jwt})
}

func splitAction(s string) (string, string) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	httpresp.JSON(w, code, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
