---
id: 20261126-173401-gemini-emulator-config-driven-fixture-engine
title: "Gemini emulator: config-driven fixture engine, no inference"
status: proposed
date: 2026-11-26
branch: feat/gemini-api
worktree: feat-gemini-api-f203e8eed9a687de
affects:
  - internal/services/gemini/**
supersedes: []
superseded_by: []
depends_on: []
journal_refs: []
tags: [gemini, engine]
---

## Context

Issue #38 adds the Gemini API (`generativelanguage.googleapis.com`) to the
emulator. A real generation backend would need model weights, an inference
runtime, or a network dependency — all of them break the project's rules:
one pure-Go binary, no Docker, no Java, no network calls at runtime.

## Decision

The Gemini service is a deterministic fixture engine driven entirely by
config:

1. `services.gemini.models` entries define every model id plus its fixture
   `reply`. `generateContent` returns the reply verbatim;
   `streamGenerateContent` splits it into N `chunks` of SSE frames.
2. `countTokens` returns an optional configured value or the whitespace
   word count of the reply. `usageMetadata` token counts derive the same
   way, so tests stay deterministic.
3. `failFirst` rules make the first N calls per model fail with a
   Gemini-shaped error body; 429 responses carry a `google.rpc.RetryInfo`
   detail with the configured `retryDelay`.
4. The service holds no state. It needs no `state.Store` namespace, no
   `/admin/reset` entry, and no persistence. Enabled when at least one
   model is configured.
5. The API is mounted on the root paths `/v1beta/models/*` plus `v1` and
   `v1alpha` (the real API serves all three with identical model paths),
   not under `/v1/projects/` — matching the real service's wire layout.

## Alternatives considered

- **Proxy to the real Gemini API.** Requires network access and a real API
  key; breaks offline use and deterministic tests.
- **Local small model (llama.cpp or similar).** CGo or a separate runtime;
  slow, heavy, and nondeterministic — the opposite of what emulator tests
  need.
- **Embed an inference engine in pure Go.** No mature pure-Go inference
  stack; a from-scratch one is out of scope for an emulator.

## Consequences

- Tests that need fixed replies, ordered streaming chunks, or retry-on-429
  behaviour get them deterministically.
- No real understanding: prompts never change the reply (documented
  non-goal in the issue and README).
- Wire shapes follow the real API, so the real `google-genai` SDK and raw
  REST clients work unchanged.
