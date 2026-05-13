# Claude guidance for gcp-local

This is a single-binary local emulator for GCP services. Pure Go, no Docker,
no Java. Read `specs/001-foundation.md` for design intent and `README.md`
for current state.

## Repo layout

```
cmd/gcp-local/        CLI entry point
internal/
  gateway/            HTTP server + h2c, dispatches /v1, /v2, /v3 paths
  state/              Store interface; memory + BoltDB backends
  auth/               Fake creds writer
  health/             Service readiness registry, /healthz
  pidfile/            Daemon PID file
  dashboard/          go:embed HTML at /dashboard
  config/             YAML config schema
  httpresp/           Shared JSON response helpers
  services/           One package per emulated service
    storage/          GCS JSON + XML API
    pubsub/           REST + gRPC (Publisher + Subscriber)
    secretmanager/
    tasks/            HTTP dispatch
    scheduler/        cron-ish ticker
    kms/              AES-GCM
    logging/
    monitoring/
    firestore/        gRPC, no Java
    bigquery/         SQLite-backed
    bigtable/         gRPC stub (UNIMPLEMENTED)
    spanner/          gRPC stub (CreateSession works)
    cloudsql/         REST instance management
    memorystore/      miniredis (own port)
    cloudrun/         Used for both Cloud Run and Functions
tests/
  go/                 Integration tests, separate Go module
  python/             pytest + venv
  typescript/         vitest + pnpm
```

Service code keeps state through the `state.Store` interface, never raw disk.
Adding a service: implement `Name()`, `Register(*http.ServeMux)` (or
`RegisterGRPC(*grpc.Server)`), and `HandleV1/V2/V3` if it dispatches under
`/v1/projects/` style paths.

## Tests

```bash
# from tests/go
go test -race ./...

# from tests/python (one-time setup: python3 -m venv .venv && .venv/bin/pip install -r requirements.txt)
.venv/bin/python -m pytest

# from tests/typescript (one-time: pnpm install)
pnpm test
```

Go test infra builds the binary into a tmpdir per session and starts it on a
random free port for each test. Python and TS spin the binary up once per
session.

Tests are integration-only (no unit tests). Race detector should stay clean.

### Pre-push gate (run all five, no shortcuts)

Run the full gate locally before every `git push`. CI catches things, but
catching them locally is the whole point of having a three-language harness.

```bash
go vet ./...
gofmt -l internal/ cmd/   # must print nothing
(cd tests/go && go test -race -count=1 ./...)
(cd tests/python && .venv/bin/python -m pytest -q)
(cd tests/typescript && pnpm test)
```

If any step fails, fix it and re-run the whole gate. Don't push partial.
Don't trust CI to be the first signal — by the time CI fails, you've
already shipped the breakage to `main`.

## Conventions

- Real GCP SDKs are the compatibility target. When in doubt, run the real
  client library against the emulator and fix divergences.
- Match real Google REST and gRPC paths and JSON shapes. Don't invent custom
  surfaces unless the real service has no API at all (admin endpoints like
  `/admin/reset` are the exception).
- Errors return GCP-style `{"error":{"code":N,"message":"..."}}` JSON for REST
  and proper gRPC status codes for gRPC.
- Keep files under ~500 lines. Split when they grow.
- No new deps without a clear need. Pure Go preferred (avoid CGo).

## Things that will bite you

- `STORAGE_EMULATOR_HOST` env var matters; the Go and Python GCS SDKs use
  both `/storage/v1/...` and root `/b/...` paths depending on configuration.
  The storage service registers both.
- Pub/Sub Go/Python/TS SDKs use gRPC when `PUBSUB_EMULATOR_HOST` is set, not
  REST. REST endpoints exist for completeness but are not what real clients
  hit by default.
- Cloud Run/Functions `invoke` only proxies; there is no process spawning.
  Tests pass a `backendUrl` to a `httptest.NewServer`.
- BigQuery uses SQLite, not DuckDB. SQL surface is small; tests stay in
  simple `SELECT/INSERT` territory.
- Memorystore is disabled by default because it grabs its own port. Enable
  it explicitly in config when testing redis-using code.
- Daemon mode is the default for `gcp-local start`. CI scripts should pass
  `--no-daemon` (the test harness does).

## What's not done

See README.md "Divergences from the design spec". Short version: CloudSQL
(no DB engine), Bigtable / Spanner (stubs), Cloud Run / Functions (no
subprocess execution), Dataflow / Vertex AI / AlloyDB (not started).
BigQuery uses SQLite instead of DuckDB.

## Don't

- Don't commit secrets or `.env` files.
- Don't add CGo dependencies; we run on macOS and Linux dev machines and
  want `go install` to work without a compiler toolchain beyond Go itself.
- Don't add new tests to the root or `cmd/` packages — they live under
  `tests/<lang>/`.
- Don't change real GCP API paths to be "more convenient". The point of the
  project is wire compatibility.
