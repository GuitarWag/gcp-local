---
id: 20261126-221104-holdout-acceptance-scenarios-between-review-and
title: Holdout acceptance scenarios between review and merge
status: accepted
date: 2026-11-26
branch: feat/holdout-accept
worktree: gcp-local
session: 31a3fa98-8ee7-48ed-8ce2-c871fd67d015
affects:
  - .agents/skills/factory/**
  - scripts/factory/orchestrator.sh
  - docs/factory.md
supersedes: []
superseded_by: []
depends_on: [20261126-211742-autonomous-agent-factory-label-state-machine]
journal_refs: []
tags: [process, factory]
---

## Context

In the factory (ADR 20261126-211742), the build team writes the code and
the tests that check it, from the same spec. CI and the review confirm
that those tests pass and look sound. If the builders misread the spec,
code and tests agree and are both wrong, and no check sees it. The dark
factory pattern answers this with holdout scenarios: acceptance checks
that the coding agent never sees, run by a separate evaluator.

## Decision

1. The spec agent writes 3 to 8 plain-English scenarios for each issue
   in the private repo `GuitarWag/gcp-local-holdout`. The public spec
   gives only the count.
2. A new stage, `stage:accept`, runs between `stage:review` and
   `stage:merge`. The accept agent writes a real-SDK program for each
   scenario, runs it 3 times against the PR code, and passes a scenario
   on 2 of 3 runs. The gate needs every P0 scenario and 90% overall.
3. A failure goes to `stage:fix` as one line for each failed scenario.
   The full report stays in the holdout repo. 3 accept failures flag the
   issue.
4. Only the spec and accept prompts name the holdout clone. Each of those
   jobs gets its own clone, deleted when the job ends. `.gitignore`
   blocks the evaluator's `zz_holdout_` programs.
5. For issues specified before this stage, the accept agent writes the
   scenarios from the issue and spec before it reads the PR.

## Alternatives considered

- **An LLM that plans HTTP calls and judges the responses.** The real
  GCP SDKs are a better oracle for this project than an LLM judge, so
  the evaluator writes SDK programs and the assertions decide.
- **Scenarios in the public repo.** Every agent reads the repo, so the
  builders would code to the checks.
- **A second GitHub identity for spec and accept.** It gives real access
  control, but the owner chose one identity for all agents.

## Consequences

- Each feature costs one more agent run, plus up to 3 more fix rounds.
- The wall is a convention: a build agent that searches the owner's
  repos can find the holdout repo. A second identity would close this.
- The spec agent writes both the spec and the scenarios, so a wrong spec
  still gives wrong scenarios. The holdout catches builder errors, not
  spec errors; the verify stage and the review cover the spec.

