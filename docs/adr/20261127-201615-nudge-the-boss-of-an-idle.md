---
id: 20261127-201615-nudge-the-boss-of-an-idle
title: Nudge the boss of an idle factory team
status: accepted
date: 2026-11-27
branch: feat/team-idle-nudge
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/herdr.sh
  - scripts/factory/orchestrator.sh
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-233622-visible-factory-agents-herdr-runner-with]
journal_refs: []
tags: [process, factory, herdr]
---

## Context

Build teams stopped for more than an hour with no agent at work. The
f40 boss waited for a push that needed a human. The f41 boss waited for
a report from a worker that had already finished. The orchestrator saw
only the claim, so it did nothing until the claim expired.

Only 2 teams could run at the same time, so a fix for a reviewed PR
waited behind two slow build teams.

## Decision

1. On each tick, the orchestrator checks every team that is still in
   build or fix. When no agent of the team is `working` for 10 minutes
   (`FACTORY_TEAM_IDLE`), it sends the boss a message: check the worker
   reports, send missed tasks again, or run the gate, push and open the
   PR. It sends the message again every 10 minutes while the team stays
   idle.
2. Up to 6 teams run at the same time (`FACTORY_MAX_TEAMS`, was 2).

## Alternatives considered

- **Close and restart an idle team.** The new team loses the context of
  the old team, and the work in progress can be lost.
- **A cron job outside the orchestrator.** The orchestrator already
  ticks every 5 minutes and knows the team list.

## Consequences

- An idle team costs at most about 15 minutes instead of a full claim
  lease.
- 6 teams can use up to 24 agents. The machine and the model quota must
  carry that load.
