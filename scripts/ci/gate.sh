#!/usr/bin/env bash
# The pre-push gate: every check that CI runs, in one command.
#   gate.sh            static checks, policy and all three test suites
#   gate.sh --cover    also the coverage ratchet (about 2 minutes more)
# The gate runs every step and lists all failures at the end.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 2

failed=()
step() {
  local name=$1
  shift
  echo "==> $name"
  if "$@"; then echo "    ok"; else failed+=("$name"); fi
}
gofmt_clean() { [ -z "$(gofmt -l internal/ cmd/ | tee /dev/stderr)" ]; }

git fetch -q origin main 2>/dev/null || echo "gate: fetch failed; using the local origin/main"

step "go vet" go vet ./...
step "gofmt" gofmt_clean
step "golangci-lint" golangci-lint run ./...
step "golangci-lint (new code)" golangci-lint run -c .golangci-new.yml ./...
step "ast-grep" ast-grep scan
step "policy" scripts/ci/policy.sh
step "go test" bash -c 'cd tests/go && go test -race -count=1 -shuffle=on ./...'
step "pytest" bash -c 'cd tests/python && .venv/bin/python -m pytest -q'
step "vitest" bash -c 'cd tests/typescript && pnpm test'
[ "${1:-}" = --cover ] && step "coverage" scripts/ci/coverage.sh

if [ ${#failed[@]} -gt 0 ]; then
  echo "gate: FAILED: ${failed[*]}"
  exit 1
fi
echo "gate: all checks passed"
