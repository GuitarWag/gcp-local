# Spec 001 — Foundation

**Status:** Implemented (with documented divergences)
**Last updated:** May 2026

This is the original design document for `gcp-local`. It captures the scope,
architecture, and goals decided before any code was written. The current
implementation follows this spec with a small set of explicit divergences
listed in `README.md` and `CHANGELOG.md`.

---

## Overview

`gcp-local` is a single binary, zero-dependency local emulator for Google Cloud Platform services. It targets developers who want to build and test GCP-backed applications without hitting real GCP APIs, paying for usage, or running Docker. The closest analogues are LocalStack (AWS) and Minio (S3-only). No direct equivalent exists for GCP.

---

## Problem

GCP ships official emulators for a handful of services (Firestore, Pub/Sub, Bigtable, Datastore) as Docker images requiring Java. They are fragmented — each service runs separately with its own port and configuration — and have no unified startup, no shared state backend, and no web UI. Cloud SQL, Secret Manager, Cloud Tasks, KMS, and most other services have no emulator at all.

The result is that local development against GCP either requires a live GCP project (slow, costs money, requires network) or a patchwork of containers that are painful to orchestrate and break in CI.

---

## Goals

- One binary. `brew install gcp-local` or `go install`, then `gcp-local start`.
- No Docker, no Java, no external processes required.
- GCP SDKs in Go, Python, and TypeScript connect without code changes, using environment variable overrides.
- 20 services supported in v1, covering the most common GCP usage patterns.
- Persistent state via BoltDB (default) or in-memory (flag).
- Seed data and reset supported for test automation.
- Web dashboard for inspecting local state (buckets, topics, secrets, queues).

---

## Non-goals

- 100% GCP API fidelity. Edge cases, IAM policy enforcement, and billing are explicitly out of scope.
- Production use. This is a dev/test tool only.
- Replacing Terraform or deployment tooling.
- GUI application. CLI only, with an embedded web dashboard.

---

## Users

**Primary:** Backend engineers building GCP-integrated services who want fast, offline-capable local development.

**Secondary:** CI pipelines running integration tests against GCP services without provisioning real infrastructure.

---

## Services — v1 scope

Grouped by implementation complexity.

### Tier 1 — REST shim (weeks each)

These services have straightforward REST APIs and map cleanly to in-process state.

| Service | Notes |
|---|---|
| Cloud Storage | GCS XML + JSON API. Objects on disk or BoltDB. |
| Pub/Sub | Topics, subscriptions, publish, pull, push delivery via goroutine. |
| Secret Manager | KV store with versioning. |
| Cloud Tasks | Queue with HTTP dispatch to local handlers. |
| Cloud Scheduler | Cron trigger → HTTP POST to configured endpoint. |
| KMS | Fake key management, AES encrypt/decrypt in-process. |
| Cloud Logging | Accept log writes, store queryable. |
| Cloud Monitoring | Accept metric writes, no-op alerting. |

### Tier 2 — protocol work (months each)

These require implementing gRPC proto stubs or non-trivial wire protocols.

| Service | Notes |
|---|---|
| Firestore | gRPC proto stubs. No Java dependency. |
| BigQuery | DuckDB as the query engine. SQL-compatible. |
| Bigtable | HBase-like gRPC surface. |
| Spanner | SQLite backend, gRPC surface. |
| Cloud Run | Process proxy. Runs local HTTP server, routes traffic. |
| Cloud Functions | Subprocess per function, HTTP trigger. |
| Dataflow | Simplified pipeline runner. Best-effort compat. |
| Vertex AI | Stub responses; optionally proxy to real endpoint. |

### Tier 3 — embedded engine (hardest, highest value)

These require embedding a real database engine to achieve wire compatibility.

| Service | Notes |
|---|---|
| Cloud SQL (Postgres) | Embed `pgembedded` (CGo) or SQLite with pg shim (pure Go). |
| Cloud SQL (MySQL) | Similar approach, lower priority than Postgres. |
| Memorystore | Embed `go-redis-server` or `miniredis`. |
| AlloyDB | Same path as Cloud SQL Postgres. |

---

## SDK compatibility

GCP SDKs support endpoint overrides designed for emulator use. `gcp-local` relies on three mechanisms:

**Environment variables** — SDKs check these before using real endpoints:

```bash
STORAGE_EMULATOR_HOST=localhost:4443
PUBSUB_EMULATOR_HOST=localhost:4443
FIRESTORE_EMULATOR_HOST=localhost:4443
BIGTABLE_EMULATOR_HOST=localhost:4443
SPANNER_EMULATOR_HOST=localhost:4443
GOOGLE_CLOUD_PROJECT=local-project
GOOGLE_APPLICATION_CREDENTIALS=~/.gcp-local/fake-creds.json
```

**Credential bypass** — a fake service account JSON file that satisfies SDK auth checks without hitting Google's token endpoint. `gcp-local start` writes this file and sets the env var automatically.

**TLS** — v1 ships with HTTP only. Apps must use `option.WithoutAuthentication()` and `http://` endpoints where env vars don't cover it. v2 will add a self-signed cert + local trust store installation for fully transparent TLS.

`eval $(gcp-local env)` prints and applies all required variables for the current shell.

---

## Configuration

```yaml
# gcp-local.yaml
project: local-project
port: 4443
state: boltdb        # memory | boltdb
dashboard: true
dashboard_port: 4444

services:
  storage:
    enabled: true
    buckets:
      - name: my-bucket
        seed: ./fixtures/bucket/

  pubsub:
    enabled: true
    topics:
      - name: my-topic
        subscriptions:
          - name: my-sub
            push_endpoint: http://localhost:8080/pubsub

  cloudsql:
    enabled: true
    engine: sqlite     # sqlite | postgres
    instances:
      - name: main
        database: mydb
        seed: ./fixtures/schema.sql

  secretmanager:
    enabled: true
    secrets:
      - name: api-key
        value: local-dev-secret
```

---

## CLI

```
gcp-local start              # start all enabled services
gcp-local start --no-daemon  # foreground (useful in CI)
gcp-local stop
gcp-local status
gcp-local env                # print export statements for current shell
gcp-local reset              # wipe all state, re-apply seed data
gcp-local reset --service=pubsub
```

---

## Health endpoint

`GET /healthz` returns `200` only once all enabled services are ready. Used by test harnesses instead of `sleep`.

```json
{
  "status": "ok",
  "services": {
    "storage": "ready",
    "pubsub": "ready",
    "firestore": "starting"
  }
}
```

---

## Testing strategy

The test suite covers all three primary SDK languages: Go, Python, and TypeScript. Each language uses the same pattern: start the emulator once per test run, configure clients via env vars or explicit endpoint override, run assertions, tear down.

### Go

```go
// tests/go/testutil/client.go
func Start(t *testing.T) *Emulator {
    t.Helper()
    cmd := exec.Command("gcp-local", "start", "--port=4443", "--no-daemon")
    if err := cmd.Start(); err != nil {
        t.Fatalf("failed to start gcp-local: %v", err)
    }
    waitReady(t, "localhost:4443")
    t.Cleanup(func() { cmd.Process.Kill() })
    return &Emulator{Host: "localhost:4443"}
}

func waitReady(t *testing.T, host string) {
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        resp, err := http.Get("http://" + host + "/healthz")
        if err == nil && resp.StatusCode == 200 {
            return
        }
        time.Sleep(50 * time.Millisecond)
    }
    t.Fatal("gcp-local not ready after 5s")
}
```

### Python

```python
# tests/python/conftest.py
@pytest.fixture(scope="session")
def emulator():
    proc = subprocess.Popen(
        ["gcp-local", "start", "--port=4443", "--no-daemon"]
    )
    _wait_ready("localhost:4443")
    yield {"host": "localhost:4443"}
    proc.terminate()

@pytest.fixture
def storage_client(emulator):
    return storage.Client(
        project="local-project",
        credentials=AnonymousCredentials(),
        client_options={"api_endpoint": f"http://{emulator['host']}"},
    )
```

### TypeScript

```typescript
// tests/typescript/setup.ts
export async function setup() {
    proc = spawn("gcp-local", ["start", "--port=4443", "--no-daemon"])
    await waitReady("localhost:4443")
    process.env.GCP_LOCAL_HOST = "localhost:4443"
}
```

---

## Project structure

```
gcp-local/
├── cmd/gcp-local/
│   └── main.go
├── internal/
│   ├── gateway/          # HTTP + gRPC multiplexer
│   ├── auth/             # credential bypass, fake token server
│   ├── state/
│   │   ├── memory.go
│   │   └── boltdb.go
│   ├── services/
│   │   ├── storage/
│   │   ├── pubsub/
│   │   ├── firestore/
│   │   ├── secretmanager/
│   │   ├── tasks/
│   │   ├── scheduler/
│   │   ├── kms/
│   │   ├── bigquery/
│   │   └── cloudsql/
│   └── dashboard/        # embedded web UI via go:embed
├── tests/
│   ├── go/
│   ├── python/
│   └── typescript/
├── config/
│   └── schema.go
└── proto/                # vendored GCP proto stubs
```

---

## Architecture decisions

**Single port, host-based routing.** All services run behind one gateway on one port. The gateway inspects the `Host` header and URL prefix to dispatch to the right service handler. This matches how SDKs talk to real GCP (different hostnames, same TLS port).

**gRPC and REST on the same port.** The gateway detects `Content-Type: application/grpc` and routes to gRPC handlers; everything else goes to HTTP REST handlers.

**State interface.** Every service writes through a `Store` interface, not directly to disk. Swapping `memory` for `boltdb` (or adding SQLite later) requires no service-level changes.

**CloudSQL engine choice.** Default is `modernc.org/sqlite` (pure Go, zero CGo, zero external binary). Apps using Postgres-specific types or functions can opt into `pgembedded`, which shells out to a real Postgres binary (~90MB download on first use). This is flagged in config per-instance.

**No Firestore Java wrapper.** Implement gRPC proto stubs directly using `google.golang.org/genproto`. Slower to build but eliminates the Java dependency entirely.

---

## Risks

| Risk | Mitigation |
|---|---|
| GCP proto changes break emulator | Pin proto versions, add a compat test suite against real GCP in CI |
| CloudSQL SQLite gaps cause false-green tests | Document limitations clearly; pgembedded opt-in for Postgres-specific usage |
| SDK auth flow changes | Abstract auth bypass behind an interface; monitor GCP SDK changelogs |
| gRPC service surface is large | Prioritise methods by usage frequency; return `UNIMPLEMENTED` for the long tail |

---

## Milestones

| Milestone | Scope |
|---|---|
| M1 | Gateway + auth bypass + Storage + Pub/Sub + `/healthz`. Go tests passing. |
| M2 | Secret Manager, Tasks, Scheduler, KMS. Python tests passing. |
| M3 | Firestore (gRPC). TypeScript tests passing. All Tier 1 services done. |
| M4 | BigQuery (DuckDB), Bigtable, Spanner. |
| M5 | CloudSQL (SQLite default, pgembedded opt-in), Memorystore. |
| M6 | Web dashboard, `gcp-local reset`, seed data, CI hardening. |
| M7 | Cloud Run + Cloud Functions (process proxy). |
