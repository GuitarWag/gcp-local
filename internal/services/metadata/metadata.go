// Package metadata implements a GCE-shaped metadata server for ADC
// flows. Real GCE serves these endpoints from
// http://metadata.google.internal (or 169.254.169.254) on port 80;
// SDK clients honour GCE_METADATA_HOST to point elsewhere, which is
// how tests redirect them at the emulator's gateway port.
package metadata

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	flavorHeader = "Metadata-Flavor"
	flavorValue  = "Google"
	keyID        = "gcp-local-key-1"
	issuer       = "gcp-local"
)

type Service struct {
	project    string
	email      string
	scopes     []string
	privateKey *rsa.PrivateKey
}

func New(_ state.Store, cfg *config.Config) (*Service, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate rsa key: %w", err)
	}
	project := "local-project"
	if cfg != nil && cfg.Project != "" {
		project = cfg.Project
	}
	return &Service{
		project:    project,
		email:      fmt.Sprintf("gcp-local@%s.iam.gserviceaccount.com", project),
		scopes:     []string{"https://www.googleapis.com/auth/cloud-platform"},
		privateKey: key,
	}, nil
}

func (s *Service) Name() string { return "metadata" }

// PublicKey exposes the RSA public key used to sign identity JWTs, so
// other services (iamcredentials) can share the same key material.
func (s *Service) PublicKey() *rsa.PublicKey { return &s.privateKey.PublicKey }

// SignIDToken issues a Google-style ID token. Used by both the
// metadata server's /identity endpoint and the iamcredentials
// generateIdToken RPC.
func (s *Service) SignIDToken(audience, sub, email string) (string, error) {
	now := time.Now().Unix()
	if sub == "" {
		sub = "000000000000000000000"
	}
	if email == "" {
		email = s.email
	}
	claims := map[string]any{
		"iss":            issuer,
		"sub":            sub,
		"aud":            audience,
		"exp":            now + 3600,
		"iat":            now,
		"email":          email,
		"email_verified": true,
	}
	return s.signJWT(claims)
}

// SignAccessToken returns an opaque bearer string. Real tokens are
// JWTs, but the gateway accepts any string, so we return a stable
// identifier and let callers refresh whenever they want.
func (s *Service) SignAccessToken() (string, int64) {
	return "gcp-local-access-token", 3600
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/computeMetadata/", s.handle)
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// JWKS lives outside computeMetadata namespace conceptually but we
	// piggyback on the same handler tree to avoid leaking yet another
	// public route name. Real GCP exposes signing keys via OIDC
	// discovery; here we just publish them for completeness.
	if r.URL.Path == "/computeMetadata/v1/jwks" {
		s.writeJWKS(w)
		return
	}
	if r.Header.Get(flavorHeader) != flavorValue {
		// Real metadata server returns 403 with a JSON-ish body when the
		// Metadata-Flavor header is missing. SDKs always include it.
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Missing Metadata-Flavor:Google header.\n"))
		return
	}
	w.Header().Set(flavorHeader, flavorValue)

	path := strings.TrimPrefix(r.URL.Path, "/computeMetadata/v1")
	switch path {
	case "", "/":
		writePlain(w, "instance/\nproject/\n")
	case "/instance", "/instance/":
		writePlain(w, "service-accounts/\n")
	case "/instance/service-accounts", "/instance/service-accounts/":
		writePlain(w, "default/\n")
	case "/instance/service-accounts/default", "/instance/service-accounts/default/":
		if r.URL.Query().Get("recursive") == "true" {
			writeJSON(w, map[string]any{
				"aliases": []string{"default"},
				"email":   s.email,
				"scopes":  s.scopes,
			})
			return
		}
		writePlain(w, "aliases\nemail\nidentity\nscopes\ntoken\n")
	case "/instance/service-accounts/default/email":
		writePlain(w, s.email)
	case "/instance/service-accounts/default/aliases":
		writePlain(w, "default")
	case "/instance/service-accounts/default/scopes":
		writePlain(w, strings.Join(s.scopes, "\n"))
	case "/instance/service-accounts/default/token":
		tok, ttl := s.SignAccessToken()
		writeJSON(w, map[string]any{
			"access_token": tok,
			"expires_in":   ttl,
			"token_type":   "Bearer",
		})
	case "/instance/service-accounts/default/identity":
		audience := r.URL.Query().Get("audience")
		if audience == "" {
			writeErr(w, http.StatusBadRequest, "non-empty audience parameter required")
			return
		}
		jwt, err := s.SignIDToken(audience, "", "")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writePlain(w, jwt)
	case "/project", "/project/":
		writePlain(w, "numeric-project-id\nproject-id\n")
	case "/project/project-id":
		writePlain(w, s.project)
	case "/project/numeric-project-id":
		// Stable made-up numeric id; real one is the project number not name.
		writePlain(w, "1234567890")
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

func (s *Service) signJWT(claims map[string]any) (string, error) {
	header := map[string]any{
		"alg": "RS256",
		"typ": "JWT",
		"kid": keyID,
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64url(hb) + "." + b64url(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64url(sig), nil
}

func (s *Service) writeJWKS(w http.ResponseWriter) {
	pub := s.privateKey.PublicKey
	w.Header().Set(flavorHeader, flavorValue)
	writeJSON(w, map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": keyID,
			"n":   b64url(pub.N.Bytes()),
			"e":   b64url(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

// PublicKeyPEM returns the public key encoded as PEM. Handy for
// consumers that want to verify signatures without parsing JWKS.
func (s *Service) PublicKeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(&s.privateKey.PublicKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writePlain(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "application/text")
	_, _ = w.Write([]byte(s))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`+"\n", code, msg)
}
