#!/usr/bin/env bash
# Claims with expiry, kept as issue comments. See .agents/skills/factory/SKILL.md.
set -euo pipefail

REPO=${FACTORY_REPO:-GuitarWag/gcp-local}
MAX_FAILS=${FACTORY_MAX_FAILS:-3}

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
in_minutes() {
  date -u -v+"$1"M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "+$1 min" +%Y-%m-%dT%H:%M:%SZ
}

# Prints "comment-id<TAB>agent<TAB>until" for each claim, oldest first.
claims() {
  gh api "repos/$REPO/issues/$1/comments" --paginate -q '.[]
    | select(.body | startswith("<!-- factory:claim "))
    | [.id, (.body | capture("agent=(?<a>[^ ]+)").a), (.body | capture("until=(?<u>[^ ]+)").u)]
    | @tsv'
}

delete_comment() { gh api -X DELETE "repos/$REPO/issues/comments/$1" >/dev/null; }
comment() { gh issue comment "$1" -R "$REPO" --body "$2" >/dev/null; }
stage_of() {
  gh issue view "$1" -R "$REPO" --json labels -q '[.labels[].name | select(startswith("stage:"))][0] // ""'
}

unclaim_if_empty() {
  if [ -z "$(claims "$1")" ]; then
    gh issue edit "$1" -R "$REPO" --remove-label factory:claimed >/dev/null
  fi
}

flag() {
  local issue=$1 reason=$2
  claims "$issue" | while IFS=$'\t' read -r id _ _; do delete_comment "$id"; done
  comment "$issue" "<!-- factory:flagged -->
Factory flagged this issue: $reason"
  gh issue edit "$issue" -R "$REPO" --add-label factory:flagged --remove-label factory:claimed >/dev/null
}

# Records a failed attempt. Flags the issue after MAX_FAILS at one stage.
fail() {
  local issue=$1 id=$2 agent=$3 stage
  stage=$(stage_of "$issue")
  delete_comment "$id"
  comment "$issue" "<!-- factory:failed stage=$stage agent=$agent -->
Agent \`$agent\` ended without finishing \`$stage\`. Another agent will retry."
  local fails
  # Count failures since the last janitor reset only.
  fails=$(gh issue view "$issue" -R "$REPO" --json comments -q "
    [.comments[].body] as \$b
    | ([\$b | to_entries[] | select(.value | startswith(\"<!-- factory:janitor reset\")) | .key] | max // -1) as \$r
    | [\$b[(\$r + 1):][] | select(startswith(\"<!-- factory:failed stage=$stage \"))] | length")
  if [ "$fails" -ge "$MAX_FAILS" ]; then
    flag "$issue" "$fails failed attempts at $stage"
  else
    unclaim_if_empty "$issue"
  fi
}

cmd=${1:-}
case "$cmd" in
  claim)
    issue=$2 agent=$3 minutes=$4
    gh issue edit "$issue" -R "$REPO" --add-label factory:claimed >/dev/null
    mine=$(gh api "repos/$REPO/issues/$issue/comments" \
      -f body="<!-- factory:claim agent=$agent until=$(in_minutes "$minutes") -->" -q .id)
    winner=$(claims "$issue" | awk -F'\t' -v now="$(now)" '$3 >= now { print $1; exit }')
    if [ "$winner" != "$mine" ]; then
      delete_comment "$mine"
      echo "issue #$issue is already claimed" >&2
      exit 1
    fi
    ;;
  renew)
    issue=$2 agent=$3 minutes=$4
    id=$(claims "$issue" | awk -F'\t' -v a="$agent" '$2 == a { print $1; exit }')
    [ -n "$id" ] || { echo "no claim by $agent on #$issue" >&2; exit 1; }
    gh api -X PATCH "repos/$REPO/issues/comments/$id" \
      -f body="<!-- factory:claim agent=$agent until=$(in_minutes "$minutes") -->" >/dev/null
    ;;
  advance)
    issue=$2 from=$3 to=$4
    claims "$issue" | while IFS=$'\t' read -r id _ _; do delete_comment "$id"; done
    gh issue edit "$issue" -R "$REPO" --remove-label "$from" --remove-label factory:claimed \
      --add-label "$to" >/dev/null
    ;;
  flag)
    flag "$2" "$3"
    ;;
  drop)
    # Orchestrator: the agent process exited and its claim is still there.
    issue=$2 agent=$3
    claims "$issue" | awk -F'\t' -v a="$agent" '$2 == a' |
      while IFS=$'\t' read -r id a _; do fail "$issue" "$id" "$a"; done
    ;;
  holds)
    # Exit 0 when the agent still holds a claim on the issue.
    claims "$2" | awk -F'\t' -v a="$3" '$2 == a { found = 1 } END { exit !found }'
    ;;
  reap)
    for issue in $(gh issue list -R "$REPO" --state all --label factory:claimed --limit 200 \
      --json number -q '.[].number'); do
      claims "$issue" | awk -F'\t' -v now="$(now)" '$3 < now' |
        while IFS=$'\t' read -r id a _; do fail "$issue" "$id" "$a"; done
      unclaim_if_empty "$issue"
    done
    ;;
  *)
    echo "usage: $0 claim|renew <issue> <agent> <minutes> | advance <issue> <from> <to> | flag <issue> <reason> | drop|holds <issue> <agent> | reap" >&2
    exit 2
    ;;
esac
