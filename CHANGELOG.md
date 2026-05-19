# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Until 1.0.0, breaking changes may land in minor releases.

## [Unreleased]

## [0.8.0] - 2026-05-19

### Added

- **Metadata server + iamcredentials for ADC.** A new metadata service
  responds at `/computeMetadata/v1/...` with GCE-shaped endpoints for
  the default service account (`token`, `email`, `scopes`, `identity`,
  `aliases`) and the project (`project-id`, `numeric-project-id`).
  Pointing an SDK at the emulator with `GCE_METADATA_HOST=localhost:4443`
  is now enough for `google.DefaultTokenSource`-style ADC paths to
  resolve a token without a credentials file, matching how a process
  picks up an attached service account on GCE/Cloud Run. The companion
  `iamcredentials.googleapis.com` `generateAccessToken` /
  `generateIdToken` endpoints are wired into the same key material, so
  impersonation tests get a real RS256-signed JWT carrying the
  requested `audience` claim. The signing key is generated in-memory at
  start-up and published at `/computeMetadata/v1/jwks` for callers that
  want to verify signatures. Requests without the `Metadata-Flavor:
  Google` header are rejected with 403 so SDK probes that depend on
  that handshake see the right shape. Closes #29.

## [0.7.1] - 2026-05-19

### Changed

- **OTLP exporter honours `OTEL_EXPORTER_OTLP_PROTOCOL`.** The trace
  exporter introduced in 0.7.0 hardcoded HTTP/protobuf and silently
  ignored the standard protocol env var. It now selects between
  `http/protobuf` (default) and `grpc`, reading
  `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` first and falling back to
  `OTEL_EXPORTER_OTLP_PROTOCOL`. Any other value is rejected at
  start-up rather than being silently downgraded.
- **`otelhttp` bumped to v0.63.0** so the contrib HTTP and gRPC
  instrumentation modules sit on the same minor version, avoiding the
  TracerProvider lookup skew the 2-minor gap could introduce.

### Fixed

- **Tracer-provider init ordering.** `observability.Init` now installs
  the global text-map propagators only after the OTLP exporter and
  resource have been built successfully. The previous order left W3C
  propagators globally installed even when the exporter failed to start,
  which would have leaked partial state into any caller that kept the
  process alive on error.

### Notes

- Span names are still `METHOD PATH` and include resource ids embedded
  in GCP-style URLs (e.g. `GET /v1/projects/{p}/topics/{t}`). That keeps
  the dev experience simple but inflates service-map cardinality if you
  point the emulator at a long-lived trace backend. Path templating is
  tracked as a follow-up rather than a fix.

## [0.7.0] - 2026-05-18

### Added

- **OpenTelemetry tracing from the emulator.** Setting
  `OTEL_EXPORTER_OTLP_ENDPOINT` switches on an OTLP HTTP trace
  exporter; the gateway HTTP handler is wrapped with `otelhttp` and the
  shared `grpc.Server` registers an `otelgrpc` stats handler, so every
  REST and gRPC request gets a server span. The W3C tracecontext +
  baggage propagators are installed unconditionally, which means a
  `traceparent` header on the incoming request parents the emulator's
  span onto the caller's trace — a Go app that already traces its work
  sees app → emulator → state-store as one trace in Jaeger/Tempo. With
  the env var unset (the default), the global tracer provider stays a
  no-op, so the disabled path adds no exporter goroutine and no
  per-request allocation beyond a couple of interface calls. All
  standard `OTEL_*` env vars (`OTEL_SERVICE_NAME`,
  `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_HEADERS`,
  `OTEL_EXPORTER_OTLP_PROTOCOL`, …) are honoured by the SDK. README
  gains a "Tracing" section with a Jaeger all-in-one quickstart.
  Closes #30.

## [0.6.0] - 2026-05-16

### Added

- **Cloud Run / Cloud Functions: spawn a local binary on `invoke`.**
  Resources accept a `command` (path + args) and optional `env` map at
  create time. On the first `:invoke`, `gcp-local` allocates a free
  loopback port, execs the command with `PORT`, `K_SERVICE`, and the
  caller-supplied env, waits for the child to bind the port, then
  proxies the request to it. Subsequent invokes reuse the cached child
  handle; deleting the resource or shutting the emulator down terminates
  the child. The existing `backendUrl` proxy path is unchanged — if both
  are set, `command` wins. No container image support: bring your own
  binary. Closes #1.

## [0.5.1] - 2026-05-15

### Changed

- **Auto-tag releases when `CHANGELOG.md` graduates a new version.** New
  `.github/workflows/auto-tag.yml` runs on every push to `main` that
  touches `CHANGELOG.md`, reads the topmost `## [X.Y.Z] - YYYY-MM-DD`
  section, and if no matching `vX.Y.Z` tag exists yet, pushes the tag
  and runs goreleaser in the same job. Closes the gap that left 0.3.0
  and 0.4.0 unreleased (the CHANGELOG graduated them but nobody pushed
  the tag, so the existing tag-triggered `release.yml` never fired).
  `release.yml` stays as a manual escape hatch for tag pushes that
  bypass `CHANGELOG.md`. CLAUDE.md's release section is updated — step 3
  (manual `git tag && git push`) is no longer required.

## [0.5.0] - 2026-05-15

### Added

- **`scripts/seed.sh`: one-shot heavy seed for every emulated service.**
  60 log entries spread across 5 logs and 10 severities, 5 buckets with
  15 mixed-content objects, 5 Pub/Sub topics with 9 subscriptions and
  60 messages, 10 secrets with 1–3 versions each, 3 task queues with 6
  staggered tasks, 6 scheduler jobs (cron + `@every`), 3 KMS keyrings
  with 6 keys, 4 Cloud Monitoring time series with 5 points each, 2
  BigQuery datasets with 4 tables and 10 rows, 3 Cloud SQL Postgres
  instances on staggered ports, 4 Cloud Run services, 4 Cloud Functions,
  and ~69 Firestore documents across 5 collections plus a subcollection
  (the Firestore portion delegates to `scripts/seed_firestore/main.go`,
  a small Go program using the real Firestore client against the
  emulator's gRPC surface). Useful for clicking through `/console`
  without staring at empty lists. Bash 3.2 compatible (no associative
  arrays), works on stock macOS. The script is also exercised in CI:
  `TestSeedScriptDrivesAllConsoleEndpoints` runs it against a fresh
  emulator and asserts every `/console/api/*` endpoint returns the
  expected shape — drift detection so adding a new console page or
  renaming a `ConsoleX` adapter fails CI unless `seed.sh` is updated
  to match.
- **Console pages for the remaining 8 emulated services.** Cloud
  Monitoring (time-series table with metric type / resource / point
  count / last end-time), BigQuery (datasets + tables + an ad-hoc
  query box that runs against the SQLite backend), Cloud SQL
  (instances with engine / port / database / state), Cloud Run +
  Cloud Functions (resource list with backend URLs), Memorystore
  (host / port / live key count from miniredis), and stub status
  cards for Bigtable + Spanner explaining what's implemented vs.
  what returns `UNIMPLEMENTED`. All 16 emulated services are now on
  the sidebar; the overview table reflects health-registry state per
  service.
- **Console UI at `/console`.** A local clone of the Google Cloud
  console, scoped to what's useful while debugging. Eight per-service
  pages reached from a sidebar: Cloud Logging (live tail with severity
  / logName / timestamp filter, pause-on-hover), Cloud Storage (bucket
  → object → text-or-hex preview + XML PUT upload form), Pub/Sub (topic
  → subscription → peek messages without draining + publish test
  message), Firestore (collection → document → JSON field view), Secret
  Manager (secret → versions → reveal-on-click payload), Cloud Tasks
  (queue → tasks with scheduleTime + method + URL), Cloud Scheduler
  (jobs with next-fire computed from the cron expression), Cloud KMS
  (keyring → key → encrypt/decrypt round-trip form). Server-side HTML
  templates, vanilla `fetch` polling (1s for log tail, 2-3s elsewhere).
  Hand-rolled CSS with a terminal-brutalist aesthetic: IBM Plex Mono
  throughout, amber accent on near-black, sharp corners, ASCII section
  markers, status pills with colour per state, subtle scan-line and
  grid overlays. All static assets are bundled into the binary via
  `go:embed`; the only network call is the optional Google Fonts
  request from the browser, with a monospace system fallback if
  offline. The legacy flat-list dashboard at `/dashboard` is unchanged.
  Closes #17.
- **CloudSQL: real Postgres wire protocol backed by SQLite.** Each
  configured instance now spins up a TCP listener that speaks pgproto3
  (jackc/pgproto3) against an in-process pure-Go SQLite engine
  (modernc.org/sqlite), so `pgx`, `psycopg2`, and `node-postgres` connect
  and run real round-trips against the emulator. Simple and Extended query
  protocols are both implemented (Parse/Bind/Describe/Execute/Sync); `$N`
  placeholders translate to SQLite `?` with parameter reuse, a small
  dialect shim rewrites `SERIAL`/`BIGSERIAL` → `INTEGER PK AUTOINCREMENT`,
  `BYTEA` → `BLOB`, and strips `::type` casts; row OIDs on
  `RowDescription` are inferred from SQLite `DECLTYPE`. Per-instance YAML
  config (`engine`, `port`, `database`, `seed`) plus a top-level
  `base_port`; admin API responses carry the assigned `host` and `port`.
  Default engine is `sqlite`; `postgres` (pgembedded) is rejected at
  startup as a follow-up. Gateway shutdown closes listeners and SQLite
  handles. Integration tests cover CREATE / INSERT / SELECT / UPDATE /
  DELETE round-trips in Go, Python, and TypeScript. Closes #4.
- **golangci-lint v2 config and pre-push integration.** New
  `.golangci.yml` enables a curated linter set (bodyclose, copyloopvar,
  errcheck, errorlint, gocheckcompilerdirectives, gosec, govet,
  ineffassign, misspell, nilerr, nolintlint, revive, staticcheck,
  unconvert, unparam, unused, usestdlibvars + gofmt/goimports), with
  per-path suppressions documented inline for cases that are emulator
  behaviour by design (md5 as the GCS ETag hash, proxy pass-through in
  Cloud Run `invoke`, fake service-account JSON, local-config paths in
  daemon re-exec and TLS key writes). CLAUDE.md pre-push gate now runs
  `golangci-lint run ./...` alongside `go vet` and `gofmt`. Closes #19.
- **`gcloud` CLI setup against the emulator.** New `gcp-local gcloud-setup`
  and `gcp-local gcloud-teardown` subcommands print shell snippets
  (mirroring `gcp-local env`) that create a dedicated `gcp-local` gcloud
  configuration, disable credentials, and wire
  `api_endpoint_overrides/<service>` for every service the emulator
  supports — then switch back to the user's default configuration. New
  `docs/gcloud.md` walks through the eval-based workflow, manual fallback,
  and per-service example commands. README gains a short "Using with
  `gcloud`" section. Closes #16.

### Changed

- **Best-practice sweep across services.** Pubsub `Service` receivers
  unified to `s` across all 34 methods (staticcheck ST1016); shadowed
  `max` builtin removed from `inferParamOIDs` and `PullMessages`; dead
  state and unused mutex fields stripped from `kms`, `logging`, `storage`,
  `tasks`, `cloudrun`; dead helpers (`storage.readAll`,
  `pgwire.dollarToQuestion`) and unused parameters/separators removed;
  blank SQLite imports in `bigquery` and `cloudsql` now carry a
  justifying comment. No behaviour change.

### Fixed

- **Firestore `BatchWrite` dropped caller context.** `BatchWrite` was
  passing `context.Background()` to the internal `Commit`, so cancellation
  and deadlines from the caller were silently ignored. The caller's ctx
  is now forwarded.
- **`io.EOF` comparisons use `errors.Is`.** `pubsub.StreamingPull` and the
  `storage` multipart upload reader compared via `err == io.EOF`, which
  fails once the error is wrapped; both now use `errors.Is`.
- **Error propagation on deferred `Close` and PEM writes.** Deferred
  `Close()` calls in `bigquery`, `pgwire`, and `tlsx` no longer drop
  errors; `tlsx.writePEM` returns the close error via a named return so a
  half-written PEM surfaces instead of silently committing.
- **Logging timestamp parse + memorystore start errors wrap properly.**
  `%v` → `%w` in the logging timestamp error path; `memorystore` start
  failures now join both underlying errors with `errors.Join`.

## [0.2.0] - 2026-05-14

Nine closed issues, +28 integration tests, real pre-built binaries on the
Release page for the first time.

### Added

- **BigQuery: JOIN/GROUP BY/HAVING and scalar function translation.** The
  query translator no longer mangles qualified column refs (`alias.col`),
  numeric literals, or aggregate calls, so `JOIN ... ON`, `GROUP BY`,
  `HAVING`, `COUNT(*)`, `SUM/AVG/MIN/MAX`, and `ORDER BY` now work via the
  REST `queries` endpoint. A small set of BigQuery scalar functions is
  rewritten before hitting SQLite: `CURRENT_TIMESTAMP()` → `CURRENT_TIMESTAMP`,
  `SAFE_CAST(x AS T)` → `CAST(x AS T)` (no NULL-on-failure for v1),
  `CONCAT(a, b, ...)` → `(a || b || ...)`; `IFNULL` passes through. Not
  translated: window functions, `STRUCT`, `ARRAY`, `UNNEST`, `PARTITION
  BY`, `WITH RECURSIVE`, BigQuery-specific date arithmetic. Engine remains
  pure-Go SQLite (no DuckDB). Closes #7.
- **TLS: `--tls` flag + self-signed cert.** `gcp-local start --tls` now serves
  HTTPS (real HTTP/2 over TLS, no h2c) so SDK clients that hard-code
  `https://` work without `option.WithoutAuthentication()`. The cert and
  RSA-2048 key persist at `~/.gcp-local/tls/{cert,key}.pem` and are reused
  across restarts. Adds `gcp-local trust install` / `trust uninstall` for the
  macOS login keychain (Linux prints manual instructions; Windows not
  supported). Stdlib-only — no new dependencies. Closes #10.
- **Pub/Sub: push subscription delivery.** Subscriptions created with
  `pushConfig.pushEndpoint` now actually POST each message to the endpoint
  as `{"message": {...}, "subscription": "..."}`. 2xx responses ack the
  message; non-2xx and transport errors leave it in the queue for retry
  with exponential backoff capped at the subscription's ack deadline.
  Works for both REST and gRPC-created subscriptions and for subs seeded
  from config. Closes #14.
- **Cloud Logging: `ListLogEntries` filter parsing.** The `filter` field on
  list requests is now parsed and applied instead of silently ignored. Supports
  `severity` comparisons against standard Cloud Logging levels (DEFAULT…
  EMERGENCY), `logName`, `resource.type`, `resource.labels.<key>`, and
  `timestamp` (RFC3339) with `AND` between predicates. Unsupported syntax
  (OR, NOT, parens, regex, functions) returns 400 with a clear message
  rather than over-returning. Closes #13.
- **Cloud Tasks: scheduleTime + retry on 5xx.** Task HTTP dispatch now waits
  until `scheduleTime` before firing and retries non-2xx/transport failures
  with exponential backoff (base 100ms, cap 30s) up to 5 attempts; 2xx/3xx
  short-circuits the loop and 4xx is treated as non-retryable. `Stop()`
  cancels pending and in-flight retries via the existing `inflight`
  WaitGroup. Closes #15.
- **Firestore: Listen streaming RPC.** `DocumentRef.Snapshots()` and
  `Query.Snapshots()` from the real `cloud.google.com/go/firestore` SDK now
  work end-to-end. Listeners receive an initial state snapshot (`ADD` →
  `DocumentChange|DocumentDelete`* → `CURRENT` → `NO_CHANGE`), then live
  updates as docs are created, updated, or deleted. Where/OrderBy filters
  on query targets are ignored for now (the listener fires on any change
  in the collection, matching how `RunQuery` already behaves). Closes #5.
- **Storage: XML API write + chunked resumable uploads.** `PUT /{bucket}/{object}`
  now uploads via the XML path (used by the Python google-cloud-storage SDK
  in some configurations). The resumable upload handler now parses
  `Content-Range` headers and supports multi-chunk transfers, status-query
  pings (`bytes */<total>`), 308 Resume Incomplete intermediate responses,
  and the Node SDK's open-ended `bytes 0-*/*` single-chunk streaming form.
  Closes #8.
- **Release pipeline.** Tagging `v*` now triggers a GoReleaser workflow that
  builds binaries for macOS and Linux (amd64 + arm64), generates SHA-256
  checksums, and attaches everything to the GitHub Release. Closes #6.
- **`gcp-local version`** subcommand prints the build version, commit hash,
  and build date (populated via `-ldflags` at release time).
- **Scheduler: standard cron syntax.** Jobs now accept full 5-field cron
  expressions (`0 9 * * 1-5`) and `@every <duration>` shorthand via
  `robfig/cron/v3`. The legacy `every <duration>` form is still accepted
  for backwards compatibility — it's rewritten to `@every` internally.
  Closes #9.

## [0.1.0] - 2026-05-13

First public release.

### Added — milestone M1 (foundation)

- Single-binary CLI: `start`, `stop`, `status`, `env`, `reset`.
- Default daemon mode with PID file at `~/.gcp-local/gcp-local.pid`;
  `--no-daemon` keeps the process in the foreground.
- HTTP server with h2c so gRPC and REST share one port. Content-Type-based
  dispatch.
- `state.Store` interface; in-memory and BoltDB backends.
- Fake service-account JSON written to `~/.gcp-local/fake-creds.json`.
- `/healthz` reports per-service readiness.
- `/admin/reset` endpoint wipes all known namespaces.
- Cloud Storage: GCS JSON + XML APIs. Supports media, multipart, and
  resumable uploads. Computes MD5 + CRC32C so Node SDK upload verification
  passes. Real `cloud.google.com/go/storage`, `google-cloud-storage` (Py),
  `@google-cloud/storage` (TS) clients work via `STORAGE_EMULATOR_HOST`.
- Cloud Pub/Sub: REST endpoints for topics, subscriptions, publish, pull,
  ack. Full gRPC (`Publisher` + `Subscriber` incl `StreamingPull`) so the
  real Go/Python/TS Pub/Sub clients work via `PUBSUB_EMULATOR_HOST`.
- Storage `seed:` config: walks the seed directory and uploads every file
  on startup.

### Added — milestone M2

- Secret Manager: CRUD, version add, version access (`/v1/projects/{p}/secrets`).
- Cloud Tasks: queue + task CRUD at `/v2/projects/{p}/locations/{loc}/queues`.
  HTTP dispatch fires the task's `httpRequest.url`.
- Cloud Scheduler: cron-style HTTP jobs at `/v1/projects/{p}/locations/{loc}/jobs`.
  Schedule syntax accepts `every Ns/m/h` (subset of real cron).
- KMS: KeyRing + CryptoKey CRUD, AES-GCM `encrypt` / `decrypt` with
  in-process random key material.
- Refactored `/v1/projects/` and `/v2/projects/` into a central gateway
  dispatcher; services expose `HandleV1` / `HandleV2`.

### Added — milestone M3

- Cloud Logging: `WriteLogEntries`, `ListLogEntries` at `/v2/entries:write|list`.
- Cloud Monitoring: `CreateTimeSeries`, `ListTimeSeries` at
  `/v3/projects/{p}/timeSeries`.
- Firestore: gRPC implementation of `GetDocument`, `CreateDocument`,
  `UpdateDocument`, `DeleteDocument`, `ListDocuments`, `Commit`, `BatchWrite`,
  `BatchGetDocuments`, `RunQuery`, `BeginTransaction`, `Rollback`. Supports
  nested maps, arrays, integers, doubles, strings, booleans, nulls, bytes,
  timestamps. No `Listen` streaming. Works with real
  `cloud.google.com/go/firestore` and `google-cloud-firestore` clients.

### Added — milestone M4

- BigQuery: pure-Go SQLite backend (`modernc.org/sqlite`) instead of DuckDB.
  REST CRUD for datasets and tables, `insertAll`, and `jobs.query` /
  `queries`. Translates `dataset.table` references to SQLite table names.
- Bigtable: gRPC server with `UnimplementedBigtableServer`. Clients connect
  and receive proper `UNIMPLEMENTED` codes for data operations.
- Spanner: same UNIMPLEMENTED stub pattern, with `CreateSession` and
  `DeleteSession` implemented so client libraries can negotiate sessions
  before failing on data RPCs.

### Added — milestone M5

- Memorystore: embedded `miniredis` speaks the real Redis wire protocol on
  a configurable port (default 6379). Disabled by default.
- CloudSQL: REST instance-management stub at `/sql/v1beta4/projects/{p}/instances`.
  No DB engine; no Postgres/MySQL wire protocol.

### Added — milestone M6

- Embedded web dashboard at `/dashboard` (via `go:embed`). Polls
  `/dashboard/api/state` every 3 seconds and renders all known resources.

### Added — milestone M7

- Cloud Run: REST CRUD for services at `/v2/projects/{p}/locations/{loc}/services`.
  `invoke` action proxies requests to a configured `backendUrl`. No
  subprocess execution.
- Cloud Functions: same shape, at `/v2/projects/{p}/locations/{loc}/functions`.

### Fixed — hardening pass

- **Pub/Sub redelivery.** Pull no longer dequeues messages. Messages are
  marked in-flight with an ack deadline; `Acknowledge` consumes them,
  `ModifyAckDeadline` extends or nacks (sec=0). Ack-deadline expiry causes
  redelivery on the next pull. Closes the silent-message-loss path that
  existed when a consumer crashed between pull and ack.
- **BigQuery SQL identifier injection.** `datasetId`, `tableId`, and schema
  field names are validated against `^[A-Za-z0-9_\-]+$` before reaching
  SQLite DDL/DML interpolation. Hostile ids return 400 instead of executing.
- **Tasks HTTP body drain.** `dispatchHTTP` now drains and closes the
  response body so `http.Transport` keep-alive works. Goroutine wrapped in
  `defer recover`; `Stop()` waits for outstanding dispatches via WaitGroup.
- **Pub/Sub publish lock scope.** Per-subscriber appends now share one
  critical section instead of locking-and-releasing per message; prevents
  cross-publisher reordering under concurrent publish.
- **Firestore concurrent commits.** `Commit`, `CreateDocument`, and
  `UpdateDocument` now hold a service mutex across the read-modify-write
  cycle. Concurrent writes to the same doc no longer race.
- **Status code consistency.** DELETE on pubsub topics/subs, tasks
  queues/tasks, and secrets now returns 204 instead of 200. Secret DELETE
  cascades to versions.
- **Error propagation.** `tasks.go` and `storage.go` no longer return 200 OK
  when `store.Put` or `json.Marshal` fails — surfaces as 500 with detail.
- **Gateway `/admin/reset`** surfaces backend List/Delete errors instead of
  silently returning 204. `g.mem.Stop` now uses `shutdownCtx` (5s budget)
  instead of the already-cancelled parent ctx.
- **BigQuery query types.** Query response schema reports the original
  BigQuery type (INTEGER/FLOAT/BOOLEAN/STRING) sourced from the table
  schema, falling back to the SQLite column type. Previously every column
  was reported as STRING.
- **Encode error logging.** All service `writeJSON` helpers now go through
  `internal/httpresp`, which logs encode failures instead of silently
  dropping them.

### Testing

- 64 Go integration tests covering all working services, edge cases (empty
  objects, 2 MiB blobs, unicode names), concurrency (8 workers × 50 messages
  through Pub/Sub, 20 goroutines on the same Firestore doc), BoltDB
  persistence across restart for storage/secrets/pubsub topics/KMS keys,
  ack-deadline expiry and nack redelivery, SQL identifier injection guards,
  status-code regressions, `/admin/reset` end-to-end, `-race` clean.
- 18 Python tests covering Storage, Pub/Sub (REST + gRPC SDK), Firestore
  SDK, Secret Manager, KMS, Cloud Tasks, Logging, Monitoring, BigQuery,
  CloudSQL, Cloud Run, dashboard.
- 20 TypeScript tests covering the same surface via REST + Firestore /
  Pub/Sub SDK clients.

### Known gaps / divergences from `specs/001-foundation.md`

- BigQuery uses SQLite, not DuckDB.
- CloudSQL: no actual database engine (the spec called for pgembedded or a
  SQLite-with-Postgres shim).
- Bigtable + Spanner data paths return `UNIMPLEMENTED`.
- Cloud Run + Cloud Functions do not spawn subprocesses; they only proxy
  to a pre-configured backend URL.
- Dataflow, Vertex AI, AlloyDB, Cloud SQL MySQL: not implemented.
- Routing is URL-prefix based, not Host-header based as the spec describes
  (functionally equivalent for emulator use).
- Default state backend is `memory`, not `boltdb`.

[Unreleased]: https://github.com/GuitarWag/gcp-local/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.8.0
[0.7.1]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.7.1
[0.7.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.7.0
[0.6.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.6.0
[0.5.1]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.5.1
[0.5.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.5.0
[0.2.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.2.0
[0.1.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.1.0
