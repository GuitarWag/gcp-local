package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestKMSEncryptDecryptRoundTrip(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/" + project + "/locations/us-central1"

	resp, body := doJSON(t, http.MethodPost, base+"/keyRings?keyRingId=ring1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create keyring: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, base+"/keyRings/ring1/cryptoKeys?cryptoKeyId=key1", map[string]any{
		"purpose": "ENCRYPT_DECRYPT",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create key: %d %s", resp.StatusCode, body)
	}

	plain := []byte("hello kms")
	resp, body = doJSON(t, http.MethodPost, base+"/keyRings/ring1/cryptoKeys/key1:encrypt", map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString(plain),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", resp.StatusCode, body)
	}
	var er struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("decode encrypt: %v", err)
	}
	if er.Ciphertext == "" {
		t.Fatal("empty ciphertext")
	}

	resp, body = doJSON(t, http.MethodPost, base+"/keyRings/ring1/cryptoKeys/key1:decrypt", map[string]any{
		"ciphertext": er.Ciphertext,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decrypt: %d %s", resp.StatusCode, body)
	}
	var dr struct {
		Plaintext string `json:"plaintext"`
	}
	_ = json.Unmarshal(body, &dr)
	out, _ := base64.StdEncoding.DecodeString(dr.Plaintext)
	if string(out) != "hello kms" {
		t.Errorf("decrypted = %q", string(out))
	}
}
