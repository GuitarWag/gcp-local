---
id: 20261127-121826-console-ui-initiative-role
title: Console UI initiative role
status: accepted
date: 2026-11-27
branch: feat/console-initiative
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/orchestrator.sh
  - .agents/skills/factory/roles/console.md
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: []
tags: [process, factory, console]
---

## Context

The owner wants the console UI (`/console`) to improve all the time. The
scout files only API and service issues, and it runs only when the
backlog is small. So console work starts only when a human files it.

## Decision

1. A new interval role, `console`, files 1 console issue per run, with
   the labels `console` and `stage:spec`. The issue then goes through
   the normal pipeline.
2. The orchestrator starts the role every hour
   (`FACTORY_CONSOLE_EVERY`), only when fewer than 2 console issues are
   open (`FACTORY_CONSOLE_WIP`). The open issue with the labels
   `initiative` and `console` does not count.
3. The owner steers the role through that initiative issue. Its body and
   the owner's comments set the priorities.
4. The role judges the console that runs: it starts the emulator and
   fetches the pages and the `/console/api/` endpoints.

## Alternatives considered

- **Extend the scout.** The scout runs once a day and only when the
  backlog is small, so console work would stop whenever feature work is
  busy.
- **A resident console agent.** It costs a context that stays open all
  day for one issue per hour. The owner can still add `console` to
  `FACTORY_RESIDENT`.

## Consequences

- Up to 2 console issues use the shared job slots with feature work.
- Console issues use Go tests in `tests/go/console_test.go` as their
  acceptance, and the holdout scenarios check page and API bodies.
