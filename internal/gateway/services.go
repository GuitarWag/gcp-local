package gateway

import (
	"fmt"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/dashboard/console"
	"github.com/GuitarWag/gcp-local/internal/health"
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

// initIAMAdmin constructs the IAM admin service. It has no Enabled flag
// and no error path, so it always runs.
func (g *Gateway) initIAMAdmin(cfg *config.Config, store state.Store) {
	g.iamSA = iamadmin.New(store, cfg)
	g.health.Set(g.iamSA.Name(), health.StatusReady)
}

func (g *Gateway) initStorage(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Storage.Enabled {
		return nil
	}
	svc, err := storage.New(store, cfg, g.iam)
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}
	g.storage = svc
	g.health.Set(svc.Name(), health.StatusStarting)
	svc.Register(g.mux)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initPubSub(cfg *config.Config, store state.Store) error {
	if !cfg.Services.PubSub.Enabled {
		return nil
	}
	svc, err := pubsub.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init pubsub: %w", err)
	}
	g.pubsub = svc
	g.health.Set(svc.Name(), health.StatusStarting)
	svc.Register(g.mux)
	svc.RegisterGRPC(g.grpc)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initSecretManager(cfg *config.Config, store state.Store) error {
	if !cfg.Services.SecretManager.Enabled {
		return nil
	}
	svc, err := secretmanager.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init secretmanager: %w", err)
	}
	g.sm = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initTasks(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Tasks.Enabled {
		return nil
	}
	svc, err := tasks.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init tasks: %w", err)
	}
	g.tasks = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initScheduler(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Scheduler.Enabled {
		return nil
	}
	svc, err := scheduler.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init scheduler: %w", err)
	}
	g.sched = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initKMS(cfg *config.Config, store state.Store) error {
	if !cfg.Services.KMS.Enabled {
		return nil
	}
	svc, err := kms.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init kms: %w", err)
	}
	g.kms = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initLogging(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Logging.Enabled {
		return nil
	}
	svc, err := logging.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init logging: %w", err)
	}
	g.logging = svc
	svc.Register(g.mux)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initMonitoring(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Monitoring.Enabled {
		return nil
	}
	svc, err := monitoring.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init monitoring: %w", err)
	}
	g.mon = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initFirestore(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Firestore.Enabled {
		return nil
	}
	svc, err := firestore.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init firestore: %w", err)
	}
	g.fs = svc
	svc.RegisterGRPC(g.grpc)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initBigQuery(cfg *config.Config, store state.Store) error {
	if !cfg.Services.BigQuery.Enabled {
		return nil
	}
	svc, err := bigquery.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init bigquery: %w", err)
	}
	g.bq = svc
	svc.Register(g.mux)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initBigtable(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Bigtable.Enabled {
		return nil
	}
	svc, err := bigtable.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init bigtable: %w", err)
	}
	g.bt = svc
	svc.RegisterGRPC(g.grpc)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initSpanner(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Spanner.Enabled {
		return nil
	}
	svc, err := spanner.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init spanner: %w", err)
	}
	g.sp = svc
	svc.RegisterGRPC(g.grpc)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initMemorystore(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Memorystore.Enabled {
		return nil
	}
	svc, err := memorystore.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init memorystore: %w", err)
	}
	g.mem = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initCloudSQL(cfg *config.Config, store state.Store) error {
	if !cfg.Services.CloudSQL.Enabled {
		return nil
	}
	svc, err := cloudsql.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init cloudsql: %w", err)
	}
	g.csql = svc
	svc.Register(g.mux)
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initCloudRun(cfg *config.Config, store state.Store) error {
	if !cfg.Services.CloudRun.Enabled {
		return nil
	}
	svc, err := cloudrun.NewCloudRun(store, cfg)
	if err != nil {
		return fmt.Errorf("init cloudrun: %w", err)
	}
	g.run = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

func (g *Gateway) initFunctions(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Functions.Enabled {
		return nil
	}
	svc, err := cloudrun.NewFunctions(store, cfg)
	if err != nil {
		return fmt.Errorf("init functions: %w", err)
	}
	g.funcs = svc
	g.health.Set(svc.Name(), health.StatusReady)
	return nil
}

// initMetadata covers the metadata block verbatim, including the
// iamcredentials construction that takes the metadata service as input.
func (g *Gateway) initMetadata(cfg *config.Config, store state.Store) error {
	if !cfg.Services.Metadata.Enabled {
		return nil
	}
	svc, err := metadata.New(store, cfg)
	if err != nil {
		return fmt.Errorf("init metadata: %w", err)
	}
	g.meta = svc
	svc.Register(g.mux)
	g.health.Set(svc.Name(), health.StatusReady)

	iam, err := iamcredentials.New(store, cfg, svc)
	if err != nil {
		return fmt.Errorf("init iamcredentials: %w", err)
	}
	g.iamc = iam
	g.health.Set(iam.Name(), health.StatusReady)
	return nil
}

// initConsole wires the per-service console adapters only when the
// underlying service is constructed.
func (g *Gateway) initConsole(cfg *config.Config, build BuildInfo, store state.Store) error {
	if !cfg.Dashboard {
		return nil
	}
	copts := console.Options{
		Version: build.Version,
		Commit:  build.Commit,
		Project: cfg.Project,
		Health:  g.health,
	}
	// Wire each per-service console adapter only when the underlying
	// service is constructed. Each adapter is a small interface in
	// the console package; the service implements just what /console
	// renders, leaving the full REST/gRPC surface untouched. The
	// remaining services (storage, pubsub, firestore, secrets,
	// tasks, scheduler, kms) get wired here as their console
	// adapters land.
	if g.logging != nil {
		copts.Logging = g.logging
	}
	if g.storage != nil {
		copts.Storage = g.storage
	}
	if g.pubsub != nil {
		copts.PubSub = g.pubsub
	}
	if g.fs != nil {
		copts.Firestore = g.fs
	}
	if g.sm != nil {
		copts.Secrets = g.sm
	}
	if g.tasks != nil {
		copts.Tasks = g.tasks
	}
	if g.sched != nil {
		copts.Scheduler = g.sched
	}
	if g.kms != nil {
		copts.KMS = g.kms
	}
	if g.mon != nil {
		copts.Monitoring = g.mon
	}
	if g.bq != nil {
		copts.BigQuery = g.bq
	}
	if g.csql != nil {
		copts.CloudSQL = g.csql
	}
	if g.run != nil {
		copts.CloudRun = g.run
	}
	if g.funcs != nil {
		copts.Functions = g.funcs
	}
	if g.mem != nil {
		copts.Memorystore = g.mem
	}
	if g.bt != nil {
		copts.Bigtable = g.bt
	}
	if g.sp != nil {
		copts.Spanner = g.sp
	}
	cs, err := console.New(store, copts)
	if err != nil {
		return fmt.Errorf("init console: %w", err)
	}
	cs.Register(g.mux)
	return nil
}
