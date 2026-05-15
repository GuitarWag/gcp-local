package console

import (
	"encoding/json"
	"net/http"
)

// pageHandler maps a sidebar id to its HTML page handler. Returns false
// if the service isn't enabled.
func (s *Service) pageHandler(id string) (http.HandlerFunc, bool) {
	switch id {
	case "logging":
		if s.opts.Logging == nil {
			return nil, false
		}
		return s.pageLogging, true
	case "storage":
		if s.opts.Storage == nil {
			return nil, false
		}
		return s.pageStorage, true
	case "pubsub":
		if s.opts.PubSub == nil {
			return nil, false
		}
		return s.pagePubSub, true
	case "firestore":
		if s.opts.Firestore == nil {
			return nil, false
		}
		return s.pageFirestore, true
	case "secrets":
		if s.opts.Secrets == nil {
			return nil, false
		}
		return s.pageSecrets, true
	case "tasks":
		if s.opts.Tasks == nil {
			return nil, false
		}
		return s.pageTasks, true
	case "scheduler":
		if s.opts.Scheduler == nil {
			return nil, false
		}
		return s.pageScheduler, true
	case "kms":
		if s.opts.KMS == nil {
			return nil, false
		}
		return s.pageKMS, true
	case "monitoring":
		if s.opts.Monitoring == nil {
			return nil, false
		}
		return s.pageMonitoring, true
	case "bigquery":
		if s.opts.BigQuery == nil {
			return nil, false
		}
		return s.pageBigQuery, true
	case "cloudsql":
		if s.opts.CloudSQL == nil {
			return nil, false
		}
		return s.pageCloudSQL, true
	case "cloudrun":
		if s.opts.CloudRun == nil {
			return nil, false
		}
		return s.pageCloudRun, true
	case "functions":
		if s.opts.Functions == nil {
			return nil, false
		}
		return s.pageFunctions, true
	case "memorystore":
		if s.opts.Memorystore == nil {
			return nil, false
		}
		return s.pageMemorystore, true
	case "bigtable":
		if s.opts.Bigtable == nil {
			return nil, false
		}
		return s.pageBigtable, true
	case "spanner":
		if s.opts.Spanner == nil {
			return nil, false
		}
		return s.pageSpanner, true
	}
	return nil, false
}

// apiHandler maps a sidebar id to its JSON endpoint handler. The full
// request URL is /console/api/<id>/<sub>; per-service handlers parse the
// sub-path themselves to keep this dispatch table small.
func (s *Service) apiHandler(id string) (http.HandlerFunc, bool) {
	switch id {
	case "logging":
		if s.opts.Logging == nil {
			return nil, false
		}
		return s.apiLogging, true
	case "storage":
		if s.opts.Storage == nil {
			return nil, false
		}
		return s.apiStorage, true
	case "pubsub":
		if s.opts.PubSub == nil {
			return nil, false
		}
		return s.apiPubSub, true
	case "firestore":
		if s.opts.Firestore == nil {
			return nil, false
		}
		return s.apiFirestore, true
	case "secrets":
		if s.opts.Secrets == nil {
			return nil, false
		}
		return s.apiSecrets, true
	case "tasks":
		if s.opts.Tasks == nil {
			return nil, false
		}
		return s.apiTasks, true
	case "scheduler":
		if s.opts.Scheduler == nil {
			return nil, false
		}
		return s.apiScheduler, true
	case "kms":
		if s.opts.KMS == nil {
			return nil, false
		}
		return s.apiKMS, true
	case "monitoring":
		if s.opts.Monitoring == nil {
			return nil, false
		}
		return s.apiMonitoring, true
	case "bigquery":
		if s.opts.BigQuery == nil {
			return nil, false
		}
		return s.apiBigQuery, true
	case "cloudsql":
		if s.opts.CloudSQL == nil {
			return nil, false
		}
		return s.apiCloudSQL, true
	case "cloudrun":
		if s.opts.CloudRun == nil {
			return nil, false
		}
		return s.apiCloudRun, true
	case "functions":
		if s.opts.Functions == nil {
			return nil, false
		}
		return s.apiFunctions, true
	case "memorystore":
		if s.opts.Memorystore == nil {
			return nil, false
		}
		return s.apiMemorystore, true
	case "bigtable":
		if s.opts.Bigtable == nil {
			return nil, false
		}
		return s.apiBigtable, true
	case "spanner":
		if s.opts.Spanner == nil {
			return nil, false
		}
		return s.apiSpanner, true
	}
	return nil, false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
