---
id: 20261126-214345-ci-guardrails-mechanical-checks-over-written
title: "CI guardrails: mechanical checks over written rules"
status: accepted
date: 2026-11-26
branch: chore/ci-guardrails
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/ci/**
  - .sg/**
  - .golangci-new.yml
  - .github/workflows/ci.yml
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: []
tags: [process, ci]
---

## Context

The factory has no human gate (ADR 20261126-211742). Most rules in
`AGENTS.md` were prose, and CI did not run golangci-lint, so an agent
that skipped the local gate could merge lint errors. Agents follow a
failing check far more reliably than a written rule, and when they
cannot fix a check they tend to silence it.

## Decision

1. Every rule that a machine can check becomes a required CI check:
   `Lint` (golangci-lint, a stricter config for changed lines only, and
   ast-grep rules), `Policy` and `Coverage`, plus `-shuffle=on` for Go
   tests.
2. `scripts/ci/policy.sh` checks the PR diff against the merge base.
   CI runs the copy from `main`, so a PR cannot weaken its own judge.
3. Escape hatches are ratchets: the count of `//nolint`,
   `ast-grep-ignore`, skipped tests and lint exclusions may not grow.
   Guardrail files change only in PRs that change no code.
4. Old code is not held to the new rules. The strict lint config uses
   `new-from-merge-base`, big files may not grow, and the 8 existing
   fixed sleeps in tests carry `ast-grep-ignore` markers.
5. Coverage compares each package with the merge base in the same CI
   run (integration coverage from a `-cover` build of the binary), so no
   baseline file needs upkeep.
6. `scripts/ci/gate.sh` runs the same checks locally in one command.

## Alternatives considered

- **More prose in AGENTS.md.** Agents skip it under pressure; this is
  the failure the decision fixes.
- **A coverage baseline file.** Somebody must update it, and an agent
  would update it to pass. Comparing with the base in the same run needs
  no state.
- **Mutation testing.** It proves tests fail without the change, but no
  pure-Go mutation tool runs integration tests against a subprocess
  binary at a useful speed.

## Consequences

- CI time grows by about 2 minutes (coverage runs the Go suite twice).
- Refactors that touch services without tests pass only with a
  `refactor` PR title, and the coverage ratchet still guards them.
- Large features need several PRs because of the 1500-line diff limit.
- An agent can still change a guardrail in a guardrail-only PR. With no
  human gate, the review stage is the only check on such PRs.

