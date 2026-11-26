#!/usr/bin/env bash
# Factory orchestrator. No LLM: it reads GitHub labels and starts agents.
#   orchestrator.sh          loop forever, one tick every FACTORY_TICK seconds
#   orchestrator.sh --once   run one tick
# FACTORY_DRY=1 logs what it would start and changes nothing.
# Run it inside a herdr pane: build and fix teams open as herdr tabs.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
REPO=${FACTORY_REPO:-GuitarWag/gcp-local}
OWNER=${REPO%%/*}
HOME_F=${FACTORY_HOME:-$HOME/.factory}
TICK=${FACTORY_TICK:-300}
MAX_JOBS=${FACTORY_MAX_JOBS:-3}
MAX_TEAMS=${FACTORY_MAX_TEAMS:-2}
AGENTS=${FACTORY_AGENTS:-"pi opencode"}
# A reviewer from another model family catches what the builders miss.
REVIEW_AGENT=${FACTORY_REVIEW_AGENT:-}
REVIEW_MODEL=${FACTORY_REVIEW_MODEL:-}
BOSS=${FACTORY_BOSS:-opencode}
WORKER=${FACTORY_WORKER:-pi}
JOB_LEASE=${FACTORY_JOB_LEASE:-60}
STALL_MIN=${FACTORY_STALL_MIN:-20}
JOB_MAX_MIN=${FACTORY_JOB_MAX_MIN:-90}
TEAM_LEASE=${FACTORY_TEAM_LEASE:-240}
JANITOR_EVERY=${FACTORY_JANITOR_EVERY:-3600}
SCOUT_EVERY=${FACTORY_SCOUT_EVERY:-86400}
RELEASE_EVERY=${FACTORY_RELEASE_EVERY:-3600}
SCOUT_BACKLOG=${FACTORY_SCOUT_BACKLOG:-5}
# Private scenarios. Only the spec and accept prompts name the clone.
HOLDOUT_REPO=${FACTORY_HOLDOUT_REPO:-GuitarWag/gcp-local-holdout}
DRY=${FACTORY_DRY:-}
CLAIM="$ROOT/scripts/factory/claim.sh"
export FACTORY_REPO=$REPO FACTORY_HOME=$HOME_F FACTORY_DRY=$DRY

mkdir -p "$HOME_F/logs" "$HOME_F/run" "$HOME_F/wt" "$HOME_F/holdout"
log() { echo "$(date '+%F %T') $*"; }

pick_agent() {
  local n
  n=$(cat "$HOME_F/rr" 2>/dev/null || echo 0)
  echo $((n + 1)) >"$HOME_F/rr"
  set -- $AGENTS
  shift $((n % $#))
  echo "$1"
}

run_agent() {
  local agent=$1 prompt=$2 model=${3:-}
  case $agent in
    # json mode streams events, so the watchdog sees progress in the log.
    pi) pi -p --mode json --no-session ${model:+--model "$model"} "$prompt" ;;
    opencode) opencode run --auto --archived ${model:+-m "$model"} "$prompt" ;;
    *) echo "unknown agent: $agent" >&2; return 2 ;;
  esac
}

running_jobs() {
  local n=0 f
  for f in "$HOME_F"/run/*.pid; do
    [ -e "$f" ] || continue
    if kill -0 "$(cat "$f")" 2>/dev/null; then n=$((n + 1)); else rm -f "$f"; fi
  done
  echo "$n"
}

mtime() { stat -f %m "$1" 2>/dev/null || stat -c %Y "$1"; }

# Stops agents that hang: a model request with no answer never times out.
# The job wrapper then releases the claim and removes the worktree.
watchdog() {
  local f id wrapper logf now idle age
  now=$(date +%s)
  for f in "$HOME_F"/run/*.pid; do
    [ -e "$f" ] || continue
    id=$(basename "$f" .pid)
    wrapper=$(cat "$f")
    logf="$HOME_F/logs/$id.log"
    [ -e "$logf" ] || continue
    idle=$(((now - $(mtime "$logf")) / 60))
    age=$(((now - $(mtime "$f")) / 60))
    if [ "$idle" -ge "$STALL_MIN" ] || [ "$age" -ge "$JOB_MAX_MIN" ]; then
      log "watchdog: stop $id (no log output for ${idle}m, running ${age}m)"
      [ -n "$DRY" ] || pkill -TERM -P "$wrapper"
    fi
  done
}

role_running() { ls "$HOME_F"/run/"$1"-*.pid >/dev/null 2>&1; }

# Open owner issues in a stage, without a claim, oldest first.
issues_in() {
  gh issue list -R "$REPO" --state open --label "stage:$1" --limit 100 --json number,author,labels -q "
    [.[] | select(.author.login == \"$OWNER\")
         | select([.labels[].name] | (index(\"factory:claimed\") or index(\"factory:flagged\") or index(\"factory:ignore\")) | not)]
    | sort_by(.number) | .[].number"
}

count_label() {
  gh issue list -R "$REPO" --state open --label "$1" --limit 200 --json number -q 'length'
}

teams_running() {
  gh issue list -R "$REPO" --state open --label factory:claimed --limit 200 --json labels -q '
    [.[] | select([.labels[].name] | (index("stage:build") or index("stage:fix")))] | length'
}

prompt_for() {
  local role=$1 issue=$2 id=$3 wt=$4 holdout=${5:-}
  printf '%s' "You are the gcp-local factory agent for the role \"$role\". Your agent id is \"$id\".${issue:+ Your issue is #$issue.}
Work only in the folder $wt. Read AGENTS.md, then .agents/skills/factory/SKILL.md, then .agents/skills/factory/roles/$role.md.
Do the role now from start to end without questions. No human will answer. Use scripts/factory/claim.sh from this folder."
  if [ -n "$holdout" ]; then
    printf '%s' "
Your holdout folder is $holdout (a clone of the private repo $HOLDOUT_REPO). Never copy its content into gcp-local or into a public comment."
  fi
}

# Ephemeral job: own detached worktree on origin/main, removed on exit.
spawn_job() {
  local role=$1 issue=${2:-} agent id wt logf model= holdout=
  agent=$(pick_agent)
  if [ "$role" = review ]; then
    agent=${REVIEW_AGENT:-$agent}
    model=$REVIEW_MODEL
  fi
  id="$role-${issue:-x}-$agent-$(date +%s)"
  if [ -n "$DRY" ]; then log "dry-run: start $role ${issue:+#$issue }with $agent"; return; fi
  if [ -n "$issue" ]; then "$CLAIM" claim "$issue" "$id" "$JOB_LEASE" || return; fi
  wt="$HOME_F/wt/$id"
  logf="$HOME_F/logs/$id.log"
  git -C "$ROOT" fetch -q origin main
  git -C "$ROOT" worktree add -q --detach "$wt" origin/main || {
    [ -n "$issue" ] && "$CLAIM" drop "$issue" "$id"
    return
  }
  if [ "$role" = spec ] || [ "$role" = accept ]; then
    holdout="$HOME_F/holdout/$id"
    gh repo clone "$HOLDOUT_REPO" "$holdout" -- -q >>"$logf" 2>&1 || {
      log "holdout clone failed for $id"
      git -C "$ROOT" worktree remove --force "$wt"
      "$CLAIM" drop "$issue" "$id"
      return
    }
  fi
  log "start $role ${issue:+#$issue }with $agent (log: $logf)"
  (
    cd "$wt" && run_agent "$agent" "$(prompt_for "$role" "$issue" "$id" "$wt" "$holdout")" "$model" >"$logf" 2>&1
    log "end $role ${issue:+#$issue }exit=$?" >>"$logf"
    if [ -n "$issue" ] && "$CLAIM" holds "$issue" "$id"; then "$CLAIM" drop "$issue" "$id"; fi
    git -C "$ROOT" worktree remove --force "$wt"
    [ -z "$holdout" ] || rm -rf "${holdout:?}"
    rm -f "$HOME_F/run/$id.pid"
  ) &
  echo $! >"$HOME_F/run/$id.pid"
}

team_tab() {
  herdr agent list 2>/dev/null |
    jq -r --arg n "f$1-boss" '.result.agents[] | select(.name == $n) | .tab_id' | head -1
}

close_team() {
  local tab
  tab=$(team_tab "$1")
  [ -n "$tab" ] || return 0
  log "close herdr team f$1"
  [ -n "$DRY" ] || herdr tab close "$tab" >/dev/null
}

# Build or fix: a herdr team in a worktree on the feature branch.
spawn_team() {
  local role=$1 issue=$2 branch size wt pr id
  if [ "$role" = fix ]; then
    pr=$(gh issue view "$issue" -R "$REPO" --json closedByPullRequestsReferences -q '.closedByPullRequestsReferences[-1].number // ""')
    branch=$([ -n "$pr" ] && gh pr view "$pr" -R "$REPO" --json headRefName -q .headRefName)
    size=2
  else
    local plan
    plan=$(gh issue view "$issue" -R "$REPO" --json comments \
      -q "[.comments[] | select(.author.login == \"$OWNER\") | .body | select(startswith(\"<!-- factory:plan \"))][-1] // \"\"" | head -1)
    branch=$(printf '%s' "$plan" | sed -n 's/.*branch=\([^ ]*\).*/\1/p')
    size=$(printf '%s' "$plan" | sed -n 's/.*team=\([0-9]*\).*/\1/p')
  fi
  branch=${branch:-factory/$issue}
  size=${size:-3}
  if [ -n "$DRY" ]; then log "dry-run: start $role team f$issue on $branch with $size workers"; return; fi

  id="team-f$issue"
  "$CLAIM" claim "$issue" "$id" "$TEAM_LEASE" || return
  close_team "$issue"
  wt="$HOME_F/wt/f$issue"
  git -C "$ROOT" fetch -q origin
  if [ ! -d "$wt" ]; then
    if git -C "$ROOT" ls-remote --exit-code --heads origin "$branch" >/dev/null; then
      git -C "$ROOT" worktree add -q -B "$branch" "$wt" "origin/$branch"
    else
      git -C "$ROOT" worktree add -q -b "$branch" "$wt" origin/main
    fi || { log "worktree for $branch failed"; "$CLAIM" drop "$issue" "$id"; return; }
  fi
  log "start $role team f$issue on $branch with $size workers"
  herdr-shepherd "f$issue" --cwd "$wt" --boss-agent "$BOSS" --worker-agent "$WORKER" \
    --workers "$size" >>"$HOME_F/logs/f$issue.log" 2>&1 || {
    "$CLAIM" drop "$issue" "$id"
    return
  }
  sleep 30 # ponytail: fixed wait for the boss to start; poll herdr if it is too short
  herdr-shepherd "f$issue" --send boss --message "$(prompt_for "$role" "$issue" "$id" "$wt")
You are the herdr boss of team f$issue. Renew the claim with: scripts/factory/claim.sh renew $issue $id $TEAM_LEASE" \
    >>"$HOME_F/logs/f$issue.log" 2>&1
}

# Close tabs of teams whose issue left build or fix.
close_finished_teams() {
  local issue active
  for issue in $(herdr agent list 2>/dev/null |
    jq -r '.result.agents[].name // empty | select(test("^f[0-9]+-boss$")) | ltrimstr("f") | rtrimstr("-boss")'); do
    active=$(gh issue view "$issue" -R "$REPO" --json state,labels -q '
      [.labels[].name] as $l
      | .state == "OPEN" and ($l | index("factory:claimed")) != null
        and (($l | index("stage:build")) != null or ($l | index("stage:fix")) != null)')
    [ "$active" = true ] || close_team "$issue"
  done
}

due() {
  local stamp="$HOME_F/last-$1" last
  last=$(cat "$stamp" 2>/dev/null || echo 0)
  [ $(($(date +%s) - last)) -ge "$2" ] || return 1
  [ -n "$DRY" ] || date +%s >"$stamp"
}

tick() {
  [ -n "$DRY" ] || "$CLAIM" reap
  running_jobs >/dev/null
  watchdog
  close_finished_teams

  local role issue
  for role in spec verify plan review accept; do
    for issue in $(issues_in "$role"); do
      [ "$(running_jobs)" -lt "$MAX_JOBS" ] || break 2
      spawn_job "$role" "$issue"
    done
  done

  for role in fix build; do
    for issue in $(issues_in "$role"); do
      [ "$(teams_running)" -lt "$MAX_TEAMS" ] || break 2
      spawn_team "$role" "$issue"
    done
  done

  "$ROOT/scripts/factory/release.sh" merge
  if due release "$RELEASE_EVERY"; then "$ROOT/scripts/factory/release.sh" cut; fi
  if ! role_running janitor && due janitor "$JANITOR_EVERY"; then spawn_job janitor; fi
  if ! role_running scout; then
    local backlog=$(($(count_label stage:spec) + $(count_label stage:verify) + $(count_label stage:plan)))
    if [ "$backlog" -lt "$SCOUT_BACKLOG" ] && due scout "$SCOUT_EVERY"; then spawn_job scout; fi
  fi
}

while :; do
  if [ -e "$HOME_F/STOP" ]; then log "STOP file found; exit"; exit 0; fi
  tick
  [ "${1:-}" = --once ] && exit 0
  sleep "$TICK"
done
