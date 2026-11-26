---
id: 20261126-211742-autonomous-agent-factory-label-state-machine
title: "Autonomous agent factory: label state machine, no human gate"
status: accepted
date: 2026-11-26
branch: chore/agent-factory
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - scripts/factory/**
  - .agents/skills/factory/**
  - docs/factory.md
  - changelog.d/**
supersedes: []
superseded_by: []
depends_on: [20261125-231025-adopt-adrlog-cli-for-architecture-decision]
journal_refs: []
tags: [process, factory]
---

## Context

The first factory ran in T3 Code with one hourly orchestrator thread, a
human merge, and a release cut inside every PR. The owner moved the work
to the terminal (Ghostty + herdr) with pi and opencode, and removed the
human from every step. The old design had three defects: claims had no
expiry (four issues stayed claimed after T3 stopped), the reviewer
verdict lived only in the agent thread, and parallel PRs that each cut a
release picked the same version.

## Decision

1. GitHub is the only state. Each open feature issue has one `stage:*`
   label: spec, verify, plan, build, review, fix, merge. Agents find each
   other's output through HTML comment markers.
2. A claim is a comment with an expiry plus `factory:claimed`. The oldest
   valid claim wins a race. Expired claims count as failures; 3 failures
   at one stage flag the issue.
3. `scripts/factory/orchestrator.sh` is a shell loop with no LLM. It
   starts ephemeral `pi -p` / `opencode run` jobs for spec, verify, plan,
   review, janitor and scout, and herdr-shepherd teams for build and fix.
4. Specs for features with concurrency or retries carry a FizzBee model.
   The verify stage runs it; a counterexample sends the spec back.
5. No human gate. Agents use the owner's GitHub identity. GitHub forbids
   self-approval, so the review verdict is a marked PR comment.
   `release.sh` merges green PRs; branch protection with the 4 CI checks,
   applied to admins, is the only merge gate.
6. PRs add `changelog.d/` fragments. Only the release script edits
   `CHANGELOG.md` and picks the version, through a `release/vX.Y.Z` PR.
   `auto-tag.yml` is unchanged.
7. Agents act only on text by the owner account, because the repo is
   public.

## Alternatives considered

- **A GitHub Projects board as state.** It needs GraphQL for every
  change and gives no more than labels for a single-repo pipeline.
- **A local queue or database.** It hides state from GitHub, so a second
  machine or a restarted session cannot see it.
- **An LLM orchestrator.** Picking work from labels is deterministic; an
  agent adds cost and nondeterminism with no gain.
- **A bot account for approvals.** The owner chose one identity for all
  agents.

## Consequences

- Any number of agents on any machine can take part, because the claim
  protocol and the labels are the only coordination.
- A bad merge reaches `main` and a release with no human check. CI and
  the review stage are the only defenses.
- FizzBee checks the design model, not the Go code. A model that differs
  from the implementation gives false confidence; the verify role checks
  that the model covers the spec.
- Every PR touches `changelog.d/` with a unique file name, so changelog
  conflicts stop.

