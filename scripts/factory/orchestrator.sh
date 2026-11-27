#!/usr/bin/env bash
# Factory orchestrator. No LLM: it reads GitHub labels and starts agents.
#   orchestrator.sh          loop forever, one tick every FACTORY_TICK seconds
#   orchestrator.sh --once   run one tick
# FACTORY_DRY=1 logs what it would start and changes nothing.
# FACTORY_RUNNER=herdr runs every agent in a visible tab of the herdr
# workspace "factory"; headless (default) runs stage jobs in the background.
# Run it inside a herdr pane: build and fix teams open as herdr tabs.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
REPO=${FACTORY_REPO:-GuitarWag/gcp-local}
OWNER=${REPO%%/*}
HOME_F=${FACTORY_HOME:-$HOME/.factory}
TICK=${FACTORY_TICK:-300}
MAX_JOBS=${FACTORY_MAX_JOBS:-3}
MAX_TEAMS=${FACTORY_MAX_TEAMS:-2}
# Entries are "agent" or "agent:provider/model"; jobs take them in turn.
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
# Console UI initiative: keep this many console issues in the pipeline.
CONSOLE_EVERY=${FACTORY_CONSOLE_EVERY:-3600}
CONSOLE_WIP=${FACTORY_CONSOLE_WIP:-2}
RUNNER=${FACTORY_RUNNER:-headless}
# Resident roles stay open in a herdr tab, so the owner can chat with them.
RESIDENT=${FACTORY_RESIDENT-$([ "$RUNNER" = herdr ] && echo "janitor scout")}
RESIDENT_AGENT=${FACTORY_RESIDENT_AGENT:-opencode}
# Private scenarios. Only the spec and accept prompts name the clone.
HOLDOUT_REPO=${FACTORY_HOLDOUT_REPO:-GuitarWag/gcp-local-holdout}
DRY=${FACTORY_DRY:-}
CLAIM="$ROOT/scripts/factory/claim.sh"
export FACTORY_REPO=$REPO FACTORY_HOME=$HOME_F FACTORY_DRY=$DRY

mkdir -p "$HOME_F/logs" "$HOME_F/run" "$HOME_F/wt" "$HOME_F/holdout"
log() { echo "$(date '+%F %T') $*"; }
# shellcheck source=scripts/factory/herdr.sh
. "$ROOT/scripts/factory/herdr.sh"

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
  echo $((n + $(herdr_jobs)))
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

role_running() { ls "$HOME_F"/run/"$1"-*.pid "$HOME_F"/run/"$1"-*.herdr >/dev/null 2>&1; }

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
Do the role now from start to end without questions. Use scripts/factory/claim.sh from this folder."
  if [ "$RUNNER" = herdr ]; then
    printf '%s' "
Do not wait for answers. The owner may type in this pane: follow what the owner types."
  else
    printf '%s' " No human will answer."
  fi
  if [ -n "$holdout" ]; then
    printf '%s' "
Your holdout folder is $holdout (a clone of the private repo $HOLDOUT_REPO). Never copy its content into gcp-local or into a public comment."
  fi
}

# Ephemeral job: own detached worktree on origin/main, removed on exit.
spawn_job() {
  local role=$1 issue=${2:-} agent id wt logf model= holdout=
  agent=$(pick_agent)
  case $agent in *:*) model=${agent#*:} agent=${agent%%:*} ;; esac
  if [ "$role" = review ]; then
    agent=${REVIEW_AGENT:-$agent}
    model=${REVIEW_MODEL:-$model}
  fi
  id="$role-${issue:-x}-$agent-$(date +%s)"
  if [ -n "$DRY" ]; then log "dry-run: start $role ${issue:+#$issue }with $agent${model:+ ($model)}"; return; fi
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
  if [ "$RUNNER" = herdr ] && [ -n "$issue" ]; then
    start_herdr_job "$role" "$issue" "$id" "$agent" "$model" "$wt" "$holdout" || {
      "$CLAIM" drop "$issue" "$id"
      git -C "$ROOT" worktree remove --force "$wt"
      [ -z "$holdout" ] || rm -rf "${holdout:?}"
    }
    return
  fi
  log "start $role ${issue:+#$issue }with $agent${model:+ ($model)} (log: $logf)"
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

overdue() {
  local last
  last=$(cat "$HOME_F/last-$1" 2>/dev/null || echo 0)
  [ $(($(date +%s) - last)) -ge "$2" ]
}
stamp() { [ -n "$DRY" ] || date +%s >"$HOME_F/last-$1"; }
due() { overdue "$1" "$2" && stamp "$1"; }

is_resident() { case " $RESIDENT " in *" $1 "*) return 0 ;; esac; return 1; }

# Janitor and scout: resident agents when listed in RESIDENT, else ephemeral jobs.
interval_role() {
  local role=$1 every=$2
  if is_resident "$role"; then
    ensure_resident "$role" || return
    if overdue "$role" "$every" && run_resident "$role"; then stamp "$role"; fi
  elif ! role_running "$role" && due "$role" "$every"; then
    spawn_job "$role"
  fi
}

tick() {
  [ -n "$DRY" ] || "$CLAIM" reap
  running_jobs >/dev/null
  watchdog
  reap_herdr_jobs
  close_finished_teams

  local role issue
  # Latest stage first: work near merge finishes before new work starts.
  for role in accept review plan verify spec; do
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
  interval_role janitor "$JANITOR_EVERY"
  local backlog=$(($(count_label stage:spec) + $(count_label stage:verify) + $(count_label stage:plan)))
  if [ "$backlog" -lt "$SCOUT_BACKLOG" ]; then
    interval_role scout "$SCOUT_EVERY"
  elif is_resident scout; then
    ensure_resident scout
  fi
  local console_open
  console_open=$(gh issue list -R "$REPO" --state open --label console --limit 100 --json labels -q '
    [.[] | select([.labels[].name] | index("initiative") | not)] | length')
  if [ "${console_open:-$CONSOLE_WIP}" -lt "$CONSOLE_WIP" ]; then interval_role console "$CONSOLE_EVERY"; fi
}

while :; do
  if [ -e "$HOME_F/STOP" ]; then log "STOP file found; exit"; exit 0; fi
  tick
  [ "${1:-}" = --once ] && exit 0
  sleep "$TICK"
done
