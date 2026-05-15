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
	"sort"
	"strings"
)

// ConsoleKeyRings lists every key ring.
func (s *Service) ConsoleKeyRings() ([]map[string]any, error) {
	all, err := s.store.List(nsKeyRings, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var k keyRingResource
		if json.Unmarshal(v, &k) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":       k.Name,
			"id":         lastSegment(k.Name),
			"createTime": k.CreateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleCryptoKeys lists the keys under a given key ring.
func (s *Service) ConsoleCryptoKeys(keyring string) ([]map[string]any, error) {
	prefix := keyring + "/cryptoKeys/"
	all, err := s.store.List(nsCryptoKeys, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var k cryptoKeyResource
		if json.Unmarshal(v, &k) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":       k.Name,
			"id":         lastSegment(k.Name),
			"purpose":    k.Purpose,
			"createTime": k.CreateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleEncrypt accepts plaintext as a UTF-8 string (the console form
// is text, not binary) and returns the base64-encoded ciphertext.
func (s *Service) ConsoleEncrypt(key, plaintext string) (string, error) {
	gcm, err := s.loadGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// ConsoleDecrypt takes base64 ciphertext and returns plaintext as a
// UTF-8 string. Non-UTF-8 plaintext is base64-encoded with a marker so
// the console doesn't render garbage.
func (s *Service) ConsoleDecrypt(key, ciphertext string) (string, error) {
	gcm, err := s.loadGCM(key)
	if err != nil {
		return "", err
	}
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return "", fmt.Errorf("ciphertext not base64: %w", err)
	}
	if len(ct) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := ct[:gcm.NonceSize()], ct[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) loadGCM(key string) (cipher.AEAD, error) {
	raw, err := s.loadKey(key)
	if err != nil {
		return nil, errors.New("cryptoKey not found")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
