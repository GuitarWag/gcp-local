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

### Pre-push gate (run all six, no shortcuts)

Run the full gate locally before every `git push`. CI catches things, but
catching them locally is the whole point of having a three-language harness.

```bash
go vet ./...
gofmt -l internal/ cmd/                # must print nothing
golangci-lint run ./...                # config in .golangci.yml
(cd tests/go && go test -race -count=1 ./...)
(cd tests/python && .venv/bin/python -m pytest -q)
(cd tests/typescript && pnpm test)
```

Install golangci-lint v2 once:
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`

If any step fails, fix it and re-run the whole gate. Don't push partial.
Don't trust CI to be the first signal — by the time CI fails, you've
already shipped the breakage to `main`.

## CHANGELOG and releases (required on every PR)

Every PR must update `CHANGELOG.md` **and** cut a new release. No
exceptions — docs, refactors, tiny fixes included. The CHANGELOG drives
the release notes; the release ships the binaries users actually
download.

### 1. Update `CHANGELOG.md`

Append an entry under `## [Unreleased]` in the same PR. Use Keep a
Changelog sections (`### Added` / `### Changed` / `### Fixed` /
`### Removed` / `### Deprecated` / `### Security`). Mirror the existing
style: bold lead, prose paragraph, `Closes #N` at the end. Be specific
about the user-visible behaviour change, not the internal refactor.

### 2. Cut the release in the same PR

Graduate `[Unreleased]` to a new versioned heading before merge:

1. Pick the next version using semver (pre-1.0: minor bump for new
   user-visible features or behaviour changes, patch bump for fixes and
   docs).
2. In `CHANGELOG.md`: rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`,
   re-add an empty `## [Unreleased]` above it, and update the link
   footnotes at the bottom (new `[X.Y.Z]:` line + bump `[Unreleased]:` to
   `compare/vX.Y.Z...HEAD`).
3. The merge to `main` triggers `.github/workflows/auto-tag.yml`, which
   reads the topmost semver section from `CHANGELOG.md`, pushes
   `vX.Y.Z` if it doesn't already exist, and runs goreleaser. Binaries
   for darwin/linux × amd64/arm64 + a GitHub Release land within a
   couple of minutes. No manual `git tag` step is needed.

   `release.yml` is still wired to `push: tags: v*` as a manual escape
   hatch — if you ever push a tag by hand, it'll fire goreleaser the
   same way. The two don't double-trigger because tags pushed via
   `GITHUB_TOKEN` (what `auto-tag.yml` uses) don't fire other workflows.

The agent opening the PR is responsible for the CHANGELOG entry and the
version bump in the same diff. If you're unsure about the version bump,
propose one in the PR description and let the maintainer correct it
before merge.

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
- Cloud Run/Functions `invoke` either proxies to a configured `backendUrl`
  or spawns a local executable (`command:` on the resource, PORT + K_SERVICE
  env, child handle cached and killed on delete / shutdown). No container
  image support. Tests can pass either `backendUrl` (httptest server) or a
  `command` pointing at a built fixture binary.
- BigQuery uses SQLite, not DuckDB. SQL surface is small; tests stay in
  simple `SELECT/INSERT` territory.
- Memorystore is disabled by default because it grabs its own port. Enable
  it explicitly in config when testing redis-using code.
- Daemon mode is the default for `gcp-local start`. CI scripts should pass
  `--no-daemon` (the test harness does).

## What's not done

See README.md "Divergences from the design spec". Short version: Bigtable /
Spanner (stubs), Cloud Run / Functions (no container image support — only
exec a local binary or proxy to a URL), Dataflow / Vertex AI / AlloyDB
(not started). BigQuery uses SQLite instead of DuckDB.

## Don't

- Don't commit secrets or `.env` files.
- Don't add CGo dependencies; we run on macOS and Linux dev machines and
  want `go install` to work without a compiler toolchain beyond Go itself.
- Don't add new tests to the root or `cmd/` packages — they live under
  `tests/<lang>/`.
- Don't change real GCP API paths to be "more convenient". The point of the
  project is wire compatibility.
