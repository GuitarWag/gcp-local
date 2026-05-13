package gcplocaltest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestStorageDuplicateBucketReturns409(t *testing.T) {
	em := testutil.Start(t)
	url := em.URL("/storage/v1/b?project=" + project)

	resp, body := doJSON(t, http.MethodPost, url, map[string]any{"name": "dup-bucket"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first create: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPost, url, map[string]any{"name": "dup-bucket"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 on duplicate bucket, got %d %s", resp.StatusCode, body)
	}
}

func TestSecretManagerDuplicateSecretReturns409(t *testing.T) {
	em := testutil.Start(t)
	url := em.URL("/v1/projects/" + project + "/secrets?secretId=dup-secret")

	resp, body := doJSON(t, http.MethodPost, url, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first create: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPost, url, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 on duplicate secret, got %d %s", resp.StatusCode, body)
	}
}

func TestKMSListAndGetSurface(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project + "/locations/us-central1")

	if resp, body := doJSON(t, http.MethodPost, base+"/keyRings?keyRingId=listring", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("create keyring: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, base+"/keyRings/listring/cryptoKeys?cryptoKeyId=listkey", map[string]any{"purpose": "ENCRYPT_DECRYPT"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create key: %d %s", resp.StatusCode, body)
	}

	resp, body := doJSON(t, http.MethodGet, base+"/keyRings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list keyrings: %d %s", resp.StatusCode, body)
	}
	var rings struct {
		KeyRings []struct {
			Name string `json:"name"`
		} `json:"keyRings"`
	}
	if err := json.Unmarshal(body, &rings); err != nil {
		t.Fatalf("decode rings: %v", err)
	}
	found := false
	for _, r := range rings.KeyRings {
		if r.Name == "projects/"+project+"/locations/us-central1/keyRings/listring" {
			found = true
		}
	}
	if !found {
		t.Errorf("keyring not in list: %+v", rings)
	}

	resp, body = doJSON(t, http.MethodGet, base+"/keyRings/listring", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("get keyring: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, base+"/keyRings/listring/cryptoKeys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list keys: %d %s", resp.StatusCode, body)
	}
	var keys struct {
		CryptoKeys []struct {
			Name    string `json:"name"`
			Purpose string `json:"purpose"`
		} `json:"cryptoKeys"`
	}
	_ = json.Unmarshal(body, &keys)
	if len(keys.CryptoKeys) != 1 || keys.CryptoKeys[0].Purpose != "ENCRYPT_DECRYPT" {
		t.Errorf("unexpected keys list: %+v", keys)
	}

	resp, body = doJSON(t, http.MethodGet, base+"/keyRings/listring/cryptoKeys/listkey", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("get key: %d %s", resp.StatusCode, body)
	}
}

func TestKMSDecryptWithWrongKeyFails(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project + "/locations/us-central1")

	if resp, _ := doJSON(t, http.MethodPost, base+"/keyRings?keyRingId=r2", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("ring")
	}
	for _, k := range []string{"a", "b"} {
		if resp, _ := doJSON(t, http.MethodPost, base+"/keyRings/r2/cryptoKeys?cryptoKeyId="+k, map[string]any{}); resp.StatusCode != http.StatusOK {
			t.Fatalf("create %s", k)
		}
	}

	plain := base64.StdEncoding.EncodeToString([]byte("secret message"))
	resp, body := doJSON(t, http.MethodPost, base+"/keyRings/r2/cryptoKeys/a:encrypt", map[string]any{"plaintext": plain})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", resp.StatusCode, body)
	}
	var er struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.Unmarshal(body, &er)

	resp, body = doJSON(t, http.MethodPost, base+"/keyRings/r2/cryptoKeys/b:decrypt", map[string]any{"ciphertext": er.Ciphertext})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected non-200 on wrong-key decrypt, got 200 %s", body)
	}
}

func TestBigQueryDatasetDeleteCascadesTables(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/bigquery/v2/projects/" + project)

	if resp, body := doJSON(t, http.MethodPost, base+"/datasets", map[string]any{
		"datasetReference": map[string]any{"datasetId": "cascade_ds"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create dataset: %d %s", resp.StatusCode, body)
	}

	if resp, body := doJSON(t, http.MethodPost, base+"/datasets/cascade_ds/tables", map[string]any{
		"tableReference": map[string]any{"tableId": "t1"},
		"schema": map[string]any{
			"fields": []map[string]any{{"name": "k", "type": "STRING"}},
		},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create table: %d %s", resp.StatusCode, body)
	}

	resp, _ := doJSON(t, http.MethodDelete, base+"/datasets/cascade_ds", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 on delete, got %d", resp.StatusCode)
	}

	resp, _ = doJSON(t, http.MethodGet, base+"/datasets/cascade_ds/tables/t1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("table not gone after dataset delete: got %d", resp.StatusCode)
	}
}

func TestPersistencePubSubAndKMSAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "state.db")
	cfgPath := filepath.Join(tmp, "gcp-local.yaml")
	cfgBody := "project: local-project\nstate: boltdb\nstate_dir: " + dbPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}

	em1 := startEmulator(t, cfgPath)

	if resp, body := doJSON(t, http.MethodPut, em1.URL("/v1/projects/"+project+"/topics/persist-topic"), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("topic: %d %s", resp.StatusCode, body)
	}

	kmsBase := em1.URL("/v1/projects/" + project + "/locations/us-central1")
	if resp, body := doJSON(t, http.MethodPost, kmsBase+"/keyRings?keyRingId=persistring", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("ring: %d %s", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, http.MethodPost, kmsBase+"/keyRings/persistring/cryptoKeys?cryptoKeyId=persistkey", map[string]any{}); resp.StatusCode != http.StatusOK {
		t.Fatalf("key: %d %s", resp.StatusCode, body)
	}
	resp, body := doJSON(t, http.MethodPost, kmsBase+"/keyRings/persistring/cryptoKeys/persistkey:encrypt", map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString([]byte("survive restart")),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", resp.StatusCode, body)
	}
	var enc struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.Unmarshal(body, &enc)

	stopEmulator(em1)
	em2 := startEmulator(t, cfgPath)

	resp, body = doJSON(t, http.MethodGet, em2.URL("/v1/projects/"+project+"/topics/persist-topic"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("topic missing after restart: %d %s", resp.StatusCode, body)
	}

	kmsBase2 := em2.URL("/v1/projects/" + project + "/locations/us-central1")
	resp, body = doJSON(t, http.MethodGet, kmsBase2+"/keyRings/persistring", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("keyring missing after restart: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, kmsBase2+"/keyRings/persistring/cryptoKeys/persistkey:decrypt", map[string]any{
		"ciphertext": enc.Ciphertext,
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("decrypt after restart failed: %d %s", resp.StatusCode, body)
	}
	var dec struct {
		Plaintext string `json:"plaintext"`
	}
	_ = json.Unmarshal(body, &dec)
	out, _ := base64.StdEncoding.DecodeString(dec.Plaintext)
	if string(out) != "survive restart" {
		t.Errorf("plaintext mismatch after restart: %q", string(out))
	}
}
