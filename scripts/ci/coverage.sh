#!/usr/bin/env bash
# Coverage ratchet. Runs the Go integration tests against a coverage build of
# the binary, for HEAD and for the merge base, and fails when a package drops.
#   coverage.sh [base-ref]    default: origin/main
# Env: COVER_TOLERANCE (points, default 1.0), COVER_NEW_FLOOR (percent for a
# new internal/services package, default 60).
set -uo pipefail

BASE=${1:-origin/main}
TOL=${COVER_TOLERANCE:-1.0}
FLOOR=${COVER_NEW_FLOOR:-60}
ROOT=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'git -C "$ROOT" worktree remove --force "$work/base-src" 2>/dev/null; rm -rf "$work"' EXIT

# measure <source-root> <out-dir>: writes <out-dir>/pct as "pkg percent".
measure() {
  local src=$1 out=$2
  mkdir -p "$out/data"
  if ! (cd "$src/tests/go" && GCP_LOCAL_COVERDIR="$out/data" go test -count=1 ./... >"$out/test.log" 2>&1); then
    echo "fail" >"$out/status"
    return
  fi
  go tool covdata percent -i "$out/data" 2>/dev/null |
    awk '{ sub("github.com/GuitarWag/gcp-local/", "", $1); sub("%", "", $3); print $1, $3 }' |
    sort >"$out/pct"
}

git -C "$ROOT" worktree add -q --detach "$work/base-src" "$(git -C "$ROOT" merge-base HEAD "$BASE")"
measure "$ROOT" "$work/head" &
measure "$work/base-src" "$work/base" &
wait

if [ -e "$work/head/status" ]; then
  echo "coverage: tests failed on HEAD"
  tail -40 "$work/head/test.log"
  exit 1
fi
if [ ! -s "$work/head/pct" ]; then
  echo "coverage: HEAD produced no coverage data"
  exit 1
fi

echo "coverage per package (base -> head):"
if [ -e "$work/base/status" ] || [ ! -s "$work/base/pct" ]; then
  echo "coverage: no usable base data; reporting HEAD only"
  : >"$work/base/pct"
fi

has_base=0
[ -s "$work/base/pct" ] && has_base=1
join -a 2 -e none -o 0,1.2,2.2 "$work/base/pct" "$work/head/pct" |
  awk -v tol="$TOL" -v floor="$FLOOR" -v has_base="$has_base" '
    {
      pkg = $1; base = $2; head = $3
      printf "  %-45s %6s -> %6s\n", pkg, base, head
      if (base == "none") {
        if (pkg ~ /^internal\/services\// && head + 0 < floor + 0 && has_base) {
          printf "coverage: FAIL: new package %s has %.1f%% (floor %d%%)\n", pkg, head, floor
          bad++
        }
      } else if (head + tol < base) {
        printf "coverage: FAIL: %s dropped from %.1f%% to %.1f%%\n", pkg, base, head
        bad++
      }
    }
    END { exit bad > 0 }
  '
status=$?
[ "$status" -eq 0 ] && echo "coverage: ok"
exit "$status"
