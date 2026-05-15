package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	nsKeyRings   = "kms/keyrings"
	nsCryptoKeys = "kms/cryptokeys"
)

type keyRingResource struct {
	Name       string    `json:"name"`
	CreateTime time.Time `json:"createTime"`
}

type cryptoKeyResource struct {
	Name       string    `json:"name"`
	Purpose    string    `json:"purpose"`
	CreateTime time.Time `json:"createTime"`
}

type cryptoKeyStored struct {
	Name       string    `json:"name"`
	Purpose    string    `json:"purpose"`
	CreateTime time.Time `json:"createTime"`
	Key        string    `json:"keyMaterial"` // base64
}

type encryptRequest struct {
	Plaintext string `json:"plaintext"`
}

type encryptResponse struct {
	Name       string `json:"name"`
	Ciphertext string `json:"ciphertext"`
}

type decryptRequest struct {
	Ciphertext string `json:"ciphertext"`
}

type decryptResponse struct {
	Plaintext string `json:"plaintext"`
}

type Service struct {
	store   state.Store
	project string
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{store: store, project: cfg.Project}, nil
}

func (s *Service) Name() string              { return "kms" }
func (s *Service) Register(_ *http.ServeMux) {}

// HandleV1 handles /v1/projects/{p}/locations/{loc}/keyRings/...
func (s *Service) HandleV1(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 5 || parts[2] != "locations" || parts[4] != "keyRings" {
		return false
	}
	switch len(parts) {
	case 5:
		s.keyRingCollection(w, r, parts)
		return true
	case 6:
		s.keyRingItem(w, r, parts)
		return true
	case 7:
		if parts[6] == "cryptoKeys" {
			s.cryptoKeyCollection(w, r, parts)
			return true
		}
		return false
	case 8:
		if parts[6] == "cryptoKeys" {
			s.cryptoKeyItem(w, r, parts)
			return true
		}
		return false
	}
	return false
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func keyRingName(p, l, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", p, l, id)
}

func cryptoKeyName(p, l, ring, id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", p, l, ring, id)
}

func (s *Service) keyRingCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	project, loc := parts[1], parts[3]
	switch r.Method {
	case http.MethodPost:
		id := r.URL.Query().Get("keyRingId")
		if id == "" {
			s.writeErr(w, http.StatusBadRequest, "keyRingId required")
			return
		}
		name := keyRingName(project, loc, id)
		if _, err := s.store.Get(nsKeyRings, name); err == nil {
			s.writeErr(w, http.StatusConflict, "keyRing exists")
			return
		}
		k := keyRingResource{Name: name, CreateTime: time.Now().UTC()}
		data, _ := json.Marshal(k)
		_ = s.store.Put(nsKeyRings, name, data)
		s.writeJSON(w, http.StatusOK, k)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/locations/%s/keyRings/", project, loc)
		all, _ := s.store.List(nsKeyRings, prefix)
		out := struct {
			KeyRings []keyRingResource `json:"keyRings"`
		}{KeyRings: []keyRingResource{}}
		for _, v := range all {
			var kr keyRingResource
			if json.Unmarshal(v, &kr) == nil {
				out.KeyRings = append(out.KeyRings, kr)
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) keyRingItem(w http.ResponseWriter, r *http.Request, parts []string) {
	name := keyRingName(parts[1], parts[3], parts[5])
	if r.Method != http.MethodGet {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	data, err := s.store.Get(nsKeyRings, name)
	if errors.Is(err, state.ErrNotFound) {
		s.writeErr(w, http.StatusNotFound, "keyRing not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Service) cryptoKeyCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	project, loc, ring := parts[1], parts[3], parts[5]
	ringName := keyRingName(project, loc, ring)
	if _, err := s.store.Get(nsKeyRings, ringName); err != nil {
		s.writeErr(w, http.StatusNotFound, "keyRing not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		id := r.URL.Query().Get("cryptoKeyId")
		if id == "" {
			s.writeErr(w, http.StatusBadRequest, "cryptoKeyId required")
			return
		}
		var body struct {
			Purpose string `json:"purpose"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Purpose == "" {
			body.Purpose = "ENCRYPT_DECRYPT"
		}
		name := cryptoKeyName(project, loc, ring, id)
		if _, err := s.store.Get(nsCryptoKeys, name); err == nil {
			s.writeErr(w, http.StatusConflict, "cryptoKey exists")
			return
		}
		keyBytes := make([]byte, 32)
		if _, err := rand.Read(keyBytes); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		stored := cryptoKeyStored{
			Name:       name,
			Purpose:    body.Purpose,
			CreateTime: time.Now().UTC(),
			Key:        base64.StdEncoding.EncodeToString(keyBytes),
		}
		data, _ := json.Marshal(stored)
		_ = s.store.Put(nsCryptoKeys, name, data)
		s.writeJSON(w, http.StatusOK, cryptoKeyResource{
			Name:       stored.Name,
			Purpose:    stored.Purpose,
			CreateTime: stored.CreateTime,
		})
	case http.MethodGet:
		prefix := ringName + "/cryptoKeys/"
		all, _ := s.store.List(nsCryptoKeys, prefix)
		out := struct {
			CryptoKeys []cryptoKeyResource `json:"cryptoKeys"`
		}{CryptoKeys: []cryptoKeyResource{}}
		for _, v := range all {
			var st cryptoKeyStored
			if json.Unmarshal(v, &st) == nil {
				out.CryptoKeys = append(out.CryptoKeys, cryptoKeyResource{
					Name: st.Name, Purpose: st.Purpose, CreateTime: st.CreateTime,
				})
			}
		}
		s.writeJSON(w, http.StatusOK, out)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) cryptoKeyItem(w http.ResponseWriter, r *http.Request, parts []string) {
	keyPart, action := splitAction(parts[7])
	name := cryptoKeyName(parts[1], parts[3], parts[5], keyPart)
	switch action {
	case "encrypt":
		s.encrypt(w, r, name)
		return
	case "decrypt":
		s.decrypt(w, r, name)
		return
	}
	if r.Method != http.MethodGet {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	data, err := s.store.Get(nsCryptoKeys, name)
	if err != nil {
		s.writeErr(w, http.StatusNotFound, "cryptoKey not found")
		return
	}
	var st cryptoKeyStored
	_ = json.Unmarshal(data, &st)
	s.writeJSON(w, http.StatusOK, cryptoKeyResource{
		Name: st.Name, Purpose: st.Purpose, CreateTime: st.CreateTime,
	})
}

func (s *Service) loadKey(name string) ([]byte, error) {
	data, err := s.store.Get(nsCryptoKeys, name)
	if err != nil {
		return nil, err
	}
	var st cryptoKeyStored
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(st.Key)
}

func (s *Service) encrypt(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body encryptRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	plain, err := base64.StdEncoding.DecodeString(body.Plaintext)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "plaintext not base64")
		return
	}
	key, err := s.loadKey(name)
	if err != nil {
		s.writeErr(w, http.StatusNotFound, "cryptoKey not found")
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ct := gcm.Seal(nonce, nonce, plain, nil)
	s.writeJSON(w, http.StatusOK, encryptResponse{
		Name:       name + "/cryptoKeyVersions/1",
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	})
}

func (s *Service) decrypt(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body decryptRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ct, err := base64.StdEncoding.DecodeString(body.Ciphertext)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "ciphertext not base64")
		return
	}
	key, err := s.loadKey(name)
	if err != nil {
		s.writeErr(w, http.StatusNotFound, "cryptoKey not found")
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(ct) < gcm.NonceSize() {
		s.writeErr(w, http.StatusBadRequest, "ciphertext too short")
		return
	}
	nonce, body2 := ct[:gcm.NonceSize()], ct[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body2, nil)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, "decryption failed")
		return
	}
	s.writeJSON(w, http.StatusOK, decryptResponse{
		Plaintext: base64.StdEncoding.EncodeToString(plain),
	})
}

func splitAction(s string) (string, string) {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
