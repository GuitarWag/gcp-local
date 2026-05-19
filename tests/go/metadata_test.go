package gcplocaltest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cmetadata "cloud.google.com/go/compute/metadata"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

const flavor = "Metadata-Flavor"

func TestMetadataServerProjectID(t *testing.T) {
	em := testutil.Start(t)
	body := metaGet(t, em, "/computeMetadata/v1/project/project-id")
	if body != "local-project" {
		t.Fatalf("project-id = %q, want local-project", body)
	}
}

func TestMetadataServerToken(t *testing.T) {
	em := testutil.Start(t)
	body := metaGet(t, em, "/computeMetadata/v1/instance/service-accounts/default/token")
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal([]byte(body), &tok); err != nil {
		t.Fatalf("decode token: %v body=%s", err, body)
	}
	if tok.AccessToken == "" || tok.TokenType != "Bearer" || tok.ExpiresIn <= 0 {
		t.Fatalf("bad token response: %+v", tok)
	}
}

func TestMetadataServerIdentity(t *testing.T) {
	em := testutil.Start(t)
	jwt := metaGet(t, em, "/computeMetadata/v1/instance/service-accounts/default/identity?audience=https://my.app")
	claims := decodeJWTClaims(t, jwt)
	if claims["aud"] != "https://my.app" {
		t.Fatalf("aud = %v, want https://my.app", claims["aud"])
	}
	if claims["iss"] != "gcp-local" {
		t.Fatalf("iss = %v, want gcp-local", claims["iss"])
	}
}

func TestMetadataServerMissingFlavorHeader(t *testing.T) {
	em := testutil.Start(t)
	req, _ := http.NewRequest(http.MethodGet, em.URL("/computeMetadata/v1/project/project-id"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing-flavor status = %d, want 403", resp.StatusCode)
	}
}

func TestMetadataServerEmailAndScopes(t *testing.T) {
	em := testutil.Start(t)
	email := metaGet(t, em, "/computeMetadata/v1/instance/service-accounts/default/email")
	if !strings.Contains(email, "@local-project.iam.gserviceaccount.com") {
		t.Fatalf("email = %q", email)
	}
	scopes := metaGet(t, em, "/computeMetadata/v1/instance/service-accounts/default/scopes")
	if !strings.Contains(scopes, "cloud-platform") {
		t.Fatalf("scopes = %q", scopes)
	}
}

func TestMetadataServerGoClient(t *testing.T) {
	em := testutil.Start(t)
	t.Setenv("GCE_METADATA_HOST", em.Host)
	client := cmetadata.NewClient(nil)
	got, err := client.ProjectIDWithContext(context.Background())
	if err != nil {
		t.Fatalf("ProjectID: %v", err)
	}
	if got != "local-project" {
		t.Fatalf("ProjectID = %q, want local-project", got)
	}
}

func TestIAMCredentialsGenerateIdToken(t *testing.T) {
	em := testutil.Start(t)
	url := em.URL("/v1/projects/-/serviceAccounts/sa@example.iam.gserviceaccount.com:generateIdToken")
	resp, body := doJSON(t, http.MethodPost, url, map[string]any{
		"audience":     "https://target/",
		"includeEmail": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	claims := decodeJWTClaims(t, out.Token)
	if claims["aud"] != "https://target/" {
		t.Fatalf("aud = %v", claims["aud"])
	}
	if claims["email"] != "sa@example.iam.gserviceaccount.com" {
		t.Fatalf("email = %v", claims["email"])
	}
}

func TestIAMCredentialsGenerateAccessToken(t *testing.T) {
	em := testutil.Start(t)
	url := em.URL("/v1/projects/-/serviceAccounts/sa@example.iam.gserviceaccount.com:generateAccessToken")
	resp, body := doJSON(t, http.MethodPost, url, map[string]any{
		"scope":    []string{"https://www.googleapis.com/auth/cloud-platform"},
		"lifetime": "3600s",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  string `json:"expireTime"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" || out.ExpireTime == "" {
		t.Fatalf("bad resp: %+v", out)
	}
}

func metaGet(t *testing.T, em *testutil.Emulator, path string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, em.URL(path), nil)
	req.Header.Set(flavor, "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d", path, resp.StatusCode)
	}
	if resp.Header.Get(flavor) != "Google" {
		t.Fatalf("%s missing Metadata-Flavor response header", path)
	}
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return strings.TrimRight(string(buf), "\n")
}

func decodeJWTClaims(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts, want 3 (%s)", len(parts), jwt)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode jwt payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}
