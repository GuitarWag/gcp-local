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
curl -sSL https://raw.githubusercontent.com/GuitarWag/gcp-local/main/install.sh | sh
gcp-local start
eval $(gcp-local env)
```

Your `cloud.google.com/go/storage`, `google-cloud-pubsub`, `@google-cloud/firestore`
clients will start hitting the local emulator. No code changes.

(Go devs: `go install github.com/GuitarWag/gcp-local/cmd/gcp-local@latest` works too — see [Install](#install) for the full list.)

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
- Has 130 integration tests using the real SDK clients, including
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
queues update live as your app writes to them. For a richer, per-service
view — log tail, object preview, message peek, encrypt/decrypt forms — open
`http://localhost:4443/console`.

To poke at the console with realistic data instead of a blank slate,
`./scripts/seed.sh` heavily seeds every emulated service in one shot
(60 log entries, 5 buckets with mixed objects, 5 topics with 60 messages,
10 secrets with versions, 3 queues with staggered tasks, 6 scheduler
jobs, 3 keyrings with keys).

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

**One-line installer** (macOS and Linux, amd64 and arm64):

```bash
curl -sSL https://raw.githubusercontent.com/GuitarWag/gcp-local/main/install.sh | sh
```

The script downloads the matching pre-built binary from the latest release
and drops it in `/usr/local/bin` (or `~/.local/bin` if you can't write there).

**Manual download** — grab the right `tar.gz` from
[the releases page](https://github.com/GuitarWag/gcp-local/releases/latest):

```bash
# Example for macOS arm64; swap arch/os for your machine.
curl -L https://github.com/GuitarWag/gcp-local/releases/latest/download/gcp-local_0.2.0_darwin_arm64.tar.gz | tar xz
sudo mv gcp-local /usr/local/bin/
```

**Go users:**

```bash
go install github.com/GuitarWag/gcp-local/cmd/gcp-local@latest
```

Then make sure `$(go env GOPATH)/bin` is on your `PATH`.

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
gcp-local start --tls            # HTTPS with a self-signed cert
gcp-local status                 # readiness summary
gcp-local env                    # print exports for the current shell
gcp-local gcloud-setup           # point the gcloud CLI at the emulator
gcp-local gcloud-teardown        # restore the default gcloud config
gcp-local stop
gcp-local reset                  # wipe all state
gcp-local trust install          # add the generated cert to macOS keychain
gcp-local trust uninstall        # remove it
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
| BigQuery         | working (subset)  | SQLite backend. REST datasets/tables/insertAll/queries. Typed column schema. SQL: `SELECT`/`INSERT`, `JOIN ... ON`, `GROUP BY`, `HAVING`, aggregates (`COUNT`/`SUM`/`AVG`/`MIN`/`MAX`), `ORDER BY`. Scalar translation: `CURRENT_TIMESTAMP()`, `SAFE_CAST` (no NULL-on-failure), `CONCAT`, `IFNULL`. Not translated: window functions, `STRUCT`, `ARRAY`, `UNNEST`, `PARTITION BY`, `WITH RECURSIVE`, BigQuery-specific date arithmetic. |
| Memorystore      | working           | miniredis on its own port. Disabled by default.    |
| Cloud Run        | working (subset)  | REST CRUD + invoke. Either proxies to `backendUrl` or spawns the configured `command` on first invoke (PORT + K_SERVICE env, cached child handle, terminated on resource delete / shutdown). No container image support. |
| Cloud Functions  | working (subset)  | Same shape as Cloud Run.                           |
| CloudSQL         | working (subset)  | REST instance admin + real Postgres or MySQL wire protocol. Engines: `sqlite` (default, Postgres-wire), `mysql` (MySQL-wire). Both backed by in-memory sqlite. Per-instance TCP listener, schema + CRUD + transactions. pgembedded opt-in not yet implemented. |
| Metadata server  | working           | `/computeMetadata/v1/...` shaped like GCE/Cloud Run's metadata service. `instance/service-accounts/default/{token,email,scopes,identity}`, `project/{project-id,numeric-project-id}`. Identity endpoint returns an RS256-signed JWT carrying the requested `audience`. |
| IAM credentials  | working           | `iamcredentials.googleapis.com` `generateAccessToken` and `generateIdToken` for service-account impersonation. Shares signing key with metadata server. |
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

If you'd rather use Application Default Credentials (ADC) instead of a creds
file, point the SDK's metadata client at the emulator and skip
`GOOGLE_APPLICATION_CREDENTIALS` entirely:

```bash
export GCE_METADATA_HOST=localhost:4443
unset GOOGLE_APPLICATION_CREDENTIALS
```

`google.DefaultTokenSource`, `google.auth.default()`, and the equivalent
Node and JVM helpers all hit `/computeMetadata/v1/...` once that env var is
set, so they pick up an emulator-issued token without any code changes. The
identity endpoint
(`/computeMetadata/v1/instance/service-accounts/default/identity?audience=...`)
returns an RS256-signed JWT carrying the requested audience claim, signed
with an in-memory key published at `/computeMetadata/v1/jwks`.

Service-account impersonation that goes through `iamcredentials.googleapis.com`
(`generateAccessToken` / `generateIdToken`) is mounted at
`/v1/projects/-/serviceAccounts/{email}:{action}` and reuses the same signing
key.

## Using with `gcloud`

The SDK env vars don't reach the `gcloud` CLI. To point `gcloud` commands
(`gcloud storage cp ...`, `gcloud pubsub topics publish ...`,
`gcloud secrets versions access ...`) at the emulator instead of real GCP:

```bash
eval "$(gcp-local gcloud-setup)"        # create + activate a dedicated gcloud config
gcloud storage buckets create gs://demo  # hits localhost:4443

eval "$(gcp-local gcloud-teardown)"      # switch back to your default gcloud config
```

See [docs/gcloud.md](docs/gcloud.md) for the full setup, per-service
example commands, and which `gcloud` verbs work against the emulator.

## TLS

Default is plain HTTP (h2c). For SDK clients that hard-code `https://`, start
with `--tls`:

```bash
gcp-local start --tls
```

First use generates an RSA-2048 self-signed cert valid for `localhost`,
`127.0.0.1`, and `::1` under `~/.gcp-local/tls/cert.pem` and `key.pem`. The
same cert is reused on every subsequent boot (delete the directory to rotate).
Over TLS the gateway speaks real HTTP/2 (ALPN `h2`), so gRPC clients continue
to work.

To stop seeing "certificate signed by unknown authority" errors:

```bash
gcp-local trust install            # macOS: adds cert to login keychain
gcp-local trust uninstall          # remove it
```

On Linux the command prints manual `update-ca-certificates` / NSS instructions
rather than poking system trust stores blindly. Windows is not supported.

## Tracing

`gcp-local` emits OpenTelemetry traces when the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` env var is set. With it unset (the
default), the global tracer provider is a no-op and there is no
exporter goroutine, so the disabled path costs nothing.

When enabled, every HTTP request and gRPC call gets a server span;
the W3C `traceparent` header on the incoming request is honoured, so
an app that already traces its own work sees a single trace that
threads through the emulator and back out to the state store.

Quick local setup with Jaeger all-in-one:

```bash
docker run --rm -p 16686:16686 -p 4318:4318 \
  jaegertracing/all-in-one:latest

OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
OTEL_SERVICE_NAME=gcp-local \
  gcp-local start --no-daemon
```

Open `http://localhost:16686` to browse traces. Any standard `OTEL_*`
env var the OpenTelemetry SDK understands works
(`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_HEADERS`, etc.).

`OTEL_EXPORTER_OTLP_PROTOCOL` (or the traces-specific
`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`) selects the exporter wire format.
`http/protobuf` is the default; `grpc` switches to OTLP/gRPC against an
OTLP-gRPC collector. Any other value is rejected at start-up.

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
# Go (92 tests, race-detector clean)
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

130 integration tests total. Tests use the real GCP client libraries — they
exercise the same wire surface your production code will hit.

## Divergences from the design spec

The design document is in `specs/001-foundation.md`. The implementation
matches it with these explicit gaps:

- Routing is URL-prefix, not Host header (functionally equivalent for
  emulator use).
- BigQuery uses pure-Go SQLite, not DuckDB. SQL surface is a subset.
- CloudSQL ships the default `sqlite` engine behind a Postgres wire shim
  (pgproto3 + modernc.org/sqlite) and a `mysql` engine behind a MySQL wire
  shim (go-mysql-org/go-mysql + the same sqlite handle). The `postgres`
  engine (real pgembedded binary) is not implemented yet.
- Bigtable and Spanner are gRPC stubs that return `UNIMPLEMENTED` for data
  operations.
- Cloud Run / Cloud Functions can spawn a local executable (`command:` on the
  resource) or proxy to an existing `backendUrl`. There is no container image
  support — bring your own binary.
- Dataflow, Vertex AI, AlloyDB — not implemented.
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
