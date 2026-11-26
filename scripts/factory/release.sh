#!/usr/bin/env bash
# Release manager. No LLM: merges approved PRs and cuts releases.
#   release.sh merge   merge green stage:merge PRs and green release PRs
#   release.sh cut     fold changelog.d into CHANGELOG.md and open a release PR
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
REPO=${FACTORY_REPO:-GuitarWag/gcp-local}
OWNER=${REPO%%/*}
HOME_F=${FACTORY_HOME:-$HOME/.factory}
CLAIM="$ROOT/scripts/factory/claim.sh"
DRY=${FACTORY_DRY:-}

log() { echo "$(date '+%F %T') release: $*"; }
run() { if [ -n "$DRY" ]; then log "dry-run: $*"; else "$@"; fi; }

# Prints green, red or pending for a PR.
pr_state() {
  gh pr view "$1" -R "$REPO" --json mergeable,statusCheckRollup -q '
    [.statusCheckRollup[] | (.conclusion // .state // "")] as $c
    | if .mergeable == "CONFLICTING" then "conflict"
      elif ($c | any(. == "FAILURE" or . == "CANCELLED" or . == "TIMED_OUT" or . == "ERROR" or . == "ACTION_REQUIRED")) then "red"
      elif ($c | length) > 0 and ($c | all(. == "SUCCESS" or . == "SKIPPED" or . == "NEUTRAL")) and .mergeable == "MERGEABLE" then "green"
      else "pending" end'
}

open_pr_for() {
  for pr in $(gh issue view "$1" -R "$REPO" --json closedByPullRequestsReferences \
    -q '.closedByPullRequestsReferences[].number'); do
    if [ "$(gh pr view "$pr" -R "$REPO" --json state -q .state)" = OPEN ]; then
      echo "$pr"
      return
    fi
  done
}

merge() {
  for issue in $(gh issue list -R "$REPO" --state open --label stage:merge --json number,author,labels \
    -q "[.[] | select(.author.login == \"$OWNER\") | select([.labels[].name] | index(\"factory:claimed\") | not)] | .[].number"); do
    pr=$(open_pr_for "$issue")
    if [ -z "$pr" ]; then
      run "$CLAIM" flag "$issue" "stage:merge but no open PR closes this issue"
      continue
    fi
    case $(pr_state "$pr") in
      green)
        log "merge PR #$pr for issue #$issue"
        run gh pr merge "$pr" -R "$REPO" --squash --delete-branch &&
          run gh issue edit "$issue" -R "$REPO" --remove-label stage:merge --add-label factory:done >/dev/null &&
          { [ "$(gh issue view "$issue" -R "$REPO" --json state -q .state)" = CLOSED ] ||
            run gh issue close "$issue" -R "$REPO" --reason completed; }
        ;;
      conflict)
        log "PR #$pr conflicts with main; back to fix"
        run gh issue comment "$issue" -R "$REPO" --body "<!-- factory:release conflict -->
PR #$pr conflicts with \`main\`. Merge \`origin/main\` into the branch and resolve the conflict." >/dev/null
        run "$CLAIM" advance "$issue" stage:merge stage:fix
        ;;
      red)
        log "PR #$pr has failed checks; back to fix"
        run gh issue comment "$issue" -R "$REPO" --body "<!-- factory:release checks -->
PR #$pr has failed checks. Run \`gh pr checks $pr\` and fix them." >/dev/null
        run "$CLAIM" advance "$issue" stage:merge stage:fix
        ;;
    esac
  done

  for pr in $(gh pr list -R "$REPO" --state open --label release --json number -q '.[].number'); do
    case $(pr_state "$pr") in
      green) log "merge release PR #$pr"; run gh pr merge "$pr" -R "$REPO" --squash --delete-branch ;;
      red | conflict) log "release PR #$pr is broken; closing it so the next cut retries"
        run gh pr close "$pr" -R "$REPO" --delete-branch --comment "Release PR is broken. The next cut opens a new one." ;;
    esac
  done
}

cut() {
  if [ -n "$(gh pr list -R "$REPO" --state open --label release --json number -q '.[].number')" ]; then
    log "a release PR is already open"
    return
  fi
  git -C "$ROOT" fetch -q origin main
  if ! git -C "$ROOT" ls-tree --name-only origin/main changelog.d/ | grep -v '/README.md$' | grep -q '\.md$'; then
    log "no changelog fragments on main"
    return
  fi
  wt="$HOME_F/wt/release-$$"
  git -C "$ROOT" worktree add -q --detach "$wt" origin/main || return
  (
    cd "$wt" || exit 1
    ver=$(python3 -I scripts/factory/changelog.py .) || exit 1
    log "cut v$ver"
    [ -n "$DRY" ] && { git diff --stat; exit 0; }
    git switch -q -c "release/v$ver" &&
      git add -A &&
      git commit -q -m "chore(release): v$ver" &&
      git push -q -u origin "release/v$ver" &&
      gh pr create -R "$REPO" --base main --head "release/v$ver" --label release \
        --title "chore(release): v$ver" \
        --body "Release v$ver. Folds the changelog fragments on \`main\` into \`CHANGELOG.md\`. The merge triggers \`auto-tag.yml\`."
  )
  git -C "$ROOT" worktree remove --force "$wt"
}

case "${1:-}" in
  merge) merge ;;
  cut) cut ;;
  *) echo "usage: $0 merge|cut" >&2; exit 2 ;;
esac
