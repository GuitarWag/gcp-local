---
id: 20261126-233622-visible-factory-agents-herdr-runner-with
title: "Visible factory agents: herdr runner with resident janitor and scout"
status: accepted
date: 2026-11-26
branch: feat/resident-agents
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/**
  - .agents/skills/factory/roles/janitor.md
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: []
tags: [process, factory]
---

## Context

The factory ran stage jobs as headless processes (`pi -p`,
`opencode run`). On the first night, two jobs hung on a model request
that never got an answer. Nobody could see why, nobody could talk to
them, and they held their job slots. The owner wants every agent visible
in herdr and wants to chat with the long-running ones.

## Decision

1. `FACTORY_RUNNER=herdr` runs each stage job as an interactive agent in
   its own tab of a herdr workspace named `factory`. The tab opens with
   `--no-focus` and closes when the job no longer holds its claim.
2. The janitor and the scout become resident agents in tabs that stay
   open. The orchestrator types `RUN PASS` only when the agent is idle,
   so the owner can chat with them between passes.
3. Stage jobs stay one agent per task with a fresh context. Resident
   stage agents would carry context from one issue to the next and weaken
   the independent review and the holdout wall.
4. GitHub labels and claims stay the record of state. The herdr mailbox
   has no delivery guarantee and keeps messages for 7 days only.
5. The orchestrator stays a shell script, now visible in a herdr pane.
6. The watchdog compares the pane screen (without its spinner lines)
   between ticks for herdr jobs.
7. The factory pins the opencode model with `OPENCODE_CONFIG`, because a
   fresh interactive opencode picked a recently used model that had no
   valid credentials.
8. The default runner stays `headless` until the herdr runner has run
   for some time.

## Alternatives considered

- **Resident agents for every role.** Fewer tabs, but context from earlier
  issues leaks into later ones.
- **The herdr-shepherd mailbox as the state.** No delivery guarantee and
  a 7-day retention; a crash loses state.
- **Orca.** Deferred by the owner.

## Consequences

- The owner can read, steer or stop any agent in its tab.
- Team tabs still come from herdr-shepherd, which opens tabs in the
  focused workspace; the orchestrator switches focus for a moment. A
  `--workspace` / `--no-focus` option in herdr-shepherd would remove that.
- Team agents do not get the pinned opencode model.
- Typing in a stage job pane counts as an owner instruction.

