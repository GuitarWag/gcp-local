#!/usr/bin/env bash
#
# scripts/seed.sh — heavy seed of every emulated service. Useful for poking
# at the /console UI with realistic-looking data instead of a blank slate.
#
# Usage:
#   ./scripts/seed.sh                          # defaults to http://localhost:4443
#   ./scripts/seed.sh http://localhost:14443   # custom port
#   BASE=http://localhost:4443 ./scripts/seed.sh
#   ./scripts/seed.sh --quiet                  # suppress progress output
#
# Idempotent-ish: if a resource already exists the create call returns a
# 4xx and we keep going.
#
# Compatible with bash 3.2 (the default on macOS); no associative arrays.

set -u  # don't -e, we tolerate per-call failures

BASE="${BASE:-${1:-http://localhost:4443}}"
PROJECT="${PROJECT:-local-project}"
QUIET=0
if [[ "${1:-}" == "--quiet" || "${2:-}" == "--quiet" ]]; then
  QUIET=1
fi

# colors only when stdout is a tty
if [[ -t 1 && $QUIET -eq 0 ]]; then
  C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_GREEN=$'\033[32m'; C_BLUE=$'\033[34m'
  C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_RESET=$'\033[0m'
else
  C_BOLD=''; C_DIM=''; C_GREEN=''; C_BLUE=''; C_YELLOW=''; C_RED=''; C_RESET=''
fi

note() { [[ $QUIET -eq 1 ]] || printf '%s\n' "$*"; }
ok()   { [[ $QUIET -eq 1 ]] || printf '  %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
fail() { printf '  %s✗%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
hdr()  { [[ $QUIET -eq 1 ]] || printf '\n%s%s%s\n' "$C_BOLD" "$*" "$C_RESET"; }

# call <method> <path> [body-json]
call() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sS -o /dev/null -w '%{http_code}' -X "$method" "$BASE$path" \
      -H 'content-type: application/json' --data-raw "$body"
  else
    curl -sS -o /dev/null -w '%{http_code}' -X "$method" "$BASE$path"
  fi
}

# raw_put <path> <content-type> <body>
raw_put() {
  local path="$1" ct="$2" body="$3"
  curl -sS -o /dev/null -w '%{http_code}' -X PUT "$BASE$path" \
    -H "content-type: $ct" --data-binary "$body"
}

b64() { printf '%s' "$1" | base64 | tr -d '\n'; }

# precondition — emulator must be up
note "${C_BOLD}seeding $BASE${C_RESET} ${C_DIM}(project=$PROJECT)${C_RESET}"
if ! curl -sS -o /dev/null -m 2 "$BASE/healthz"; then
  fail "emulator not reachable at $BASE — start it with 'gcp-local start' first"
  exit 1
fi

# ---------------------------------------------------------------------------
# Cloud Logging — ~60 entries across several logs, severities, and resources
# ---------------------------------------------------------------------------
hdr "Cloud Logging"
LOG_NAMES=(
  "projects/$PROJECT/logs/api-gateway"
  "projects/$PROJECT/logs/billing-worker"
  "projects/$PROJECT/logs/checkout"
  "projects/$PROJECT/logs/auth"
  "projects/$PROJECT/logs/ingest"
)
SEVERITIES=(DEBUG INFO INFO INFO NOTICE WARNING WARNING ERROR ERROR CRITICAL)
MESSAGES=(
  "boot complete; listening on :8080"
  "loaded config from /etc/app/config.yaml"
  "user u_42 logged in"
  "user u_1337 logged in"
  "user u_42 logged out"
  "cache miss for key=product:1042 — fetching"
  "issued JWT exp=3600s sub=u_42"
  "order o_2841 created total=42.10 currency=USD"
  "order o_2842 created total=128.00 currency=USD"
  "stripe charge ch_3X9Z succeeded"
  "stripe charge ch_3X9A failed: card_declined"
  "retry attempt 2/5 for task t_117"
  "task t_117 dispatched to worker-3"
  "queue backlog depth=42 lag=2.3s"
  "deprecation: env var FOO_BAR will be removed in v2.0"
  "rate limit: 429 from upstream stripe.com retry-after=2s"
  "graceful shutdown signal received, draining"
  "GC pause 412ms heap=482MB"
  "schema migration 0042_add_index applied in 1.2s"
  "panic recovered in handler /v1/orders: nil deref at orders.go:184"
  "request POST /v1/orders status=201 dur=124ms ip=10.0.0.42"
  "request GET /v1/users/u_42 status=200 dur=18ms ip=10.0.0.91"
  "request DELETE /v1/sessions/s_551 status=404 dur=4ms ip=10.0.0.91"
  "request POST /v1/orders status=400 dur=8ms ip=10.0.0.5"
  "outbound POST stripe.com/v1/charges status=200 dur=412ms"
)
total_logs=0
for log in "${LOG_NAMES[@]}"; do
  entries=""
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    sev=${SEVERITIES[$RANDOM % ${#SEVERITIES[@]}]}
    msg=${MESSAGES[$RANDOM % ${#MESSAGES[@]}]}
    [[ -n "$entries" ]] && entries+=','
    entries+="{\"severity\":\"$sev\",\"textPayload\":\"$msg\"}"
    total_logs=$((total_logs+1))
  done
  code=$(call POST /v2/entries:write "{\"logName\":\"$log\",\"resource\":{\"type\":\"global\"},\"entries\":[$entries]}")
  if [[ "$code" == 200 ]]; then
    ok "$log → 12 entries"
  else
    fail "$log → HTTP $code"
  fi
done
note "  ${C_DIM}wrote $total_logs entries total${C_RESET}"

# ---------------------------------------------------------------------------
# Cloud Storage — 5 buckets, mixed content
# ---------------------------------------------------------------------------
hdr "Cloud Storage"
BUCKETS=(app-assets user-uploads invoices logs-archive product-images)
for b in "${BUCKETS[@]}"; do
  code=$(call POST "/storage/v1/b?project=$PROJECT" "{\"name\":\"$b\"}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then
    ok "bucket $b"
  else
    fail "bucket $b → HTTP $code"
  fi
done

# Parallel arrays: OBJ_BUCKET[i] / OBJ_PATH[i] / OBJ_CT[i] / OBJ_BODY[i].
OBJ_BUCKET=(); OBJ_PATH=(); OBJ_CT=(); OBJ_BODY=()
add_obj() { OBJ_BUCKET+=("$1"); OBJ_PATH+=("$2"); OBJ_CT+=("$3"); OBJ_BODY+=("$4"); }

add_obj app-assets       "styles.css"                  "text/css"             "body { font-family: system-ui; margin: 0; }"
add_obj app-assets       "main.js"                     "text/javascript"      "console.log('app booted')"
add_obj app-assets       "index.html"                  "text/html"            "<!doctype html><title>app</title><h1>Welcome</h1>"
add_obj app-assets       "favicon.txt"                 "text/plain"           "placeholder favicon"
add_obj user-uploads     "2026/05/avatar-u42.txt"      "text/plain"           "fake binary avatar bytes"
add_obj user-uploads     "2026/05/avatar-u1337.txt"    "text/plain"           "fake binary avatar bytes"
add_obj user-uploads     "2026/05/cover-1.txt"         "text/plain"           "fake cover image"
add_obj invoices         "2026-04.json"                "application/json"     '{"month":"2026-04","total":12450.00,"currency":"USD","lines":42}'
add_obj invoices         "2026-05.json"                "application/json"     '{"month":"2026-05","total":14820.00,"currency":"USD","lines":51}'
add_obj invoices         "README.md"                   "text/markdown"        "# Invoices

Monthly summaries written by the billing worker."
add_obj logs-archive     "2026-05-13.ndjson"           "application/x-ndjson" '{"sev":"INFO","msg":"boot"}
{"sev":"INFO","msg":"ready"}'
add_obj logs-archive     "2026-05-14.ndjson"           "application/x-ndjson" '{"sev":"WARNING","msg":"slow query 2.1s"}'
add_obj product-images   "sku-001.txt"                 "text/plain"           "stub for product image binary"
add_obj product-images   "sku-002.txt"                 "text/plain"           "stub for product image binary"
add_obj product-images   "manifest.json"               "application/json"     '{"skus":["001","002"],"updated":"2026-05-15"}'

obj_ok=0
for i in "${!OBJ_BUCKET[@]}"; do
  code=$(raw_put "/${OBJ_BUCKET[$i]}/${OBJ_PATH[$i]}" "${OBJ_CT[$i]}" "${OBJ_BODY[$i]}")
  if [[ "$code" == 200 ]]; then obj_ok=$((obj_ok+1)); fi
done
ok "objects uploaded: $obj_ok / ${#OBJ_BUCKET[@]}"

# ---------------------------------------------------------------------------
# Pub/Sub — 5 topics, ~9 subscriptions, ~60 messages
# ---------------------------------------------------------------------------
hdr "Pub/Sub"
# subs_for <topic> echoes space-separated subscription names
subs_for() {
  case "$1" in
    orders)    echo "orders-fulfillment orders-analytics" ;;
    payments)  echo "payments-ledger payments-fraud" ;;
    shipments) echo "shipments-tracker" ;;
    emails)    echo "emails-relay" ;;
    analytics) echo "analytics-warehouse analytics-debug analytics-raw" ;;
  esac
}
TOPICS=(orders payments shipments emails analytics)
for t in "${TOPICS[@]}"; do
  code=$(call PUT "/v1/projects/$PROJECT/topics/$t")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "topic $t"; else fail "topic $t → $code"; fi
  for sub in $(subs_for "$t"); do
    code=$(call PUT "/v1/projects/$PROJECT/subscriptions/$sub" \
      "{\"topic\":\"projects/$PROJECT/topics/$t\"}")
    if [[ "$code" == 200 || "$code" == 409 ]]; then ok "  sub $sub → $t"; else fail "  sub $sub → $code"; fi
  done
done

msg_count=0
for t in "${TOPICS[@]}"; do
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    payload=$(b64 "{\"$t\":\"event-$i\",\"id\":\"${t}_$((1000+i))\",\"ts\":\"2026-05-15T17:0$((i%10)):00Z\"}")
    call POST "/v1/projects/$PROJECT/topics/$t:publish" \
      "{\"messages\":[{\"data\":\"$payload\"}]}" >/dev/null
    msg_count=$((msg_count+1))
  done
done
ok "published $msg_count messages"

# ---------------------------------------------------------------------------
# Secret Manager — 10 secrets, each with 1–3 versions
# ---------------------------------------------------------------------------
hdr "Secret Manager"
SECRETS=(stripe-api-key sendgrid-api-key db-password jwt-signing-key
         oauth-google-client-secret oauth-github-client-secret
         redis-password aws-access-key webhook-signing-secret slack-bot-token)
for s in "${SECRETS[@]}"; do
  code=$(call POST "/v1/projects/$PROJECT/secrets?secretId=$s" "{}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "secret $s"; else fail "secret $s → $code"; fi
  versions=$((1 + RANDOM % 3))
  for v in $(seq 1 $versions); do
    payload=$(b64 "value-v${v}-$(date +%s)-$RANDOM")
    call POST "/v1/projects/$PROJECT/secrets/$s:addVersion" \
      "{\"payload\":{\"data\":\"$payload\"}}" >/dev/null
  done
  note "    ${C_DIM}+ $versions version(s)${C_RESET}"
done

# ---------------------------------------------------------------------------
# Cloud Tasks — 3 queues with 6 tasks each (no real target so they retry then drop)
# ---------------------------------------------------------------------------
hdr "Cloud Tasks"
QUEUES=(default emails-out webhooks-out)
for q in "${QUEUES[@]}"; do
  code=$(call POST "/v2/projects/$PROJECT/locations/us-central1/queues" "{\"name\":\"$q\"}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "queue $q"; else fail "queue $q → $code"; fi
  for i in 1 2 3 4 5 6; do
    schedule_offset=$((i * 60))
    # macOS and GNU date have different flags; try both.
    schedule_time=$(date -u -v+${schedule_offset}M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
                 || date -u -d "+${schedule_offset} minutes" +%Y-%m-%dT%H:%M:%SZ)
    body_b64=$(b64 "{\"queue\":\"$q\",\"seq\":$i}")
    call POST "/v2/projects/$PROJECT/locations/us-central1/queues/$q/tasks" \
      "{\"task\":{\"scheduleTime\":\"$schedule_time\",\"httpRequest\":{\"url\":\"https://example.invalid/$q/$i\",\"httpMethod\":\"POST\",\"body\":\"$body_b64\",\"headers\":{\"X-Seq\":\"$i\"}}}}" >/dev/null
  done
  ok "  6 staggered tasks"
done

# ---------------------------------------------------------------------------
# Cloud Scheduler — 6 cron jobs with mixed schedules
# ---------------------------------------------------------------------------
hdr "Cloud Scheduler"
# Parallel arrays for jobs.
JOB_NAME=(nightly-rollup hourly-cleanup weekday-report every-five-min every-minute monday-mailout)
JOB_SCHEDULE=("0 2 * * *" "0 * * * *" "0 9 * * 1-5" "@every 5m" "@every 1m" "0 10 * * 1")
JOB_URI=(
  "https://example.invalid/cron/nightly-rollup"
  "https://example.invalid/cron/hourly-cleanup"
  "https://example.invalid/cron/weekday-report"
  "https://example.invalid/cron/heartbeat"
  "https://example.invalid/cron/poll"
  "https://example.invalid/cron/mailout"
)
for i in "${!JOB_NAME[@]}"; do
  name="${JOB_NAME[$i]}"
  code=$(call POST "/v1/projects/$PROJECT/locations/us-central1/jobs" \
    "{\"name\":\"projects/$PROJECT/locations/us-central1/jobs/$name\",\"schedule\":\"${JOB_SCHEDULE[$i]}\",\"httpTarget\":{\"uri\":\"${JOB_URI[$i]}\",\"httpMethod\":\"GET\"}}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then
    ok "job $name → ${JOB_SCHEDULE[$i]}"
  else
    fail "job $name → $code"
  fi
done

# ---------------------------------------------------------------------------
# Firestore — gRPC-only, uses the real SDK via a tiny Go helper
# ---------------------------------------------------------------------------
hdr "Firestore"
EMULATOR_HOST=$(printf '%s' "$BASE" | sed -E 's,^https?://,,')
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if (cd "$REPO_ROOT" && go run ./scripts/seed_firestore -addr "$EMULATOR_HOST" -project "$PROJECT" $([[ $QUIET -eq 1 ]] && echo -quiet) 2>&1); then
  ok "firestore seeded (users / products / orders / sessions / audit_logs + subcollection)"
else
  fail "firestore seed failed — is gRPC reachable at $EMULATOR_HOST?"
fi

# ---------------------------------------------------------------------------
# Cloud KMS — 3 keyrings, 6 keys
# ---------------------------------------------------------------------------
hdr "Cloud KMS"
keys_for() {
  case "$1" in
    app-keys)    echo "api-token-key user-token-key session-key" ;;
    data-keys)   echo "pii-encryption-key payment-encryption-key" ;;
    backup-keys) echo "backup-key" ;;
  esac
}
RINGS=(app-keys data-keys backup-keys)
for ring in "${RINGS[@]}"; do
  code=$(call POST "/v1/projects/$PROJECT/locations/global/keyRings?keyRingId=$ring")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "keyring $ring"; else fail "keyring $ring → $code"; fi
  for k in $(keys_for "$ring"); do
    code=$(call POST "/v1/projects/$PROJECT/locations/global/keyRings/$ring/cryptoKeys?cryptoKeyId=$k" \
      "{\"purpose\":\"ENCRYPT_DECRYPT\"}")
    if [[ "$code" == 200 || "$code" == 409 ]]; then ok "  key $k"; else fail "  key $k → $code"; fi
  done
done

# ---------------------------------------------------------------------------
# Cloud Monitoring — a handful of time series
# ---------------------------------------------------------------------------
hdr "Cloud Monitoring"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
for metric in custom.googleapis.com/api/request_count custom.googleapis.com/db/connection_pool custom.googleapis.com/queue/depth custom.googleapis.com/cache/hit_ratio; do
  for v in 12 27 42 88 153; do
    code=$(call POST "/v3/projects/$PROJECT/timeSeries" \
      "{\"timeSeries\":[{\"metric\":{\"type\":\"$metric\"},\"resource\":{\"type\":\"global\"},\"metricKind\":\"GAUGE\",\"valueType\":\"INT64\",\"points\":[{\"interval\":{\"endTime\":\"$NOW\"},\"value\":{\"int64Value\":\"$v\"}}]}]}")
    [[ "$code" != 200 ]] && fail "timeseries $metric=$v → $code"
  done
  ok "$metric (5 points)"
done

# ---------------------------------------------------------------------------
# BigQuery — 2 datasets, 4 tables, sample rows
# ---------------------------------------------------------------------------
hdr "BigQuery"
for ds in analytics warehouse; do
  code=$(call POST "/bigquery/v2/projects/$PROJECT/datasets" "{\"datasetReference\":{\"datasetId\":\"$ds\"}}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "dataset $ds"; else fail "dataset $ds → $code"; fi
done

# table: analytics.events
call POST "/bigquery/v2/projects/$PROJECT/datasets/analytics/tables" \
  '{"tableReference":{"tableId":"events"},"schema":{"fields":[{"name":"event_id","type":"STRING"},{"name":"user_id","type":"STRING"},{"name":"action","type":"STRING"},{"name":"ts","type":"TIMESTAMP"}]}}' >/dev/null
ok "table analytics.events"
# rows
rows='{"rows":['
for i in 1 2 3 4 5 6 7 8 9 10; do
  [[ $i -gt 1 ]] && rows+=','
  rows+="{\"insertId\":\"e$i\",\"json\":{\"event_id\":\"evt_$i\",\"user_id\":\"u_$((1000+i))\",\"action\":\"click\",\"ts\":\"2026-05-15T17:0$((i%10)):00Z\"}}"
done
rows+=']}'
call POST "/bigquery/v2/projects/$PROJECT/datasets/analytics/tables/events/insertAll" "$rows" >/dev/null
ok "  10 rows inserted"

# table: analytics.users
call POST "/bigquery/v2/projects/$PROJECT/datasets/analytics/tables" \
  '{"tableReference":{"tableId":"users"},"schema":{"fields":[{"name":"id","type":"STRING"},{"name":"name","type":"STRING"},{"name":"signups","type":"INTEGER"}]}}' >/dev/null
ok "table analytics.users"

# table: warehouse.orders
call POST "/bigquery/v2/projects/$PROJECT/datasets/warehouse/tables" \
  '{"tableReference":{"tableId":"orders"},"schema":{"fields":[{"name":"order_id","type":"STRING"},{"name":"total","type":"FLOAT"},{"name":"status","type":"STRING"}]}}' >/dev/null
ok "table warehouse.orders"

# table: warehouse.products
call POST "/bigquery/v2/projects/$PROJECT/datasets/warehouse/tables" \
  '{"tableReference":{"tableId":"products"},"schema":{"fields":[{"name":"sku","type":"STRING"},{"name":"price","type":"FLOAT"}]}}' >/dev/null
ok "table warehouse.products"

# ---------------------------------------------------------------------------
# Cloud SQL — 3 Postgres-wire instances on staggered ports
# ---------------------------------------------------------------------------
hdr "Cloud SQL"
# Ask the emulator to allocate ephemeral ports (port=0) so the seed doesn't
# clash with anything already bound on the host or with prior runs.
for inst in app-primary analytics-replica reporting-db; do
  code=$(call POST "/sql/v1beta4/projects/$PROJECT/instances" \
    "{\"name\":\"$inst\",\"engine\":\"sqlite\",\"database\":\"app\",\"port\":0}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "instance $inst"; else fail "instance $inst → $code"; fi
done

# ---------------------------------------------------------------------------
# Cloud Run — 4 services, each with a fake backendUrl
# ---------------------------------------------------------------------------
hdr "Cloud Run"
RUN_SVCS=(api-service worker-service web-frontend image-resizer)
for svc in "${RUN_SVCS[@]}"; do
  code=$(call POST "/v2/projects/$PROJECT/locations/us-central1/services" \
    "{\"name\":\"projects/$PROJECT/locations/us-central1/services/$svc\",\"backendUrl\":\"https://example.invalid/$svc\"}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "service $svc"; else fail "service $svc → $code"; fi
done

# ---------------------------------------------------------------------------
# Cloud Functions — 4 functions
# ---------------------------------------------------------------------------
hdr "Cloud Functions"
FUNCS=(thumbnail-generator email-sender stripe-webhook user-cleanup)
for fn in "${FUNCS[@]}"; do
  code=$(call POST "/v2/projects/$PROJECT/locations/us-central1/functions" \
    "{\"name\":\"projects/$PROJECT/locations/us-central1/functions/$fn\",\"backendUrl\":\"https://example.invalid/fn/$fn\"}")
  if [[ "$code" == 200 || "$code" == 409 ]]; then ok "function $fn"; else fail "function $fn → $code"; fi
done

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
hdr "Done"
note "open ${C_BOLD}$BASE/console${C_RESET} to browse the seeded state"
