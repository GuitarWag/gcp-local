# gcp-local Software Factory

Autonomous pipeline that turns labeled GitHub issues into reviewed, gated PRs.
Humans merge; the factory does the rest. Owner of this document: the
project's PM/PO thread (T3 Code). Any process change lands here first.

## Pipeline

```
GitHub issue labeled factory:ready
  → Orchestrator (hourly scheduled task) claims it: factory:ready → factory:claimed
  → Orchestrator writes a spec comment on the issue (acceptance + test plan)
  → Builder agent (fresh thread, own worktree):
      1. Write failing integration tests in tests/go/ (+python/ts when the
         surface is SDK-visible) using the REAL GCP SDK clients.
      2. Implement in internal/services/<service>/ following repo conventions.
      3. Run the full pre-push gate (below). Fix until green.
      4. CHANGELOG entry + version bump per docs/factory.md §release.
      5. Open PR, link it to the issue, watch it.
  → Reviewer agent (fresh thread, no shared context): reviews diff vs spec,
    repo conventions, and wire compatibility. Objections → back to Builder.
  → CI green + reviewer approval → PR awaits the human merge.
  → On merge: auto-tag.yml cuts the release. Orchestrator closes loop:
      factory:claimed → factory:done on the issue.
```

## Roles

| Role | Runs when | Rules of engagement |
|------|-----------|---------------------|
| **Orchestrator** | hourly scheduled task | Picks ONE `factory:ready` issue (lowest number first). Never two builders on overlapping packages. Adds `factory:claimed`. Writes spec comment. Launches Builder. On blocked builder → `factory:flagged` + comment. |
| **Builder** | one per feature | Full ownership of one worktree. Must end with: green gate + PR opened + issue commented. If stuck > 3 attempts on the same failure → stop, flag, report state honestly. |
| **Reviewer** | after each Builder PR | Fresh thread. Verdict: approve / request changes. Reviews for wire compatibility with real GCP API shapes, test quality, file size (<500 lines), no scope creep. |
| **Human (Wagner)** | merge gate | Merges, or comments on the PR; orchestrator relays feedback to Builder. |

## Hard rules (non-negotiable, from CLAUDE.md)

1. **Never push to `main`.** All work via PRs from factory branches.
2. **Pre-push gate before every push** — all six steps, no shortcuts:
   ```bash
   go vet ./...
   gofmt -l internal/ cmd/                # must print nothing
   golangci-lint run ./...
   (cd tests/go && go test -race -count=1 ./...)
   (cd tests/python && .venv/bin/python -m pytest -q)
   (cd tests/typescript && pnpm test)
   ```
3. **Every PR updates CHANGELOG.md and cuts a release** (graduate
   `[Unreleased]` → `[X.Y.Z] - YYYY-MM-DD`, re-add empty `[Unreleased]`,
   bump the link footnotes). Semver: pre-1.0 minor bump for user-visible
   features/behaviour, patch for fixes/docs.
4. **No new dependencies** without a clear need; pure Go only (no CGo).
   A builder that wants a new dep STOPS and flags via `factory:flagged`.
5. **Tests live in `tests/<lang>/` only.** Never in root or `cmd/`.
6. **Wire compatibility is the spec.** Match real GCP REST paths and JSON
   shapes and gRPC status codes. When in doubt, run the real client library
   against the emulator and make it pass. Never invent custom surfaces
   (admin endpoints excepted).
7. **Files under ~500 lines.** Split when they grow.
8. **State through `state.Store`, never raw disk.**

## Budgets

- Builder: max ~90 minutes wall / 3 retry loops per failure class.
- PRs in flight: max 2 concurrent builders.
- Rework: max 2 review rounds per PR, then `factory:flagged` + human.

## Metrics (reported weekly by the orchestrator)

- Issues closed via factory
- First-pass review rate / rework loops per PR
- Test count delta per language
- CI pass rate, time-to-green, human merge latency

## Issue labels

| Label | Meaning |
|-------|---------|
| `factory:ready` | Spec'd and ready for a builder to claim |
| `factory:claimed` | A builder owns it right now |
| `factory:done` | Closed via merged factory PR |
| `factory:flagged` | Blocked; needs a human decision (see issue comment) |

## Backlog order (as green-lit by the owner, 2026-11)

1. **#3** spanner data path (SQLite-backed, transpile like BigQuery)
2. **#2** bigtable data path (wide-column model + filters on SQLite)
3. **Gemini API emulator** (new issue to be filed; `generativelanguage` surface)
4. **#26** cloudsql opt-in `postgres` engine (cached binaries, no CGo)
5. **Eventarc + Workflows** (new issues to be filed)
6. **Resource Manager + Service Usage** (new issues to be filed)
7. **Terraform `google` provider compat** as a tested surface
8. Breadth: Cloud Build, Artifact Registry, AlloyDB, Dataflow stub, Vertex AI
