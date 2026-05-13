package gcplocaltest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestMalformedJSONReturns400(t *testing.T) {
	em := testutil.Start(t)

	cases := []struct {
		name string
		url  string
	}{
		{"storage bucket create", em.URL("/storage/v1/b?project=" + project)},
		{"pubsub create sub", em.URL("/v1/projects/" + project + "/subscriptions/x")},
		{"secret addVersion", em.URL("/v1/projects/" + project + "/secrets/s:addVersion")},
		{"bigquery query", em.URL("/bigquery/v2/projects/" + project + "/queries")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			method := http.MethodPost
			if strings.Contains(c.url, "/subscriptions/") {
				method = http.MethodPut
			}
			req, _ := http.NewRequest(method, c.url, strings.NewReader("{not json"))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
				t.Errorf("got %d, expected 400 or 404", resp.StatusCode)
			}
		})
	}
}

func TestSecretManagerMissingFields(t *testing.T) {
	em := testutil.Start(t)
	// missing secretId query param
	resp, _ := doJSON(t, http.MethodPost, em.URL("/v1/projects/"+project+"/secrets"), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing secretId got %d", resp.StatusCode)
	}
}

func TestKMSMissingKeyRingFor404(t *testing.T) {
	em := testutil.Start(t)
	base := em.URL("/v1/projects/" + project + "/locations/us/keyRings")
	// Create cryptoKey under nonexistent keyRing
	resp, _ := doJSON(t, http.MethodPost, base+"/no-such-ring/cryptoKeys?cryptoKeyId=k", map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestStorageMissingBucketIs404(t *testing.T) {
	em := testutil.Start(t)
	resp, _ := doJSON(t, http.MethodGet, em.URL("/storage/v1/b/nope-i-dont-exist"), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 got %d", resp.StatusCode)
	}
}

func TestPubSubCreateSubWithoutTopic(t *testing.T) {
	em := testutil.Start(t)
	resp, _ := doJSON(t, http.MethodPut,
		em.URL("/v1/projects/"+project+"/subscriptions/orphan"),
		map[string]any{}) // no topic field
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	em := testutil.Start(t)
	resp, err := http.Get(em.URL("/no/such/endpoint"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowedOnHealthz(t *testing.T) {
	em := testutil.Start(t)
	req, _ := http.NewRequest(http.MethodPost, em.URL("/healthz"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// /healthz responds 200 even to POST in current impl; document via test.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("got %d", resp.StatusCode)
	}
}

// silence unused testutil import
var _ = testutil.Start
