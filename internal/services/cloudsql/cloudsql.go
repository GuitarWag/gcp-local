package cloudsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
	"github.com/GuitarWag/gcp-local/internal/services/cloudsql/mysqlwire"
	"github.com/GuitarWag/gcp-local/internal/services/cloudsql/pgwire"
	"github.com/GuitarWag/gcp-local/internal/state"

	_ "modernc.org/sqlite" // sqlite driver registered as "sqlite" with database/sql
)

// CloudSQL accepts real Postgres-wire or MySQL-wire connections backed by an
// in-memory sqlite database, one per instance. The admin REST surface tracks
// instance metadata; the wire listener for each instance binds either to an
// explicit port (config) or to an OS-assigned port (default).
//
// Engines:
//   - `sqlite` (default), `postgres`: Postgres wire shim in `pgwire`.
//   - `mysql`: MySQL wire shim in `mysqlwire`, also sqlite-backed.

const nsInstances = "cloudsql/instances"

// instanceListener is the slice of the per-engine listener API the service
// actually needs: stop on shutdown. Both pgwire.Listener and
// mysqlwire.Listener implement it.
type instanceListener interface {
	Stop() error
}

type instance struct {
	Name            string    `json:"name"`
	Project         string    `json:"project"`
	DatabaseVersion string    `json:"databaseVersion"`
	State           string    `json:"state"`
	CreateTime      time.Time `json:"createTime"`

	// emulator-specific
	Engine   string `json:"engine,omitempty"`
	Database string `json:"database,omitempty"`
	Port     int    `json:"port,omitempty"`
	Host     string `json:"host,omitempty"`
}

type Service struct {
	store    state.Store
	project  string
	basePort int

	mu        sync.Mutex
	listeners map[string]instanceListener
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	s := &Service{
		store:     store,
		project:   cfg.Project,
		basePort:  cfg.Services.CloudSQL.BasePort,
		listeners: map[string]instanceListener{},
	}
	for i, inst := range cfg.Services.CloudSQL.Instances {
		port := inst.Port
		if port == 0 && s.basePort > 0 {
			port = s.basePort + i
		}
		eng := inst.Engine
		if eng == "" {
			eng = "sqlite"
		}
		body := instance{
			Name:            inst.Name,
			Project:         cfg.Project,
			DatabaseVersion: defaultDatabaseVersion(eng),
			State:           "RUNNABLE",
			CreateTime:      time.Now().UTC(),
			Engine:          eng,
			Database:        inst.Database,
			Port:            port,
		}
		if err := s.startInstance(&body, inst.Seed); err != nil {
			return nil, fmt.Errorf("init cloudsql instance %s: %w", inst.Name, err)
		}
		key := keyFor(cfg.Project, inst.Name)
		data, _ := json.Marshal(body)
		_ = s.store.Put(nsInstances, key, data)
	}
	return s, nil
}

func (s *Service) Name() string { return "cloudsql" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/sql/v1beta4/projects/", s.dispatch)
}

// Stop closes all listeners. Idempotent.
func (s *Service) Stop(_ context.Context) {
	s.mu.Lock()
	listeners := s.listeners
	s.listeners = map[string]instanceListener{}
	s.mu.Unlock()
	for _, l := range listeners {
		_ = l.Stop()
	}
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/sql/v1beta4/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "instances" {
		http.NotFound(w, r)
		return
	}
	switch len(parts) {
	case 2:
		s.collection(w, r, parts[0])
	case 3:
		s.item(w, r, parts[0], parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) collection(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodPost:
		var body instance
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		body.Project = project
		if body.Engine == "" {
			body.Engine = "sqlite"
		}
		if body.DatabaseVersion == "" {
			body.DatabaseVersion = defaultDatabaseVersion(body.Engine)
		}
		body.State = "RUNNABLE"
		body.CreateTime = time.Now().UTC()
		key := keyFor(project, body.Name)
		if _, err := s.store.Get(nsInstances, key); err == nil {
			writeErr(w, http.StatusConflict, "instance exists")
			return
		}
		if err := s.startInstance(&body, ""); err != nil {
			writeErr(w, http.StatusInternalServerError, "start instance: "+err.Error())
			return
		}
		data, _ := json.Marshal(body)
		_ = s.store.Put(nsInstances, key, data)
		writeJSON(w, http.StatusOK, body)
	case http.MethodGet:
		prefix := fmt.Sprintf("projects/%s/instances/", project)
		all, _ := s.store.List(nsInstances, prefix)
		out := struct {
			Items []instance `json:"items"`
		}{Items: []instance{}}
		for _, v := range all {
			var i instance
			if json.Unmarshal(v, &i) == nil {
				out.Items = append(out.Items, i)
			}
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) item(w http.ResponseWriter, r *http.Request, project, name string) {
	key := keyFor(project, name)
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.Get(nsInstances, key)
		if errors.Is(err, state.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		if _, err := s.store.Get(nsInstances, key); errors.Is(err, state.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		s.stopInstance(key)
		_ = s.store.Delete(nsInstances, key)
		w.WriteHeader(http.StatusOK)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// startInstance opens the sqlite DB for the instance, runs optional seed
// SQL, then starts the wire listener that matches body.Engine. On success
// body.Port is updated to the actual bound port (relevant when the
// requested port was 0).
func (s *Service) startInstance(body *instance, seedPath string) error {
	switch body.Engine {
	case "", "sqlite", "postgres", "mysql":
		// supported
	default:
		return fmt.Errorf("engine %q not supported", body.Engine)
	}
	dsn := fmt.Sprintf("file:cloudsql_%s_%s?mode=memory&cache=shared", body.Project, body.Name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	// shared-cache in-memory needs at least one connection alive to keep the
	// DB from being released between checkouts.
	db.SetMaxOpenConns(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}
	if seedPath != "" {
		seedBytes, err := os.ReadFile(seedPath)
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("read seed %s: %w", seedPath, err)
		}
		if _, err := db.Exec(string(seedBytes)); err != nil {
			_ = db.Close()
			return fmt.Errorf("apply seed: %w", err)
		}
	}
	addr := fmt.Sprintf("127.0.0.1:%d", body.Port)
	ln, bound, err := startListener(body.Engine, body.Name, body.Database, addr, db)
	if err != nil {
		_ = db.Close()
		return err
	}
	body.Host = "127.0.0.1"
	body.Port = portFromAddr(bound)
	s.mu.Lock()
	s.listeners[keyFor(body.Project, body.Name)] = ln
	s.mu.Unlock()
	return nil
}

// startListener picks the wire shim that matches the engine and returns the
// running listener along with its bound address.
func startListener(engine, name, database, addr string, db *sql.DB) (instanceListener, string, error) {
	switch engine {
	case "mysql":
		ln := mysqlwire.NewListener(name, database, addr, db)
		bound, err := ln.Start()
		if err != nil {
			return nil, "", err
		}
		return ln, bound, nil
	default:
		ln := pgwire.NewListener(name, database, addr, db)
		bound, err := ln.Start()
		if err != nil {
			return nil, "", err
		}
		return ln, bound, nil
	}
}

func defaultDatabaseVersion(engine string) string {
	if engine == "mysql" {
		return "MYSQL_8_0"
	}
	return "POSTGRES_15"
}

func (s *Service) stopInstance(key string) {
	s.mu.Lock()
	ln, ok := s.listeners[key]
	delete(s.listeners, key)
	s.mu.Unlock()
	if ok {
		_ = ln.Stop()
	}
}

func keyFor(project, name string) string {
	return fmt.Sprintf("projects/%s/instances/%s", project, name)
}

func portFromAddr(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	var n int
	for _, c := range addr[i+1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
