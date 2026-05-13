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

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/dashboard"
	"github.com/GuitarWag/gcp-local/internal/health"
	"github.com/GuitarWag/gcp-local/internal/services/bigquery"
	"github.com/GuitarWag/gcp-local/internal/services/bigtable"
	"github.com/GuitarWag/gcp-local/internal/services/cloudrun"
	"github.com/GuitarWag/gcp-local/internal/services/cloudsql"
	"github.com/GuitarWag/gcp-local/internal/services/firestore"
	"github.com/GuitarWag/gcp-local/internal/services/kms"
	"github.com/GuitarWag/gcp-local/internal/services/logging"
	"github.com/GuitarWag/gcp-local/internal/services/memorystore"
	"github.com/GuitarWag/gcp-local/internal/services/monitoring"
	"github.com/GuitarWag/gcp-local/internal/services/pubsub"
	"github.com/GuitarWag/gcp-local/internal/services/scheduler"
	"github.com/GuitarWag/gcp-local/internal/services/secretmanager"
	"github.com/GuitarWag/gcp-local/internal/services/spanner"
	"github.com/GuitarWag/gcp-local/internal/services/storage"
	"github.com/GuitarWag/gcp-local/internal/services/tasks"
	"github.com/GuitarWag/gcp-local/internal/state"
)

type Gateway struct {
	cfg     *config.Config
	store   state.Store
	mux     *http.ServeMux
	health  *health.Registry
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
	grpc    *grpc.Server
}

type v1Handler func(http.ResponseWriter, *http.Request, []string) bool

func New(cfg *config.Config) (*Gateway, error) {
	store, err := state.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}

	g := &Gateway{
		cfg:    cfg,
		store:  store,
		mux:    http.NewServeMux(),
		health: health.New(),
		grpc:   grpc.NewServer(),
	}

	g.mux.HandleFunc("/healthz", g.health.Handler())
	g.mux.HandleFunc("/admin/reset", g.handleReset)
	if cfg.Dashboard {
		dashboard.New(store).Register(g.mux)
	}
	g.mux.HandleFunc("/v1/projects/", g.dispatchV1)
	g.mux.HandleFunc("/v2/projects/", g.dispatchV2)
	g.mux.HandleFunc("/v3/projects/", g.dispatchV3)

	if cfg.Services.Storage.Enabled {
		svc, err := storage.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init storage: %w", err)
		}
		g.storage = svc
		g.health.Set(svc.Name(), health.StatusStarting)
		svc.Register(g.mux)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.PubSub.Enabled {
		svc, err := pubsub.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init pubsub: %w", err)
		}
		g.pubsub = svc
		g.health.Set(svc.Name(), health.StatusStarting)
		svc.Register(g.mux)
		svc.RegisterGRPC(g.grpc)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.SecretManager.Enabled {
		svc, err := secretmanager.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init secretmanager: %w", err)
		}
		g.sm = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Tasks.Enabled {
		svc, err := tasks.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init tasks: %w", err)
		}
		g.tasks = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Scheduler.Enabled {
		svc, err := scheduler.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init scheduler: %w", err)
		}
		g.sched = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.KMS.Enabled {
		svc, err := kms.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init kms: %w", err)
		}
		g.kms = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Logging.Enabled {
		svc, err := logging.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init logging: %w", err)
		}
		g.logging = svc
		svc.Register(g.mux)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Monitoring.Enabled {
		svc, err := monitoring.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init monitoring: %w", err)
		}
		g.mon = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Firestore.Enabled {
		svc, err := firestore.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init firestore: %w", err)
		}
		g.fs = svc
		svc.RegisterGRPC(g.grpc)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.BigQuery.Enabled {
		svc, err := bigquery.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init bigquery: %w", err)
		}
		g.bq = svc
		svc.Register(g.mux)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Bigtable.Enabled {
		svc, err := bigtable.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init bigtable: %w", err)
		}
		g.bt = svc
		svc.RegisterGRPC(g.grpc)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Spanner.Enabled {
		svc, err := spanner.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init spanner: %w", err)
		}
		g.sp = svc
		svc.RegisterGRPC(g.grpc)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Memorystore.Enabled {
		svc, err := memorystore.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init memorystore: %w", err)
		}
		g.mem = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.CloudSQL.Enabled {
		svc, err := cloudsql.New(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init cloudsql: %w", err)
		}
		g.csql = svc
		svc.Register(g.mux)
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.CloudRun.Enabled {
		svc, err := cloudrun.NewCloudRun(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init cloudrun: %w", err)
		}
		g.run = svc
		g.health.Set(svc.Name(), health.StatusReady)
	}
	if cfg.Services.Functions.Enabled {
		svc, err := cloudrun.NewFunctions(store, cfg)
		if err != nil {
			return nil, fmt.Errorf("init functions: %w", err)
		}
		g.funcs = svc
		g.health.Set(svc.Name(), health.StatusReady)
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
	handlers := []v1Handler{}
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

func splitPath(path, prefix string) []string {
	rest := strings.TrimPrefix(path, prefix)
	return strings.Split(rest, "/")
}

func (g *Gateway) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("GCP_LOCAL_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "REQ %s %s proto=%d ct=%q\n", r.Method, r.URL.String(), r.ProtoMajor, r.Header.Get("Content-Type"))
		}
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			g.grpc.ServeHTTP(w, r)
			return
		}
		_, pattern := g.mux.Handler(r)
		if pattern == "" && g.storage != nil {
			if g.storage.HandleXML(w, r) {
				return
			}
		}
		g.mux.ServeHTTP(w, r)
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
		if g.bq != nil {
			_ = g.bq.Close()
		}
		g.grpc.GracefulStop()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return g.store.Close()
	case err := <-errCh:
		return err
	}
}
