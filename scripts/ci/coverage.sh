#!/usr/bin/env bash
# Coverage ratchet. Runs the Go integration tests against a coverage build of
# the binary, for HEAD and for the merge base, and fails when a package drops.
# A failing comparison re-measures once into fresh output dirs; the drop must
# reproduce on the second comparison to fail the ratchet.
#   coverage.sh [base-ref]    default: origin/main
# Env: COVER_TOLERANCE (points, default 2.0; parallel tests vary by ~1.5), COVER_NEW_FLOOR (percent for a
# new internal/services package, default 60).
set -uo pipefail

BASE=${1:-origin/main}
TOL=${COVER_TOLERANCE:-2.0}
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

# head_guards <head-dir>: exact lines and exit 1, also for the re-measured head.
head_guards() {
  if [ -e "$1/status" ]; then
    echo "coverage: tests failed on HEAD"
    tail -40 "$1/test.log"
    exit 1
  fi
  if [ ! -s "$1/pct" ]; then
    echo "coverage: HEAD produced no coverage data"
    exit 1
  fi
}

# compare <base-dir> <head-dir>: table and FAIL lines; status 1 on failure.
compare() {
  if [ -e "$1/status" ] || [ ! -s "$1/pct" ]; then
    echo "coverage: no usable base data; reporting HEAD only"
    : >"$1/pct"
  fi
  local has_base=0
  [ -s "$1/pct" ] && has_base=1
  join -a 2 -e none -o 0,1.2,2.2 "$1/pct" "$2/pct" |
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
}

git -C "$ROOT" worktree add -q --detach "$work/base-src" "$(git -C "$ROOT" merge-base HEAD "$BASE")"
# Sequential on purpose: parallel runs change stream timing and add noise.
measure "$ROOT" "$work/head"
measure "$work/base-src" "$work/base"

head_guards "$work/head"

echo "coverage per package (base -> head):"
compare "$work/base" "$work/head"
[ $? -eq 0 ] && exit 0

echo "coverage: first comparison failed; re-measure head and base once"
measure "$ROOT" "$work/head2"
measure "$work/base-src" "$work/base2"

head_guards "$work/head2"

echo "coverage per package, re-measured (base -> head):"
compare "$work/base2" "$work/head2"
status=$?
[ "$status" -eq 0 ] && echo "coverage: ok"
exit "$status"
