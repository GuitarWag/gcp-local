package gateway

import (
	"fmt"
	"net/http"

	"github.com/GuitarWag/gcp-local/internal/iam"
	"github.com/GuitarWag/gcp-local/internal/services/iamadmin"
	"github.com/GuitarWag/gcp-local/internal/services/workflows"
)

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
		workflows.NamespaceWorkflows,
		workflows.NamespaceOperations,
		"kms/keyrings",
		"kms/cryptokeys",
		"bigtable/tables",
		"bigtable/rows",
		iam.Namespace,
		iamadmin.NamespaceAccounts,
		iamadmin.NamespaceKeys,
		"serviceusage/services",
		"serviceusage/operations",
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
