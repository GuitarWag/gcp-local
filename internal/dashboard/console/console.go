// Package console serves the gcp-local web console at /console.
//
// The console is a server-rendered, polling-based UI for inspecting the
// emulator's state during local development. It is intentionally light:
// vanilla JS, Pico CSS, html/template, all assets embedded via go:embed.
package console

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/GuitarWag/gcp-local/internal/health"
	"github.com/GuitarWag/gcp-local/internal/state"
)

//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Options wires the console to its sibling services. Any service left nil
// is treated as disabled — the corresponding page is hidden from the
// sidebar and returns 404 if requested directly.
type Options struct {
	Version string
	Commit  string
	Project string
	Health  *health.Registry

	// Service handles. Each one is independently optional; the console
	// reads state through these rather than reaching into the store, so
	// page rendering stays consistent with whatever the service exposes
	// over its REST/gRPC surface.
	Logging     ConsoleLogging
	Storage     ConsoleStorage
	PubSub      ConsolePubSub
	Firestore   ConsoleFirestore
	Secrets     ConsoleSecrets
	Tasks       ConsoleTasks
	Scheduler   ConsoleScheduler
	KMS         ConsoleKMS
	Monitoring  ConsoleMonitoring
	BigQuery    ConsoleBigQuery
	CloudSQL    ConsoleCloudSQL
	CloudRun    ConsoleCloudRun
	Functions   ConsoleCloudRun
	Memorystore ConsoleMemorystore
	Bigtable    ConsoleStubService
	Spanner     ConsoleStubService
}

// Small per-page interfaces. Services implement just what the console
// needs, which keeps the console package decoupled from the full service
// API surface and lets tests stand in fakes.

type ConsoleLogging interface {
	ConsoleEntries(limit int, filter string) ([]map[string]any, error)
}

type ConsoleStorage interface {
	ConsoleBuckets() ([]map[string]any, error)
	ConsoleObjects(bucket string) ([]map[string]any, error)
	ConsoleObjectPreview(bucket, object string, maxBytes int) (string, bool, error) // body, isText, error
	ConsoleUpload(bucket, object, contentType string, data []byte) error
}

type ConsolePubSub interface {
	ConsoleTopics() ([]map[string]any, error)
	ConsoleSubscriptions(topic string) ([]map[string]any, error)
	ConsolePeekMessages(subscription string, limit int) ([]map[string]any, error)
	ConsolePublish(topic string, data []byte, attrs map[string]string) (string, error)
}

type ConsoleFirestore interface {
	ConsoleCollections() ([]string, error)
	ConsoleDocuments(collection string) ([]map[string]any, error)
	ConsoleDocument(path string) (map[string]any, error)
}

type ConsoleSecrets interface {
	ConsoleSecrets() ([]map[string]any, error)
	ConsoleVersions(secret string) ([]map[string]any, error)
	ConsoleVersionPayload(secret, version string) (string, error)
}

type ConsoleTasks interface {
	ConsoleQueues() ([]map[string]any, error)
	ConsoleTasks(queue string) ([]map[string]any, error)
}

type ConsoleScheduler interface {
	ConsoleJobs() ([]map[string]any, error)
}

type ConsoleKMS interface {
	ConsoleKeyRings() ([]map[string]any, error)
	ConsoleCryptoKeys(keyring string) ([]map[string]any, error)
	ConsoleEncrypt(key, plaintext string) (string, error)
	ConsoleDecrypt(key, ciphertext string) (string, error)
}

type ConsoleMonitoring interface {
	ConsoleSeries(limit int) ([]map[string]any, error)
}

type ConsoleBigQuery interface {
	ConsoleDatasets() ([]map[string]any, error)
	ConsoleTables(datasetID string) ([]map[string]any, error)
	ConsoleQuery(query string) (map[string]any, error)
}

type ConsoleCloudSQL interface {
	ConsoleInstances() ([]map[string]any, error)
}

type ConsoleCloudRun interface {
	ConsoleResources() ([]map[string]any, error)
	ConsoleKind() string
}

type ConsoleMemorystore interface {
	ConsoleStatus() map[string]any
}

// ConsoleStubService is the contract for emulated services that are
// stubs (Bigtable, Spanner) — they just expose enough to render a
// status page.
type ConsoleStubService interface {
	ConsoleStatus() map[string]any
}

// Service is the console HTTP handler.
type Service struct {
	store   state.Store
	opts    Options
	tmpls   *template.Template
	staticH http.Handler
	pages   []page // ordered sidebar entries
}

type page struct {
	ID         string
	HealthName string
	Label      string
	Href       string
	Icon       string // material-symbols ligature name
	Endpoint   string // shown on the overview row; not an exact URL, just the obvious surface
	Enabled    bool
}

func New(store state.Store, opts Options) (*Service, error) {
	t, err := template.New("").ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static fs: %w", err)
	}
	s := &Service{
		store:   store,
		opts:    opts,
		tmpls:   t,
		staticH: http.StripPrefix("/console/static/", http.FileServer(http.FS(sub))),
	}
	s.pages = []page{
		{ID: "logging", HealthName: "logging", Label: "Cloud Logging", Href: "/console/logging", Icon: "receipt_long", Endpoint: "/v2/entries:*", Enabled: opts.Logging != nil},
		{ID: "storage", HealthName: "storage", Label: "Cloud Storage", Href: "/console/storage", Icon: "folder", Endpoint: "/storage/v1/b", Enabled: opts.Storage != nil},
		{ID: "pubsub", HealthName: "pubsub", Label: "Pub/Sub", Href: "/console/pubsub", Icon: "swap_horiz", Endpoint: "/v1/projects/*/topics, gRPC", Enabled: opts.PubSub != nil},
		{ID: "firestore", HealthName: "firestore", Label: "Firestore", Href: "/console/firestore", Icon: "database", Endpoint: "gRPC (firestore.v1)", Enabled: opts.Firestore != nil},
		{ID: "secrets", HealthName: "secretmanager", Label: "Secret Manager", Href: "/console/secrets", Icon: "lock", Endpoint: "/v1/projects/*/secrets", Enabled: opts.Secrets != nil},
		{ID: "tasks", HealthName: "tasks", Label: "Cloud Tasks", Href: "/console/tasks", Icon: "task_alt", Endpoint: "/v2/projects/*/queues", Enabled: opts.Tasks != nil},
		{ID: "scheduler", HealthName: "scheduler", Label: "Cloud Scheduler", Href: "/console/scheduler", Icon: "schedule", Endpoint: "/v1/projects/*/jobs", Enabled: opts.Scheduler != nil},
		{ID: "kms", HealthName: "kms", Label: "Cloud KMS", Href: "/console/kms", Icon: "vpn_key", Endpoint: "/v1/projects/*/keyRings", Enabled: opts.KMS != nil},
		{ID: "monitoring", HealthName: "monitoring", Label: "Cloud Monitoring", Href: "/console/monitoring", Icon: "show_chart", Endpoint: "/v3/projects/*/timeSeries", Enabled: opts.Monitoring != nil},
		{ID: "bigquery", HealthName: "bigquery", Label: "BigQuery", Href: "/console/bigquery", Icon: "table_chart", Endpoint: "/bigquery/v2/projects/*", Enabled: opts.BigQuery != nil},
		{ID: "cloudsql", HealthName: "cloudsql", Label: "Cloud SQL", Href: "/console/cloudsql", Icon: "storage", Endpoint: "/sql/v1beta4 + pg wire", Enabled: opts.CloudSQL != nil},
		{ID: "cloudrun", HealthName: "cloudrun", Label: "Cloud Run", Href: "/console/cloudrun", Icon: "rocket_launch", Endpoint: "/v2/projects/*/services", Enabled: opts.CloudRun != nil},
		{ID: "functions", HealthName: "functions", Label: "Cloud Functions", Href: "/console/functions", Icon: "functions", Endpoint: "/v2/projects/*/functions", Enabled: opts.Functions != nil},
		{ID: "memorystore", HealthName: "memorystore", Label: "Memorystore", Href: "/console/memorystore", Icon: "memory", Endpoint: "Redis on its own port", Enabled: opts.Memorystore != nil},
		{ID: "bigtable", HealthName: "bigtable", Label: "Bigtable", Href: "/console/bigtable", Icon: "view_module", Endpoint: "gRPC (bigtable.v2, stub)", Enabled: opts.Bigtable != nil},
		{ID: "spanner", HealthName: "spanner", Label: "Spanner", Href: "/console/spanner", Icon: "hub", Endpoint: "gRPC (spanner.v1, stub)", Enabled: opts.Spanner != nil},
	}
	return s, nil
}

// Register mounts the console handlers onto mux. The root path /console
// serves the landing page; per-service pages are at /console/<service>,
// and JSON endpoints at /console/api/<service>/...
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/console", s.handleIndex)
	mux.HandleFunc("/console/", s.routePage)
	mux.Handle("/console/static/", s.staticH)
	mux.HandleFunc("/console/api/", s.routeAPI)
}

func (s *Service) handleIndex(w http.ResponseWriter, r *http.Request) {
	summary := make([]map[string]any, 0, len(s.pages))
	enabled := 0
	for _, p := range s.pages {
		row := map[string]any{
			"Label":    p.Label,
			"Href":     p.Href,
			"Endpoint": p.Endpoint,
			"Status":   "disabled",
		}
		if p.Enabled {
			enabled++
			row["Status"] = "ready"
			if s.opts.Health != nil {
				if st, ok := s.opts.Health.Lookup(p.HealthName); ok {
					row["Status"] = string(st)
				}
			}
		}
		summary = append(summary, row)
	}
	endpoint := r.Host
	if endpoint == "" {
		endpoint = "localhost:4443"
	}
	s.render(w, "index.html", "Overview", "", map[string]any{
		"Summary":      summary,
		"EnabledCount": enabled,
		"TotalCount":   len(s.pages),
		"Endpoint":     endpoint,
		"Project":      s.opts.Project,
	})
}

// routePage dispatches /console/<service> to the per-service handler.
func (s *Service) routePage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/")
	if rest == "" {
		s.handleIndex(w, r)
		return
	}
	// Strip any deeper path segments — pages are single-level; deeper
	// navigation happens via query strings (?bucket=, ?topic=, etc.).
	id := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		id = rest[:i]
	}
	handler, ok := s.pageHandler(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}

// routeAPI dispatches /console/api/<service>/... to per-service JSON
// handlers. The remaining segments are passed to the handler unchanged.
func (s *Service) routeAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/console/api/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	id := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		id = rest[:i]
	}
	handler, ok := s.apiHandler(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler(w, r)
}

// render executes the layout template with the given page content and
// data. activeID matches one of the pages on the sidebar; pageScript is
// inlined into a <script> tag at the foot of the layout.
func (s *Service) render(w http.ResponseWriter, contentTmpl, title, activeID string, data map[string]any) {
	nav := make([]map[string]any, 0, len(s.pages))
	nav = append(nav, map[string]any{
		"Label":  "Overview",
		"Href":   "/console",
		"Icon":   "dashboard",
		"Active": activeID == "",
	})
	for _, p := range s.pages {
		if !p.Enabled {
			continue
		}
		nav = append(nav, map[string]any{
			"Label":  p.Label,
			"Href":   p.Href,
			"Icon":   p.Icon,
			"Active": p.ID == activeID,
		})
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	data["Version"] = s.opts.Version
	data["Commit"] = s.opts.Commit
	data["Nav"] = nav
	if _, ok := data["PageScript"]; !ok {
		data["PageScript"] = template.JS("")
	}

	// We clone the parsed set and reparse just the requested page on top
	// so its `{{define "content"}}` wins. Cloning keeps requests
	// independent and avoids the all-templates-share-one-name pitfall.
	clone, err := s.tmpls.Clone()
	if err != nil {
		http.Error(w, "template clone: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := clone.ParseFS(tmplFS, "templates/"+contentTmpl); err != nil {
		http.Error(w, "template parse: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := clone.ExecuteTemplate(w, "layout", data); err != nil {
		// Headers already sent; log to stderr via the standard error path.
		fmt.Fprintf(w, "\n<!-- template error: %v -->\n", err)
	}
}
