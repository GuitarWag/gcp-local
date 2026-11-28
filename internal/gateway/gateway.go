package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/dashboard"
	"github.com/GuitarWag/gcp-local/internal/health"
	"github.com/GuitarWag/gcp-local/internal/iam"
	"github.com/GuitarWag/gcp-local/internal/services/bigquery"
	"github.com/GuitarWag/gcp-local/internal/services/bigtable"
	"github.com/GuitarWag/gcp-local/internal/services/cloudrun"
	"github.com/GuitarWag/gcp-local/internal/services/cloudsql"
	"github.com/GuitarWag/gcp-local/internal/services/firestore"
	"github.com/GuitarWag/gcp-local/internal/services/iamadmin"
	"github.com/GuitarWag/gcp-local/internal/services/iamcredentials"
	"github.com/GuitarWag/gcp-local/internal/services/kms"
	"github.com/GuitarWag/gcp-local/internal/services/logging"
	"github.com/GuitarWag/gcp-local/internal/services/memorystore"
	"github.com/GuitarWag/gcp-local/internal/services/metadata"
	"github.com/GuitarWag/gcp-local/internal/services/monitoring"
	"github.com/GuitarWag/gcp-local/internal/services/pubsub"
	"github.com/GuitarWag/gcp-local/internal/services/scheduler"
	"github.com/GuitarWag/gcp-local/internal/services/secretmanager"
	"github.com/GuitarWag/gcp-local/internal/services/spanner"
	"github.com/GuitarWag/gcp-local/internal/services/storage"
	"github.com/GuitarWag/gcp-local/internal/services/tasks"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// BuildInfo carries build-time identifiers (version, commit) injected by
// the CLI via -ldflags. It's surfaced in the /console UI footer; an
// empty struct is safe.
type BuildInfo struct {
	Version string
	Commit  string
}

type Gateway struct {
	cfg     *config.Config
	store   state.Store
	mux     *http.ServeMux
	health  *health.Registry
	build   BuildInfo
	storage *storage.Service
	pubsub  *pubsub.Service
	sm      *secretmanager.Service
	tasks   *tasks.Service
	sched   *scheduler.Service
	kms     *kms.Service
	logging *logging.Service
	mon     *monitoring.Service
	fs      *firestore.Service
	bq      *bigquery.Service
	bt      *bigtable.Service
	sp      *spanner.Service
	mem     *memorystore.Service
	csql    *cloudsql.Service
	run     *cloudrun.Service
	funcs   *cloudrun.Service
	meta    *metadata.Service
	iamc    *iamcredentials.Service
	iam     *iam.Store
	iamSA   *iamadmin.Service
	grpc    *grpc.Server
}

type v1Handler func(http.ResponseWriter, *http.Request, []string) bool

func New(cfg *config.Config, build BuildInfo) (*Gateway, error) {
	store, err := state.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}

	g := &Gateway{
		cfg:    cfg,
		store:  store,
		mux:    http.NewServeMux(),
		health: health.New(),
		build:  build,
		iam:    iam.NewStore(store),
		grpc:   grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler())),
	}
	g.initIAMAdmin(cfg, store)

	g.mux.HandleFunc("/healthz", g.health.Handler())
	g.mux.HandleFunc("/admin/reset", g.handleReset)
	if cfg.Dashboard {
		dashboard.New(store).Register(g.mux)
	}
	g.mux.HandleFunc("/v1/projects/", g.dispatchV1)
	g.mux.HandleFunc("/v2/projects/", g.dispatchV2)
	g.mux.HandleFunc("/v3/projects/", g.dispatchV3)

	// Helpers run in construction order, so the health registry
	// order is unchanged.
	for _, initSvc := range []func(*config.Config, state.Store) error{
		g.initStorage,
		g.initPubSub,
		g.initSecretManager,
		g.initTasks,
		g.initScheduler,
		g.initKMS,
		g.initLogging,
		g.initMonitoring,
		g.initFirestore,
		g.initBigQuery,
		g.initBigtable,
		g.initSpanner,
		g.initMemorystore,
		g.initCloudSQL,
		g.initCloudRun,
		g.initFunctions,
		g.initMetadata,
	} {
		if err := initSvc(cfg, store); err != nil {
			return nil, err
		}
	}

	if err := g.initConsole(cfg, build, store); err != nil {
		return nil, err
	}

	return g, nil
}

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

func (g *Gateway) handler() http.Handler {
	// otelhttp extracts the W3C traceparent header (when present) and
	// starts a server span named after the request method+path. When
	// tracing is disabled the global provider is a no-op, so the
	// wrapper degenerates to a couple of cheap interface calls per
	// request.
	rest := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := g.mux.Handler(r)
		if pattern == "" && g.storage != nil {
			if g.storage.HandleXML(w, r) {
				return
			}
		}
		g.mux.ServeHTTP(w, r)
	}), "gcp-local",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("GCP_LOCAL_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "REQ %s %s proto=%d ct=%q\n", r.Method, r.URL.String(), r.ProtoMajor, r.Header.Get("Content-Type"))
		}
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			// gRPC already has its own otelgrpc StatsHandler; let it run
			// without an extra otelhttp wrapper around the cleartext h2
			// frame so spans aren't double-counted.
			g.grpc.ServeHTTP(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	})
}

func (g *Gateway) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", g.cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if g.cfg.TLS.Enabled {
		// Real HTTP/2 over TLS. NextProtos must include "h2" so gRPC clients
		// keep working when they negotiate ALPN.
		srv.Handler = g.handler()
		srv.TLSConfig = &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
			MinVersion: tls.VersionTLS12,
		}
	} else {
		// h2c — HTTP/2 cleartext over a plain HTTP listener.
		h2s := &http2.Server{}
		srv.Handler = h2c.NewHandler(g.handler(), h2s)
	}

	errCh := make(chan error, 1)
	go func() {
		var err error
		if g.cfg.TLS.Enabled {
			err = srv.ListenAndServeTLS(g.cfg.TLS.CertFile, g.cfg.TLS.KeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if g.tasks != nil {
			g.tasks.Stop()
		}
		if g.pubsub != nil {
			g.pubsub.Stop()
		}
		if g.sched != nil {
			g.sched.Stop()
		}
		if g.mem != nil {
			g.mem.Stop(shutdownCtx)
		}
		if g.csql != nil {
			g.csql.Stop(shutdownCtx)
		}
		if g.bq != nil {
			_ = g.bq.Close()
		}
		if g.run != nil {
			g.run.Stop()
		}
		if g.funcs != nil {
			g.funcs.Stop()
		}
		stopGRPC(g.grpc, 3*time.Second)
		return errors.Join(srv.Shutdown(shutdownCtx), g.store.Close())
	case err := <-errCh:
		return err
	}
}
