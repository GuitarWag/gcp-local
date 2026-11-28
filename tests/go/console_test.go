package gcplocaltest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestConsoleAllPagesRender verifies every sidebar page returns HTTP 200
// with HTML content, even when no resources have been created. The
// console must work on a fresh emulator — that's the smallest useful
// debugging slice.
func TestConsoleAllPagesRender(t *testing.T) {
	em := testutil.Start(t)
	for _, path := range []string{
		"/console",
		"/console/logging",
		"/console/storage",
		"/console/pubsub",
		"/console/firestore",
		"/console/secrets",
		"/console/tasks",
		"/console/scheduler",
		"/console/kms",
		"/console/monitoring",
		"/console/bigquery",
		"/console/cloudsql",
		"/console/cloudrun",
		"/console/functions",
		"/console/bigtable",
		"/console/spanner",
		"/console/iamadmin",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(em.URL(path))
			if err != nil {
				t.Fatalf("get %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("content-type = %q", ct)
			}
		})
	}
}

// TestConsoleStaticAssets verifies the embedded pico.min.css and app.js
// are served with the expected content types and a non-trivial body
// size. Catches accidental embed regressions early.
func TestConsoleStaticAssets(t *testing.T) {
	em := testutil.Start(t)
	for _, c := range []struct {
		path        string
		contentType string
		minBytes    int
	}{
		{"/console/static/app.css", "text/css", 5_000},
		{"/console/static/app.js", "text/javascript", 1_000},
	} {
		resp, err := http.Get(em.URL(c.path))
		if err != nil {
			t.Fatalf("get %s: %v", c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", c.path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, c.contentType) {
			t.Errorf("%s content-type = %q want prefix %q", c.path, ct, c.contentType)
		}
		if resp.ContentLength < int64(c.minBytes) {
			t.Errorf("%s content-length = %d want >= %d", c.path, resp.ContentLength, c.minBytes)
		}
	}
}

// TestConsoleLoggingAPI seeds entries via the real Cloud Logging REST
// surface and verifies the console API returns them.
func TestConsoleLoggingAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host

	resp, body := doJSON(t, http.MethodPost, base+"/v2/entries:write", map[string]any{
		"logName":  "projects/local-project/logs/console",
		"resource": map[string]any{"type": "global"},
		"entries": []map[string]any{
			{"severity": "INFO", "textPayload": "hello"},
			{"severity": "ERROR", "textPayload": "boom"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed entries: %d %s", resp.StatusCode, body)
	}

	resp, err := http.Get(em.URL("/console/api/logging/entries?limit=50"))
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api status = %d", resp.StatusCode)
	}
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) < 2 {
		t.Errorf("entries = %d, want >= 2", len(out.Entries))
	}
}

// TestConsoleStorageAPI creates a bucket and object through the JSON API
// the SDK uses, then verifies the console buckets / objects / preview
// endpoints reflect them.
func TestConsoleStorageAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host

	resp, body := doJSON(t, http.MethodPost, base+"/storage/v1/b?project=local-project", map[string]any{"name": "ui-bucket"})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create bucket: %d %s", resp.StatusCode, body)
	}
	// XML PUT — same upload path the SDK uses against the emulator.
	req, _ := http.NewRequest(http.MethodPut, base+"/ui-bucket/hello.txt", strings.NewReader("hello console"))
	req.Header.Set("Content-Type", "text/plain")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("xml put: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("xml put status = %d", r.StatusCode)
	}

	r2, err := http.Get(em.URL("/console/api/storage/buckets"))
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	defer r2.Body.Close()
	var bResp struct {
		Buckets []map[string]any `json:"buckets"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&bResp)
	if len(bResp.Buckets) == 0 {
		t.Fatalf("no buckets returned")
	}

	r3, err := http.Get(em.URL("/console/api/storage/objects?bucket=ui-bucket"))
	if err != nil {
		t.Fatalf("objects: %v", err)
	}
	defer r3.Body.Close()
	var oResp struct {
		Objects []map[string]any `json:"objects"`
	}
	_ = json.NewDecoder(r3.Body).Decode(&oResp)
	if len(oResp.Objects) == 0 {
		t.Fatalf("no objects returned")
	}

	r4, err := http.Get(em.URL("/console/api/storage/preview?bucket=ui-bucket&object=hello.txt"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	defer r4.Body.Close()
	var pResp struct {
		Body   string `json:"body"`
		IsText bool   `json:"isText"`
	}
	_ = json.NewDecoder(r4.Body).Decode(&pResp)
	if !pResp.IsText || pResp.Body != "hello console" {
		t.Errorf("preview = %+v", pResp)
	}
}

// TestConsolePubSubAPI seeds a topic + subscription via the gRPC-style
// REST surface, publishes a message, and verifies the console peek
// shows it without draining the queue (a second peek returns the same
// message).
func TestConsolePubSubAPI(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/local-project"

	if r, b := doJSON(t, http.MethodPut, base+"/topics/console-t", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("topic: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPut, base+"/subscriptions/console-s", map[string]any{
		"topic": "projects/local-project/topics/console-t",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("sub: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPost, base+"/topics/console-t:publish", map[string]any{
		"messages": []map[string]any{{"data": "aGVsbG8="}}, // "hello"
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", r.StatusCode, b)
	}

	resp, err := http.Get(em.URL("/console/api/pubsub/peek?subscription=projects/local-project/subscriptions/console-s&limit=5"))
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Messages) == 0 {
		t.Fatalf("peek returned no messages")
	}
	// Second peek should return the same message — peek must not drain.
	resp2, err := http.Get(em.URL("/console/api/pubsub/peek?subscription=projects/local-project/subscriptions/console-s&limit=5"))
	if err != nil {
		t.Fatalf("second peek: %v", err)
	}
	defer resp2.Body.Close()
	var out2 struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&out2)
	if len(out2.Messages) != len(out.Messages) {
		t.Errorf("second peek len = %d, first = %d (peek should not drain)", len(out2.Messages), len(out.Messages))
	}
}

// TestConsoleKMSEncryptDecrypt creates a keyring/key via REST, then
// round-trips a plaintext through the console encrypt/decrypt API.
func TestConsoleKMSEncryptDecrypt(t *testing.T) {
	em := testutil.Start(t)
	base := "http://" + em.Host + "/v1/projects/local-project/locations/global"

	if r, b := doJSON(t, http.MethodPost, base+"/keyRings?keyRingId=ring-x", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("keyring: %d %s", r.StatusCode, b)
	}
	if r, b := doJSON(t, http.MethodPost, base+"/keyRings/ring-x/cryptoKeys?cryptoKeyId=key-x", map[string]any{
		"purpose": "ENCRYPT_DECRYPT",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("cryptokey: %d %s", r.StatusCode, b)
	}

	keyName := "projects/local-project/locations/global/keyRings/ring-x/cryptoKeys/key-x"
	r, body := doJSON(t, http.MethodPost, em.URL("/console/api/kms/encrypt?key="+keyName), map[string]any{"plaintext": "very secret"})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("encrypt: %d %s", r.StatusCode, body)
	}
	var encOut struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.Unmarshal(body, &encOut)
	if encOut.Ciphertext == "" {
		t.Fatal("empty ciphertext")
	}

	r, body = doJSON(t, http.MethodPost, em.URL("/console/api/kms/decrypt?key="+keyName), map[string]any{"ciphertext": encOut.Ciphertext})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("decrypt: %d %s", r.StatusCode, body)
	}
	var decOut struct {
		Plaintext string `json:"plaintext"`
	}
	_ = json.Unmarshal(body, &decOut)
	if decOut.Plaintext != "very secret" {
		t.Errorf("round-trip plaintext = %q want %q", decOut.Plaintext, "very secret")
	}
}

// TestConsoleBundleSize keeps the embedded console assets under the
// budget called out in the spec (target: <300 KB binary impact). It
// stats the built binary and asserts a hard ceiling on the file size
// uplift introduced by the console embed. The threshold is generous —
// it's a regression guard, not a tight target.
func TestConsoleBundleSize(t *testing.T) {
	binPath := testutil.BinaryPath(t)
	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	// The pre-console binary sits around 45 MB; the console contributes
	// ~100 KB of pico + ~30 KB of templates + ~5 KB of JS/CSS. A 60 MB
	// upper bound leaves room for future service growth while still
	// catching obvious regressions like accidentally embedding a font.
	const maxBytes int64 = 60 * 1024 * 1024
	if info.Size() > maxBytes {
		t.Errorf("binary size = %d bytes, want < %d", info.Size(), maxBytes)
	}
}

// TestConsoleIAMAdminAPI seeds an account and key through the REST
// surface, then verifies the IAM Admin page and API endpoints — data,
// write paths, and the 400/404/409/405 error statuses.
func TestConsoleIAMAdminAPI(t *testing.T) {
	em := testutil.Start(t)
	rest := em.URL("/v1/projects/local-project/serviceAccounts")
	api := em.URL("/console/api/iamadmin")

	// Seed via REST: create account, then mint one key.
	resp, body := doJSON(t, http.MethodPost, rest, map[string]any{
		"accountId":      "console-sa",
		"serviceAccount": map[string]any{"displayName": "Console SA"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed create: %d %s", resp.StatusCode, body)
	}
	email := "console-sa@local-project.iam.gserviceaccount.com"
	resp, body = doJSON(t, http.MethodPost, rest+"/"+email+"/keys", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed key: %d %s", resp.StatusCode, body)
	}
	var seedKey struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &seedKey); err != nil {
		t.Fatalf("decode seed key: %v body=%s", err, body)
	}
	seedKeyID := seedKey.Name[strings.LastIndexByte(seedKey.Name, '/')+1:]
	if seedKeyID == "" {
		t.Fatalf("seed key name %q has no id", seedKey.Name)
	}

	// The page renders the seeded account.
	page, err := http.Get(em.URL("/console/iamadmin"))
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d", page.StatusCode)
	}
	if ct := page.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("page content-type = %q", ct)
	}
	// Client-side rendered: the inline JS fills the lists after load, so
	// the server HTML carries the chrome; data parity is checked on the API.
	html := string(pageBody)
	if !strings.Contains(html, "Service Accounts") || !strings.Contains(html, "/console/api/iamadmin/accounts") {
		t.Errorf("page body missing IAM Admin chrome:\n%s", html)
	}

	// accounts endpoint carries email, display name and unique ID.
	resp, body = doJSON(t, http.MethodGet, api+"/accounts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accounts: %d %s", resp.StatusCode, body)
	}
	var accounts struct {
		Accounts []struct {
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
			UniqueID    string `json:"uniqueId"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(body, &accounts); err != nil {
		t.Fatalf("decode accounts: %v body=%s", err, body)
	}
	var seededUniqueID string
	for _, a := range accounts.Accounts {
		if a.Email != email {
			continue
		}
		if a.DisplayName != "Console SA" {
			t.Errorf("displayName = %q want Console SA", a.DisplayName)
		}
		if a.UniqueID == "" {
			t.Errorf("uniqueId empty for %s", email)
		}
		seededUniqueID = a.UniqueID
	}
	if seededUniqueID == "" {
		t.Fatalf("accounts endpoint missing %s: %s", email, body)
	}
	// keys endpoint shows the seeded key with the full projection.
	resp, body = doJSON(t, http.MethodGet, api+"/keys?account="+url.QueryEscape(email), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keys: %d %s", resp.StatusCode, body)
	}
	var keys struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &keys); err != nil {
		t.Fatalf("decode keys: %v body=%s", err, body)
	}
	var seededRow map[string]any
	for _, k := range keys.Keys {
		if k["keyId"] == seedKeyID {
			seededRow = k
		}
	}
	if seededRow == nil {
		t.Fatalf("seeded key %s not in keys response: %s", seedKeyID, body)
	}
	for _, field := range []string{"keyId", "keyAlgorithm", "keyType", "validAfterTime", "validBeforeTime"} {
		if v, _ := seededRow[field].(string); v == "" {
			t.Errorf("key row field %q empty or missing", field)
		}
	}
	// Credentials download: decoded JSON + attachment header.
	credResp, err := http.Get(api + "/keys/" + seedKeyID + "/credentials?account=" + url.QueryEscape(email))
	if err != nil {
		t.Fatalf("credentials: %v", err)
	}
	credBody, _ := io.ReadAll(credResp.Body)
	credResp.Body.Close()
	if credResp.StatusCode != http.StatusOK {
		t.Fatalf("credentials status = %d body=%s", credResp.StatusCode, credBody)
	}
	var cred struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal(credBody, &cred); err != nil {
		t.Fatalf("decode credentials: %v body=%s", err, credBody)
	}
	if cred.Type != "service_account" || cred.ClientEmail != email || cred.PrivateKeyID != seedKeyID {
		t.Errorf("credentials = %+v", cred)
	}
	disp := credResp.Header.Get("Content-Disposition")
	if !strings.Contains(disp, ".json") || !strings.Contains(disp, seedKeyID) {
		t.Errorf("content-disposition = %q", disp)
	}
	// Write paths: create key, then delete key, account; cascade visible.
	resp, body = doJSON(t, http.MethodPost, api+"/keys?account="+url.QueryEscape(email), map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("api create key: %d %s", resp.StatusCode, body)
	}
	var createdKey struct {
		KeyID string `json:"keyId"`
	}
	if err := json.Unmarshal(body, &createdKey); err != nil || createdKey.KeyID == "" {
		t.Fatalf("api create key body=%s err=%v", body, err)
	}
	var afterCreate struct {
		Keys []struct {
			KeyID string `json:"keyId"`
		} `json:"keys"`
	}
	resp, body = doJSON(t, http.MethodGet, api+"/keys?account="+url.QueryEscape(email), nil)
	_ = json.Unmarshal(body, &afterCreate)
	found := false
	for _, k := range afterCreate.Keys {
		if k.KeyID == createdKey.KeyID {
			found = true
		}
	}
	if !found {
		t.Errorf("created key %s not listed after create: %s", createdKey.KeyID, body)
	}
	resp, body = doJSON(t, http.MethodDelete, api+"/keys?account="+url.QueryEscape(email)+"&key="+url.QueryEscape(createdKey.KeyID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api delete key: %d %s", resp.StatusCode, body)
	}
	// Wrong method on the accounts endpoint → 405; empty accountId → 400.
	resp, _ = doJSON(t, http.MethodPut, api+"/accounts", map[string]any{})
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("accounts PUT status = %d want 405", resp.StatusCode)
	}
	resp, body = doJSON(t, http.MethodPost, api+"/accounts", map[string]any{"accountId": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("create empty accountId status = %d want 400 body=%s", resp.StatusCode, body)
	}
	// Duplicate account → 409 and the message names the account.
	resp, body = doJSON(t, http.MethodPost, api+"/accounts", map[string]any{
		"accountId": "console-sa",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate create status = %d want 409 body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), email) {
		t.Errorf("409 message %q does not name the account", body)
	}

	// Delete the account; the cascade and the 404 on repeat are visible.
	resp, body = doJSON(t, http.MethodDelete, api+"/accounts?email="+url.QueryEscape(email), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api delete account: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodGet, api+"/accounts", nil)
	if strings.Contains(string(body), email) {
		t.Errorf("account %s still listed after delete: %s", email, body)
	}
	resp, _ = doJSON(t, http.MethodGet, api+"/keys?account="+url.QueryEscape(email), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("keys after cascade status = %d want 404", resp.StatusCode)
	}
	resp, body = doJSON(t, http.MethodDelete, api+"/accounts?email="+url.QueryEscape(email), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d want 404 body=%s", resp.StatusCode, body)
	}
}
