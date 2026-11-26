Changed patch
- **CI guardrails for autonomous agents.** CI now runs golangci-lint,
  a stricter golangci-lint config for changed lines only (copied code,
  long or complex functions, TODOs), ast-grep rules for repo-specific
  mistakes, a policy check that the PR cannot edit (file size, changelog
  fragments, ADR for new dependencies, tests for service changes, no new
  `//nolint` or skipped tests, guardrails apart from code, diff size),
  and a per-package coverage ratchet against `main`. Go tests run with
  `-shuffle=on`. `scripts/ci/gate.sh` runs the same checks locally, and
  `tests/go/apicoverage` reports which real API methods the emulator
  serves.
