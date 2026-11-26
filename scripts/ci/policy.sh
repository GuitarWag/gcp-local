#!/usr/bin/env bash
# Repo policy for every PR, checked against the merge base with origin/main.
# CI runs the copy of this script from main, so a PR cannot weaken it.
#   policy.sh [base-ref]      default: origin/main
# Env: PR_TITLE, PR_BRANCH (CI sets them; local runs read git).
set -uo pipefail

BASE=${1:-origin/main}
MAX_LINES=${POLICY_MAX_FILE_LINES:-500}
MAX_DIFF=${POLICY_MAX_DIFF:-1500}

mb=$(git merge-base HEAD "$BASE") || { echo "policy: no merge base with $BASE" >&2; exit 2; }
branch=${PR_BRANCH:-$(git rev-parse --abbrev-ref HEAD)}
title=${PR_TITLE:-$(git log -1 --format=%s)}
# Working tree vs merge base: CI has a clean tree, local runs see edits too.
untracked=$(git ls-files --others --exclude-standard)
changed=$(git diff --name-only --diff-filter=ACMR "$mb"; echo "$untracked")
added=$(git diff --name-only --diff-filter=A "$mb"; echo "$untracked")
all_changed=$(git diff --name-only "$mb"; echo "$untracked")
bootstrap=
git cat-file -e "$mb:scripts/ci/policy.sh" 2>/dev/null || bootstrap=1

fails=0
fail() { echo "policy: FAIL: $*"; fails=$((fails + 1)); }
has() { printf '%s\n' "$all_changed" | grep -Eq "$1"; }
release=
case $branch in release/v*) release=1 ;; esac

# 1. File size: new Go files stay under the limit; big old files never grow.
for f in $(printf '%s\n' "$changed" | grep -E '^(internal|cmd)/.*\.go$' | grep -v '_test\.go$'); do
  [ -f "$f" ] || continue
  now=$(wc -l <"$f" | tr -d ' ')
  [ "$now" -gt "$MAX_LINES" ] || continue
  before=$(git show "$mb:$f" 2>/dev/null | wc -l | tr -d ' ')
  if [ "$before" -le "$MAX_LINES" ]; then
    fail "$f has $now lines (limit $MAX_LINES). Split it."
  elif [ "$now" -gt "$before" ]; then
    fail "$f grew from $before to $now lines and is over the $MAX_LINES limit. Split it before you add code."
  fi
done

# 2-3. Changelog: fragments only, release PRs excepted.
if [ -z "$release" ]; then
  has '^CHANGELOG\.md$' && fail "PR edits CHANGELOG.md. Add a changelog.d/ fragment instead."
  frags=$(printf '%s\n' "$added" | grep -E '^changelog\.d/.+\.md$' | grep -v '/README\.md$')
  if [ -z "$frags" ]; then
    fail "PR adds no changelog.d/<issue>-<slug>.md fragment."
  fi
  for f in $frags; do
    head -1 "$f" | grep -Eq '^(Added|Changed|Deprecated|Removed|Fixed|Security) (minor|patch)$' ||
      fail "$f: first line must be '<Section> <minor|patch>'."
  done
fi

# 4. New direct dependency needs a new ADR in the same PR.
if git diff "$mb" -- go.mod | grep -E '^\+[[:space:]]+[a-z]' | grep -vq '// indirect'; then
  printf '%s\n' "$added" | grep -Eq '^docs/adr/[0-9].*\.md$' ||
    fail "go.mod adds a direct dependency without a new ADR in docs/adr/."
fi

# 5. Tests live under tests/<lang>/ only.
for f in $(printf '%s\n' "$added" | grep -E '_test\.go$' | grep -v '^tests/'); do
  fail "$f: tests live under tests/<lang>/ only."
done

# 6. Service code changes need test changes. Refactors lean on the coverage check.
if has '^internal/services/.*\.go$' && ! has '^tests/(go|python|typescript)/'; then
  case $title in
    refactor*) echo "policy: refactor with no test change; the coverage check guards it" ;;
    *) fail "internal/services changed but no file under tests/ changed." ;;
  esac
fi

# 7. Escape hatches may not grow.
count() { # count <rev|WORKTREE> <regex> <pathspec...>
  local rev=$1 re=$2
  shift 2
  if [ "$rev" = WORKTREE ]; then
    git grep --untracked -E -h -o "$re" -- "$@" 2>/dev/null | wc -l | tr -d ' '
  else
    git grep -E -h -o "$re" "$rev" -- "$@" 2>/dev/null | wc -l | tr -d ' '
  fi
}
ratchet() { # ratchet <name> <regex> <pathspec...>
  local name=$1 re=$2 before now
  shift 2
  before=$(count "$mb" "$re" "$@")
  now=$(count WORKTREE "$re" "$@")
  if [ "$now" -gt "$before" ]; then
    if [ -n "$bootstrap" ]; then
      echo "policy: bootstrap: $name $before -> $now allowed once"
    else
      fail "$name grew from $before to $now. Fix the cause instead of silencing the check."
    fi
  fi
}
ratchet "//nolint comments" '//nolint' '*.go'
ratchet "ast-grep-ignore comments" 'ast-grep-ignore' '*.go'
ratchet "skipped Go tests" '\.(Skip|Skipf|SkipNow)\(' 'tests/go'
ratchet "skipped Python tests" 'pytest\.mark\.(skip|xfail)|pytest\.skip\(' 'tests/python'
ratchet "skipped or focused TS tests" '\b(it|test|describe)\.(skip|only|todo)\(' 'tests/typescript'
ratchet "golangci-lint exclusion rules" '^[[:space:]]+- path:' '.golangci.yml'

# 8. Guardrails change alone, never in the same PR as the code they judge.
guard='^(\.golangci(-new)?\.yml|sgconfig\.yml|\.sg/|scripts/ci/|\.github/workflows/)'
if has "$guard" && [ -z "$bootstrap" ]; then
  code=$(printf '%s\n' "$all_changed" | grep -E '^(internal|cmd|tests)/' | head -3)
  [ -z "$code" ] ||
    fail "PR changes CI guardrails and code together ($(echo "$code" | tr '\n' ' ')). Split it into two PRs."
fi

# 9. Diff size forces the plan stage to split big features.
if [ -z "$release" ]; then
  size=$(git diff --numstat "$mb" -- . ':!go.sum' ':!tests/go/go.sum' ':!tests/typescript/pnpm-lock.yaml' \
    ':!*.pb.go' ':!CHANGELOG.md' | awk '{ s += $1 + $2 } END { print s + 0 }')
  [ "$size" -le "$MAX_DIFF" ] ||
    fail "diff has $size changed lines (limit $MAX_DIFF). Split the work into smaller PRs."
fi

if [ "$fails" -gt 0 ]; then
  echo "policy: $fails failure(s)"
  exit 1
fi
echo "policy: ok"
