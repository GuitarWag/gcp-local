# gcp-local

[![CI](https://github.com/GuitarWag/gcp-local/actions/workflows/ci.yml/badge.svg)](https://github.com/GuitarWag/gcp-local/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/GuitarWag/gcp-local.svg)](https://pkg.go.dev/github.com/GuitarWag/gcp-local)
[![Go Report Card](https://goreportcard.com/badge/github.com/GuitarWag/gcp-local)](https://goreportcard.com/report/github.com/GuitarWag/gcp-local)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/GuitarWag/gcp-local)](https://github.com/GuitarWag/gcp-local/releases)

**A single binary that pretends to be Google Cloud.** No Docker, no Java, no
GCP project, no internet. Point your existing SDK code at `localhost`, get on
with your day.

If you've used [LocalStack](https://www.localstack.cloud/),
[ministack](https://github.com/ministackorg/ministack), or
[floci](https://github.com/floci-io/floci) for AWS, `gcp-local` is the GCP
counterpart — same model, different cloud, written in pure Go as a single
download.

```bash
go install github.com/GuitarWag/gcp-local/cmd/gcp-local@latest
gcp-local start
eval $(gcp-local env)
```

Your `cloud.google.com/go/storage`, `google-cloud-pubsub`, `@google-cloud/firestore`
clients will start hitting the local emulator. No code changes.

---

## Why

The official GCP emulators are a patchwork: Java images for Firestore and
Pub/Sub, a separate binary for Bigtable, nothing for Secret Manager, KMS,
Cloud Tasks, Cloud Scheduler, Cloud Logging, Cloud Monitoring, Cloud Run,
Cloud Functions. Each runs on its own port. None share state. None ship a
dashboard.

`gcp-local` replaces all of that with one binary that:

- Speaks the real GCP REST and gRPC surfaces (verified against the official
  SDKs in Go, Python, and TypeScript).
- Listens on a single port. SDKs hit it via the standard
  `STORAGE_EMULATOR_HOST` / `PUBSUB_EMULATOR_HOST` / etc. environment
  variables.
- Persists state to BoltDB or keeps it in memory, your choice.
- Boots in ~50 ms. Restarts cleanly. Resets state on demand.
- Has 102 integration tests using the real SDK clients, including
  race-detector-clean concurrency tests.

---

## Use cases

### 1. Local development without burning GCP credits

You're building a service that writes to Cloud Storage, publishes to
Pub/Sub, reads secrets from Secret Manager, and stores documents in
Firestore. Spinning up a real GCP project for every developer is slow and
costs money. Mocking each SDK in unit tests is brittle and misses real
behaviour.

```bash
# terminal 1
gcp-local start
eval $(gcp-local env)

# terminal 2 — your app, unchanged
go run ./cmd/myapp
```

The SDK reads the env vars, hits `localhost:4443`, and the emulator behaves
exactly like the real services for the surface you actually use.

Open `http://localhost:4443/dashboard` to see buckets, topics, secrets, and
queues update live as your app writes to them.

### 2. CI integration tests

A GitHub Actions job that runs your full integration suite against the
emulator, with no GCP credentials required and no Docker pulls:

```yaml
# .github/workflows/test.yml
jobs:
  integration:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.25' }

      - name: Install gcp-local
        run: go install github.com/GuitarWag/gcp-local/cmd/gcp-local@latest

      - name: Start emulator
        run: |
          gcp-local start --no-daemon &
          curl --retry 20 --retry-delay 1 --retry-connrefused \
            http://localhost:4443/healthz

      - name: Run integration tests
        env:
          STORAGE_EMULATOR_HOST: http://localhost:4443
          PUBSUB_EMULATOR_HOST: localhost:4443
          FIRESTORE_EMULATOR_HOST: localhost:4443
          GOOGLE_CLOUD_PROJECT: ci-project
        run: go test ./...
```

No service account JSON in CI secrets. No flaky network. No quota limits.

### 3. Real-SDK integration tests in your own repo

You want the safety of testing against the actual GCP client libraries
without mocking. Spawn the emulator from your test harness:

```go
// pkg/internal/testutil/emulator.go
func StartEmulator(t *testing.T) string {
    t.Helper()
    port := freePort(t)
    cmd := exec.Command("gcp-local", "start",
        "--port="+strconv.Itoa(port), "--no-daemon")
    cmd.Start()
    t.Cleanup(func() { _ = cmd.Process.Kill() })
    waitReady(t, port)
    return fmt.Sprintf("localhost:%d", port)
}

func TestPublishesOrder(t *testing.T) {
    host := StartEmulator(t)
    t.Setenv("PUBSUB_EMULATOR_HOST", host)

    client, _ := pubsub.NewClient(context.Background(), "test-project")
    topic, _ := client.CreateTopic(context.Background(), "orders")

    // your real production code path
    NewOrderService(client).Place(Order{ID: "abc"})

    sub, _ := client.CreateSubscription(...)
    // assert message arrived
}
```

This is the same Pub/Sub client your production code uses, talking the same
gRPC wire protocol, hitting an in-process emulator. Race-detector clean.

### 4. Reproducible bug investigations

Need to reproduce a customer report that involves five GCP services
interacting? Spin up `gcp-local` with a YAML config that seeds the exact
state, replay the operation, restart, replay it again.

```yaml
# repro.yaml
services:
  storage:
    enabled: true
    buckets:
      - name: customer-uploads
        seed: ./fixtures/customer-files/
  secretmanager:
    enabled: true
    secrets:
      - name: stripe-key
        value: sk_test_local
```

```bash
gcp-local start --config=repro.yaml
```

---

## Install

**Go users:**

```bash
go install github.com/GuitarWag/gcp-local/cmd/gcp-local@latest
```

**Pre-built binaries** for macOS and Linux (amd64 + arm64) on each release:

```
https://github.com/GuitarWag/gcp-local/releases/latest
```

Download the matching `tar.gz`, extract, drop the binary on your `PATH`.

**From source:**

```bash
git clone https://github.com/GuitarWag/gcp-local
cd gcp-local
go build -o gcp-local ./cmd/gcp-local
```

No CGo. Builds on macOS and Linux with stock Go ≥ 1.25.

## Quick reference

```bash
gcp-local start                  # daemon mode, port 4443
gcp-local start --no-daemon      # foreground (CI-friendly)
gcp-local status                 # readiness summary
gcp-local env                    # print exports for the current shell
gcp-local stop
gcp-local reset                  # wipe all state
```

```bash
curl http://localhost:4443/healthz       # readiness JSON
open http://localhost:4443/dashboard     # live state view
```

## Config

Drop a `gcp-local.yaml` next to your project and pass `--config=path.yaml`:

```yaml
project: local-project
port: 4443
state: memory          # memory | boltdb
state_dir: ./local.db  # used when state=boltdb
dashboard: true

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

  memorystore:
    enabled: false     # disabled by default — listens on its own redis port
    port: 6379
```

## Services

| Service          | Status            | Notes                                              |
|------------------|-------------------|----------------------------------------------------|
| Cloud Storage    | working           | Real SDK compat via `STORAGE_EMULATOR_HOST`. JSON + XML APIs (GET/PUT/DELETE). Multipart and chunked resumable uploads with status query + 308 Resume Incomplete. |
| Pub/Sub          | working           | REST + full gRPC incl `StreamingPull`. Ack deadlines, nack, modAck redelivery. Push subscriptions deliver to `pushConfig.pushEndpoint` with retry on non-2xx. |
| Secret Manager   | working           | Full CRUD + versions + access. Cascade-delete versions. |
| Cloud Tasks      | working           | Queue CRUD + HTTP dispatch to target URL. Honours `scheduleTime`, retries 5xx with exponential backoff (5 attempts). |
| Cloud Scheduler  | working           | Standard 5-field cron (`0 9 * * 1-5`), `@every <dur>`, and legacy `every <dur>`. |
| KMS              | working           | AES-GCM encrypt/decrypt with generated key material. |
| Cloud Logging    | working           | `WriteLogEntries` + `ListLogEntries` with filter subset (`severity`, `logName`, `resource.type`, `resource.labels.*`, `timestamp`, `AND`). |
| Cloud Monitoring | working           | `CreateTimeSeries` + `ListTimeSeries`.             |
| Firestore        | working (subset)  | gRPC: `Commit`, `GetDocument`, `RunQuery`, `BatchWrite`, `Listen` streaming (DocumentRef + Query snapshots, no Where/OrderBy filters). |
| BigQuery         | working (subset)  | SQLite backend. REST datasets/tables/insertAll/queries. Typed column schema. |
| Memorystore      | working           | miniredis on its own port. Disabled by default.    |
| Cloud Run        | partial           | REST CRUD + invoke proxy to `backendUrl`. No subprocess execution. |
| Cloud Functions  | partial           | Same shape as Cloud Run.                           |
| CloudSQL         | stub              | REST instance management only.                     |
| Bigtable         | stub              | gRPC connection succeeds; data methods return `UNIMPLEMENTED`. |
| Spanner          | stub              | `CreateSession` works; data methods return `UNIMPLEMENTED`. |
| Dataflow         | not implemented   |                                                     |
| Vertex AI        | not implemented   |                                                     |
| AlloyDB          | not implemented   |                                                     |

## SDK compatibility

Set these env vars so the SDK clients hit the emulator instead of real GCP:

```bash
export STORAGE_EMULATOR_HOST=http://localhost:4443
export PUBSUB_EMULATOR_HOST=localhost:4443
export FIRESTORE_EMULATOR_HOST=localhost:4443
export GOOGLE_CLOUD_PROJECT=local-project
export GOOGLE_APPLICATION_CREDENTIALS=~/.gcp-local/fake-creds.json
```

`eval $(gcp-local env)` exports all of them at once.

A fake service account JSON is written to `~/.gcp-local/fake-creds.json` on
first start; it satisfies SDK auth checks without contacting Google's token
endpoint.

## Architecture

- One HTTP server with h2c. gRPC and REST share one port.
- Path-prefix routing. SDKs pointed at `localhost:PORT` work because path
  prefixes identify the service.
- All service state writes through a `state.Store` interface. Backends:
  in-memory map (default) or BoltDB file.
- Services register HTTP handlers and/or gRPC servers; a central dispatcher
  routes `/v1/projects/`, `/v2/projects/`, `/v3/projects/` to the right
  service by URL segment.

## Tests

```bash
# Go (64 tests, race-detector clean)
cd tests/go && go test -race ./...

# Python (18 tests)
cd tests/python
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
.venv/bin/python -m pytest

# TypeScript (20 tests)
cd tests/typescript
pnpm install
pnpm test
```

102 integration tests total. Tests use the real GCP client libraries — they
exercise the same wire surface your production code will hit.

## Divergences from the design spec

The design document is in `specs/001-foundation.md`. The implementation
matches it with these explicit gaps:

- Routing is URL-prefix, not Host header (functionally equivalent for
  emulator use).
- BigQuery uses pure-Go SQLite, not DuckDB. SQL surface is a subset.
- CloudSQL has no DB engine — only the instance admin REST API.
- Bigtable and Spanner are gRPC stubs that return `UNIMPLEMENTED` for data
  operations.
- Cloud Run / Cloud Functions proxy to an existing `backendUrl`. There is no
  subprocess execution.
- Dataflow, Vertex AI, AlloyDB, Cloud SQL MySQL — not implemented.
- Default state backend is `memory`, not `boltdb`.

## Related projects

For AWS, the same idea has been done well by several teams. If you work on
mixed AWS/GCP stacks you'll likely want both:

- **[LocalStack](https://www.localstack.cloud/)** — the original local AWS
  emulator. Python, Docker-based, broad service coverage. Open core + paid tiers.
- **[ministack](https://github.com/ministackorg/ministack)** — open-source local
  AWS emulator. 40+ services, Terraform compatible, real databases. MIT.
- **[floci](https://github.com/floci-io/floci)** — Java-based AWS local
  emulator, also positioned as a LocalStack alternative.

`gcp-local` covers Google Cloud only and is intentionally smaller in scope —
single Go binary, no Docker, no Java, no plugin system. Use it alongside one
of the above if your stack spans both clouds.

## License

MIT — see `LICENSE`.
