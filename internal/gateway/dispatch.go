package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/GuitarWag/gcp-local/internal/iam"
	"github.com/GuitarWag/gcp-local/internal/services/iamadmin"
)

func (g *Gateway) dispatchV3(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path, "/v3/")
	if len(parts) < 3 || parts[0] != "projects" {
		http.NotFound(w, r)
		return
	}
	if g.mon != nil && g.mon.HandleV3(w, r, parts) {
		return
	}
	http.NotFound(w, r)
}

func (g *Gateway) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	for _, ns := range []string{
		"storage/buckets",
		"pubsub/topics",
		"pubsub/subscriptions",
		"secretmanager/secrets",
		"secretmanager/versions",
		"tasks/queues",
		"tasks/tasks",
		"scheduler/jobs",
		"kms/keyrings",
		"kms/cryptokeys",
		"bigtable/tables",
		"bigtable/rows",
		iam.Namespace,
		iamadmin.NamespaceAccounts,
		iamadmin.NamespaceKeys,
	} {
		all, err := g.store.List(ns, "")
		if err != nil {
			http.Error(w, fmt.Sprintf("list %s: %v", ns, err), http.StatusInternalServerError)
			return
		}
		for k := range all {
			if err := g.store.Delete(ns, k); err != nil {
				http.Error(w, fmt.Sprintf("delete %s/%s: %v", ns, k, err), http.StatusInternalServerError)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) dispatchV1(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path, "/v1/")
	if len(parts) < 3 || parts[0] != "projects" {
		http.NotFound(w, r)
		return
	}
	// IAM colon-verbs (:getIamPolicy / :setIamPolicy / :testIamPermissions)
	// are mounted on every resource type in real GCP. Centralising the
	// dispatch here keeps per-service code clean — they just expose
	// CRUD and let the gateway handle IAM round-tripping.
	if g.handleIAMVerb(w, r, parts) {
		return
	}
	handlers := []v1Handler{}
	if g.iamSA != nil {
		handlers = append(handlers, g.iamSA.HandleV1)
	}
	if g.pubsub != nil {
		handlers = append(handlers, g.pubsub.HandleV1)
	}
	if g.sm != nil {
		handlers = append(handlers, g.sm.HandleV1)
	}
	if g.sched != nil {
		handlers = append(handlers, g.sched.HandleV1)
	}
	if g.kms != nil {
		handlers = append(handlers, g.kms.HandleV1)
	}
	if g.iamc != nil {
		handlers = append(handlers, g.iamc.HandleV1)
	}
	for _, h := range handlers {
		if h(w, r, parts) {
			return
		}
	}
	http.NotFound(w, r)
}

func (g *Gateway) dispatchV2(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path, "/v2/")
	if len(parts) < 3 || parts[0] != "projects" {
		http.NotFound(w, r)
		return
	}
	if g.handleIAMVerb(w, r, parts) {
		return
	}
	if g.tasks != nil && g.tasks.HandleV2(w, r, parts) {
		return
	}
	if g.run != nil && g.run.HandleV2(w, r, parts) {
		return
	}
	if g.funcs != nil && g.funcs.HandleV2(w, r, parts) {
		return
	}
	http.NotFound(w, r)
}

// handleIAMVerb routes ":getIamPolicy", ":setIamPolicy",
// ":testIamPermissions" requests against any resource whose path is
// already shaped as projects/.../<type>/<name>. Returns true when an
// IAM verb was detected and handled.
func (g *Gateway) handleIAMVerb(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 4 {
		return false
	}
	last := parts[len(parts)-1]
	idx := strings.LastIndex(last, ":")
	if idx < 0 {
		return false
	}
	action := last[idx+1:]
	if !iam.IsVerb(action) {
		return false
	}
	name := last[:idx]
	clone := append(parts[:0:0], parts...)
	clone[len(clone)-1] = name
	resource := strings.Join(clone, "/")
	return g.iam.Handle(w, r, resource, action)
}

func splitPath(path, prefix string) []string {
	rest := strings.TrimPrefix(path, prefix)
	return strings.Split(rest, "/")
}
