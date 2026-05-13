package gcplocaltest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestDashboardServesIndex(t *testing.T) {
	em := testutil.Start(t)
	resp, err := http.Get(em.URL("/dashboard"))
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestDashboardStateAPI(t *testing.T) {
	em := testutil.Start(t)
	resp, err := http.Get(em.URL("/dashboard/api/state"))
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
