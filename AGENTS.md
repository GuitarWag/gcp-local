# Agent guidance for gcp-local

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
    bigtable/         gRPC data path on state.Store
    spanner/          gRPC data path, SQLite per database
    cloudsql/         REST instance management
    memorystore/      miniredis (own port)
    cloudrun/         Used for both Cloud Run and Functions
    metadata/         GCE-shaped metadata server
    iamcredentials/   generateAccessToken / generateIdToken
    iamadmin/         service accounts CRUD + keys
    serviceusage/     service enable state + batchEnable operations
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

### Pre-push gate

Run the gate before every `git push`. It runs every check that CI runs
and lists all failures at the end:

```bash
scripts/ci/gate.sh            # vet, gofmt, golangci-lint (twice), ast-grep, policy, 3 test suites
scripts/ci/gate.sh --cover    # also the coverage ratchet
```

At most 2 gate runs happen at the same time on one machine
(`GATE_SLOTS`). A third run waits for a free slot.

| Check | Config | What it blocks |
|---|---|---|
| golangci-lint | `.golangci.yml` | Bugs and unsafe code, in all code. |
| golangci-lint (new code) | `.golangci-new.yml` | Copied code, long or complex functions, repeated literals and TODOs, in lines changed since `origin/main`. |
| ast-grep | `.sg/rules/` | `http.Error` and disk writes in services, `panic` and `fmt.Print` in `internal/`, `time.Sleep` outside a loop in tests. |
| policy | `scripts/ci/policy.sh` | Files over 500 lines, `CHANGELOG.md` edits, a missing fragment, a new dependency without an ADR, tests outside `tests/`, service changes without tests, more `//nolint` or skipped tests, guardrail changes mixed with code, diffs over 1500 lines. |
| coverage | `scripts/ci/coverage.sh` | A drop of more than 2 points in any package, and new service packages under 60%. |
| go test | `-race -shuffle=on` | Data races and tests that depend on run order. |

CI runs the copy of `policy.sh` from `main`, not the copy in the PR.

When a check fails, fix the cause. Do not add `//nolint`, `ast-grep-ignore`,
a skipped test or a lint exclusion: the policy check blocks each of them.
A PR that changes a guardrail file (`.golangci*.yml`, `sgconfig.yml`,
`.sg/`, `scripts/ci/`, `.github/workflows/`) must not change code.

Install once: golangci-lint v2, ast-grep
(`npm install -g @ast-grep/cli`), the Python venv and `pnpm install`
(see Tests).

## Slop

Reviews reject these patterns:

- A new helper when one exists. Look first in `internal/httpresp`
  (JSON and GCP errors), `internal/state` (all storage), `internal/health`,
  `internal/iam`, and `tests/go/testutil` (start the emulator, clients).
- Comments that repeat the code. Comment only the reason for a decision.
- Config options, flags, interfaces or wrappers that no caller needs now.
- Tests that pass without the change, or that check only status codes
  when the real SDK checks the body.
- Error messages or JSON shapes that differ from the real API. Copy them
  from the discovery document or the protos.

`cd tests/go && go run ./apicoverage` reports which real API methods the
emulator serves.

## CHANGELOG and releases

Every PR adds one changelog fragment. No PR edits `CHANGELOG.md` or
picks a version. The release manager agent (see `docs/factory.md`) cuts
releases. This rule prevents version and merge collisions between
parallel PRs.

### 1. Add a fragment

Create `changelog.d/<issue>-<slug>.md`. The first line is the section
(`Added`, `Changed`, `Fixed`, `Removed`, `Deprecated` or `Security`) and
the bump (`minor` or `patch`). The rest is the entry:

```markdown
Added minor
- **Gemini API emulator.** Prose paragraph about the user-visible
  behaviour change, not the internal refactor. Closes #38.
```

Pre-1.0 semver: `minor` for new user-visible features or behaviour
changes, `patch` for fixes, docs and refactors.

### 2. Release (release manager only)

`scripts/factory/release.sh` collects all fragments on `main`:

1. It picks the highest bump among the fragments.
2. It writes them into `CHANGELOG.md` as `## [X.Y.Z] - YYYY-MM-DD` under an
   empty `## [Unreleased]`, updates the link footnotes, and deletes the
   fragments.
3. It opens a `release/vX.Y.Z` PR. The release manager merges it after CI
   passes. The merge to `main` triggers `.github/workflows/auto-tag.yml`, which
   reads the topmost semver section from `CHANGELOG.md`, pushes
   `vX.Y.Z` if it doesn't already exist, and runs goreleaser. Binaries
   for darwin/linux × amd64/arm64 + a GitHub Release land within a
   couple of minutes. No manual `git tag` step is needed.

   `release.yml` is still wired to `push: tags: v*` as a manual escape
   hatch — if you ever push a tag by hand, it'll fire goreleaser the
   same way. The two don't double-trigger because tags pushed via
   `GITHUB_TOKEN` (what `auto-tag.yml` uses) don't fire other workflows.

## Autonomous factory

Agents (pi, opencode, Claude Code, Codex) run this repo with no human in
the loop. The process is in `docs/factory.md`. The role instructions are
the `factory` skill in `.agents/skills/factory/`. `.claude/skills` is a
symlink to `.agents/skills`, so every agent reads the same skills.

To stop every factory loop, create the file `~/.factory/STOP`.

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

## Architecture decision records (adrlog)

- Any decision that changes architecture, engine choice, wire behaviour,
  process, or dependencies **must** be recorded with `adrlog new <title>`
  before the change lands in a PR. Records live in `docs/adr/`; the index
  (with mermaid graph) in `docs/adr/README.md` is generated — run
  `adrlog index` after adding or changing records, never edit it by hand.
- Before diverging from an `accepted` ADR, write a superseding record
  (`adrlog new ... --supersedes <id>`). The factory orchestrator won't spec
  an issue that contradicts an accepted ADR.
- Run `adrlog lint` on every PR touching `docs/adr/`.
- Quirk: adrlog resolves the repo's primary checkout — from a linked
  worktree it writes to the main working copy, so move generated files into
  your worktree before committing.

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

See README.md "Divergences from the design spec". Short version: Bigtable and
Spanner cover a subset (no ReadModifyWriteRow, no interleaved tables), Cloud Run / Functions (no container image support — only
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
