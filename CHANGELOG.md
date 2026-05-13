# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Until 1.0.0, breaking changes may land in minor releases.

## [Unreleased]

### Added

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

[Unreleased]: https://github.com/GuitarWag/gcp-local/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/GuitarWag/gcp-local/releases/tag/v0.1.0
