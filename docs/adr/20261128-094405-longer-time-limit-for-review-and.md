---
id: 20261128-094405-longer-time-limit-for-review-and
title: Longer time limit for review and accept, build queue limit
status: accepted
date: 2026-11-28
branch: feat/factory-speed
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/orchestrator.sh
  - scripts/factory/herdr.sh
  - scripts/factory/claim.sh
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: [31a3fa98-8ee7-48ed-8ce2-c871fd67d015#129, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#128, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#127, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#126, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#125, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#124, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#123, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#122, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#121, 31a3fa98-8ee7-48ed-8ce2-c871fd67d015#120]
tags: [process, factory]
---

## Context

From 2026-11-27 18:00 to 2026-11-28 09:30, 42 stage jobs finished and the
watchdog stopped 27. 22 of the 27 reached the 90-minute time limit, and
many of them still changed their screen in the last minute. Each stop
counted as a failed attempt. After 3 failed attempts the janitor closed
#38 (Gemini) and #55 (README), and their PRs stayed open.

Also, 13 issues waited in build while only 6 teams can run. Spec and plan
jobs took job slots that review and accept needed.

## Decision

1. Review and accept jobs get `FACTORY_JOB_MAX_MIN_LONG` minutes
   (default 150). The other roles keep `FACTORY_JOB_MAX_MIN` (90).
2. When a herdr job reaches its time limit and its screen still changes,
   the orchestrator ends the job without a failed attempt
   (`claim.sh release`). A job whose screen does not change for
   `FACTORY_STALL_MIN` minutes still counts as a failed attempt.
3. The orchestrator starts no spec or plan job while `FACTORY_BUILD_WIP`
   (default 8) or more issues are in `stage:build` or `stage:fix`.
4. A team start uses a local branch that has no remote branch, so a
   closed team's commits that it did not push are kept.

## Alternatives considered

- **One longer limit for all roles.** A spec or plan job that loops for
  150 minutes holds a slot for too long.
- **No time limit for active jobs.** A job that loops with a changing
  screen then holds its slot for good.

## Consequences

- A job that always reaches the time limit retries with no end. The
  janitor sees this in the issue comments.
- New issues wait in spec while the build queue is long.
