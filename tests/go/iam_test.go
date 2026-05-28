package gcplocaltest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/iam"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// policy is the JSON shape every IAM-aware GCP service round-trips
// through getIamPolicy / setIamPolicy. We deal in raw JSON here so the
// same helpers work for every service without pulling in a per-SDK
// IAM handle.
type iamPolicy struct {
	Version  int          `json:"version"`
	Etag     string       `json:"etag"`
	Bindings []iamBinding `json:"bindings"`
}

type iamBinding struct {
	Role    string   `json:"role"`
	Members []string `json:"members"`
}

func iamRoundTripViaColonVerbs(t *testing.T, em *testutil.Emulator, basePath string) {
	t.Helper()
	getURL := em.URL(basePath + ":getIamPolicy")
	setURL := em.URL(basePath + ":setIamPolicy")
	testURL := em.URL(basePath + ":testIamPermissions")

	got := httpIamGet(t, getURL)
	if len(got.Bindings) != 0 {
		t.Fatalf("%s: fresh resource should have no bindings, got %v", basePath, got.Bindings)
	}

	want := iamPolicy{
		Version: 1,
		Bindings: []iamBinding{
			{Role: "roles/owner", Members: []string{"user:alice@example.com"}},
			{Role: "roles/viewer", Members: []string{"user:bob@example.com", "serviceAccount:svc@p.iam.gserviceaccount.com"}},
		},
	}
	got2 := httpIamSet(t, setURL, want)
	if len(got2.Bindings) != 2 || got2.Etag == "" {
		t.Fatalf("%s: set returned %+v", basePath, got2)
	}

	got3 := httpIamGet(t, getURL)
	if len(got3.Bindings) != 2 || got3.Bindings[0].Role != "roles/owner" {
		t.Fatalf("%s: round-trip mismatch %+v", basePath, got3)
	}
	if got3.Etag != got2.Etag {
		t.Fatalf("%s: etag changed between set and get: %q vs %q", basePath, got2.Etag, got3.Etag)
	}

	perms := httpIamTest(t, testURL, []string{"storage.objects.get", "storage.objects.list"})
	if len(perms) != 2 {
		t.Fatalf("%s: testIamPermissions echoed %v", basePath, perms)
	}
}

func httpIamGet(t *testing.T, url string) iamPolicy {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("getIamPolicy %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getIamPolicy %s: status %d body %s", url, resp.StatusCode, body)
	}
	var p iamPolicy
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode policy: %v body=%s", err, body)
	}
	return p
}

func httpIamSet(t *testing.T, url string, p iamPolicy) iamPolicy {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"policy": p})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("setIamPolicy %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setIamPolicy %s: status %d body %s", url, resp.StatusCode, respBody)
	}
	var out iamPolicy
	if err := json.Unmarshal(respBody, &out); err != nil {
		t.Fatalf("decode policy: %v body=%s", err, respBody)
	}
	return out
}

func httpIamTest(t *testing.T, url string, perms []string) []string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"permissions": perms})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("testIamPermissions %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("testIamPermissions %s: status %d body %s", url, resp.StatusCode, respBody)
	}
	var out struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, respBody)
	}
	return out.Permissions
}

// TestIAMColonVerbsAcrossServices proves the central IAM verb interceptor
// covers every service whose IAM API uses the v1 mixin (POST :getIamPolicy
// / :setIamPolicy / :testIamPermissions on the resource path). Storage
// has its own GCS-style endpoints and is covered by TestIAMStorageBucketRoundTrip.
func TestIAMColonVerbsAcrossServices(t *testing.T) {
	em := testutil.Start(t)

	cases := []struct {
		name string
		path string
	}{
		{"pubsub-topic", "/v1/projects/local-project/topics/iam-topic"},
		{"pubsub-subscription", "/v1/projects/local-project/subscriptions/iam-sub"},
		{"secretmanager-secret", "/v1/projects/local-project/secrets/iam-secret"},
		{"kms-keyring", "/v1/projects/local-project/locations/global/keyRings/iam-ring"},
		{"kms-cryptokey", "/v1/projects/local-project/locations/global/keyRings/iam-ring/cryptoKeys/iam-key"},
		{"tasks-queue", "/v2/projects/local-project/locations/us/queues/iam-queue"},
		{"cloudrun-service", "/v2/projects/local-project/locations/us-central1/services/iam-svc"},
		{"function", "/v2/projects/local-project/locations/us-central1/functions/iam-fn"},
		{"iamadmin-sa", "/v1/projects/local-project/serviceAccounts/iam-sa@local-project.iam.gserviceaccount.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iamRoundTripViaColonVerbs(t, em, tc.path)
		})
	}
}

// TestIAMStorageBucketRoundTrip exercises the GCS-shaped IAM endpoints
// (GET/PUT /b/{bucket}/iam) through the real Go storage client. This is
// the headline acceptance test from issue #28.
func TestIAMStorageBucketRoundTrip(t *testing.T) {
	em := testutil.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Setenv("STORAGE_EMULATOR_HOST", "http://"+em.Host)
	client, err := storage.NewClient(ctx,
		option.WithoutAuthentication(),
		option.WithEndpoint("http://"+em.Host+"/storage/v1/"),
	)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}
	defer client.Close()

	const bucket = "iam-bucket"
	if err := client.Bucket(bucket).Create(ctx, "local-project", nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	h := client.Bucket(bucket).IAM()
	got, err := h.Policy(ctx)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if members := got.Members("roles/storage.admin"); len(members) != 0 {
		t.Fatalf("fresh bucket has admins: %v", members)
	}

	got.Add("user:alice@example.com", iam.RoleName("roles/storage.admin"))
	got.Add("serviceAccount:svc@local-project.iam.gserviceaccount.com", iam.RoleName("roles/storage.objectViewer"))
	if err := h.SetPolicy(ctx, got); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	got2, err := h.Policy(ctx)
	if err != nil {
		t.Fatalf("policy2: %v", err)
	}
	admins := got2.Members("roles/storage.admin")
	if len(admins) != 1 || admins[0] != "user:alice@example.com" {
		t.Fatalf("admins after set: %v", admins)
	}
	viewers := got2.Members("roles/storage.objectViewer")
	if len(viewers) != 1 || viewers[0] != "serviceAccount:svc@local-project.iam.gserviceaccount.com" {
		t.Fatalf("viewers after set: %v", viewers)
	}

	perms, err := h.TestPermissions(ctx, []string{"storage.objects.get", "storage.objects.list"})
	if err != nil {
		t.Fatalf("test permissions: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("test permissions returned %v", perms)
	}
}

// TestIAMAdminServiceAccountsCRUD covers the iam.googleapis.com
// projects.serviceAccounts admin endpoints — create, get, list, key
// generation, delete with key cascade.
func TestIAMAdminServiceAccountsCRUD(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/local-project/serviceAccounts")

	body, _ := json.Marshal(map[string]any{
		"accountId": "my-sa",
		"serviceAccount": map[string]any{
			"displayName": "Test SA",
		},
	})
	resp, err := http.Post(base, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status %d body %s", resp.StatusCode, respBody)
	}
	var created struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		ProjectID string `json:"projectId"`
		UniqueID  string `json:"uniqueId"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("decode: %v body=%s", err, respBody)
	}
	wantEmail := "my-sa@local-project.iam.gserviceaccount.com"
	if created.Email != wantEmail {
		t.Fatalf("email = %q want %q", created.Email, wantEmail)
	}
	if created.UniqueID == "" {
		t.Fatalf("uniqueId empty")
	}

	getResp, err := http.Get(base + "/" + wantEmail)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d body %s", getResp.StatusCode, getBody)
	}

	listResp, err := http.Get(base)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	var list struct {
		Accounts []struct {
			Email string `json:"email"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(listBody, &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, listBody)
	}
	found := false
	for _, sa := range list.Accounts {
		if sa.Email == wantEmail {
			found = true
		}
	}
	if !found {
		t.Fatalf("created SA not in list: %s", listBody)
	}

	keyResp, err := http.Post(base+"/"+wantEmail+"/keys", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("keys.create: %v", err)
	}
	keyBody, _ := io.ReadAll(keyResp.Body)
	keyResp.Body.Close()
	if keyResp.StatusCode != http.StatusOK {
		t.Fatalf("keys.create status %d body %s", keyResp.StatusCode, keyBody)
	}
	var key struct {
		Name           string `json:"name"`
		PrivateKeyData string `json:"privateKeyData"`
	}
	if err := json.Unmarshal(keyBody, &key); err != nil {
		t.Fatalf("decode key: %v body=%s", err, keyBody)
	}
	if key.PrivateKeyData == "" {
		t.Fatalf("privateKeyData empty: %s", keyBody)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, base+"/"+wantEmail, nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", delResp.StatusCode)
	}

	g, _ := http.Get(base + "/" + wantEmail)
	if g.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status %d", g.StatusCode)
	}
	g.Body.Close()
}

// TestIAMResetClearsPolicies asserts that POST /admin/reset wipes the
// IAM policy namespace alongside the per-service ones. Without this,
// resets would silently leak stale bindings across test resources.
func TestIAMResetClearsPolicies(t *testing.T) {
	em := testutil.Start(t)
	base := "/v1/projects/local-project/topics/reset-iam-topic"

	want := iamPolicy{
		Version:  1,
		Bindings: []iamBinding{{Role: "roles/pubsub.publisher", Members: []string{"user:alice@example.com"}}},
	}
	httpIamSet(t, em.URL(base+":setIamPolicy"), want)

	resp, err := http.Post(em.URL("/admin/reset"), "application/json", nil)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	resp.Body.Close()

	got := httpIamGet(t, em.URL(base+":getIamPolicy"))
	if len(got.Bindings) != 0 {
		t.Fatalf("policy survived reset: %+v", got)
	}
}
