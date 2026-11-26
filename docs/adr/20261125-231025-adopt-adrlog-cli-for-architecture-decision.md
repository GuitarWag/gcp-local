---
id: 20261125-231025-adopt-adrlog-cli-for-architecture-decision
title: Adopt adrlog CLI for architecture decision records
status: accepted
date: 2026-11-25
worktree: t3-964b840f-37638e179031ef81
affects:
  - docs/**
  - CLAUDE.md
supersedes: []
superseded_by: []
depends_on: []
journal_refs: []
tags:
  - process
  - documentation
---

## Context

Decisions made by parallel agent sessions (factory orchestrator, builders,
reviewers, human) were previously recorded only in GitHub issues, PR
descriptions, and CLAUDE.md. Nothing captured *why* an approach was chosen
over alternatives in a place future sessions consult before changing course.

We already use the `adrlog` CLI (v0.1.2, installed at `~/.local/bin/adrlog`)
elsewhere; it generates timestamped markdown ADRs under `docs/adr/` with
front matter (`id`, `status`, `affects`, `supersedes`, `depends_on`, tags)
and regenerates `docs/adr/README.md` — including a mermaid decision graph —
via `adrlog index`.

## Decision

Adopt the adrlog CLI as the mandatory ADR system for this repository,
effective immediately:

1. **Any decision** that changes architecture, engine choice, wire behaviour,
   process, or dependencies **must** be recorded with `adrlog new` before the
   change lands in a PR.
2. Records go in `docs/adr/` (generated), indexed in `docs/adr/README.md`.
   Never edit the README by hand; run `adrlog index` after adding or changing
   records.
3. **Status lifecycle**: `proposed` → `accepted` on merge of the first PR
   that relies on the decision; `superseded` via `--supersedes` when replaced.
   The factory orchestrator refuses to spec an issue that contradicts an
   `accepted` ADR without a superseding record.
4. **Every factory builder prompt** must include: "Check docs/adr/README.md
   for accepted decisions that constrain your task; if your approach would
   supersede one, stop and flag with `factory:flagged`."
5. `adrlog lint` must be clean on every PR that touches `docs/adr/` (added to
   the pre-push gate for those PRs; not part of CI yet).

## Alternatives considered

- **Plain hand-written ADRs** (`docs/adr/NNNN-*.md`, no tooling): zero new
  tooling, but the index and mermaid graph go stale immediately under
  parallel agent sessions, and there's no linting or session journal — which
  is the exact problem adrlog was built for.
- **GitHub Discussions / issues as decision log**: decisions get scattered
  across threads, are not greppable from a worktree, and can't be linked into
  the factory pipeline mechanically.

## Consequences

- All future sessions (human and agent) must check `docs/adr/README.md`
  before diverging from an accepted decision, and record new ones via the
  CLI.
- `adrlog` is developer-local tooling (not a Go dependency, not shipped in
  the binary); CI does not install it yet. If a builder lacks the binary,
  hand-write the record in the same front-matter format and flag it.
- Known quirk: adrlog resolves the repo's primary checkout — when run from a
  linked worktree it writes to the main working copy, so move generated files
  into the worktree before committing (fixed upstream in v0.1.2+; verify
  version before relying on it).
