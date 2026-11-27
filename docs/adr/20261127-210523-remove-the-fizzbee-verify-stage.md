---
id: 20261127-210523-remove-the-fizzbee-verify-stage
title: Remove the FizzBee verify stage
status: accepted
date: 2026-11-27
branch: feat/remove-verify-stage
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/orchestrator.sh
  - .agents/skills/factory/SKILL.md
  - .agents/skills/factory/roles/spec.md
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: []
tags: [process, factory]
---

## Context

ADR 20261126-211742 item 4 put a FizzBee model in specs with concurrency
or retries, and a verify stage ran it. FizzBee used too much memory on
the factory Mac, which also runs up to 6 herdr teams. Most specs said
`model-check: skip`, so the stage cost one more agent run per issue for
little gain.

## Decision

1. Remove the `stage:verify` stage. The spec role advances an issue from
   `stage:spec` to `stage:plan`.
2. Remove the verify role, `scripts/factory/fizz-check.sh`, the
   `Model check` spec field and the FizzBee install step.
3. Issues that wait in `stage:verify` move to `stage:plan`.

This replaces item 4 of ADR 20261126-211742. The other items stay.

## Alternatives considered

- **Limit FizzBee memory or state space.** The agent writes the model,
  so the state space is hard to bound, and the stage still costs a run.
- **Run FizzBee only in review.** Review already runs the Go tests with
  `-race -shuffle=on`, which check the real code.

## Consequences

- Each issue needs one fewer agent run.
- Design faults in concurrent features (leases, retries, ordering) are
  found only in review, holdout acceptance and the race tests.
