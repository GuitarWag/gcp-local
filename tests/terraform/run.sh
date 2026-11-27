#!/usr/bin/env bash
# Terraform acceptance harness for gcp-local.
#
# Builds the emulator, runs terraform init/plan/apply/destroy against it
# with the hashicorp/google provider pinned in versions.tf, verifies the
# resources with verify.py, and records the known Cloud Run gap.
#
# Usage: tests/terraform/run.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TF_DIR="$REPO_ROOT/tests/terraform"
CLOUDRUN_DIR="$TF_DIR/cloudrun"
PYTHON="$TF_DIR/../python/.venv/bin/python"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

[ -x "$PYTHON" ] || fail "tests/python venv not found; run: python3 -m venv tests/python/.venv && tests/python/.venv/bin/pip install -r tests/python/requirements.txt"
command -v go >/dev/null 2>&1 || fail "go not found on PATH"
command -v terraform >/dev/null 2>&1 || fail "terraform not found on PATH"

TMP=""
EMULATOR_PID=""
cleanup() {
    if [ -n "$EMULATOR_PID" ]; then
        kill "$EMULATOR_PID" 2>/dev/null || true
        wait "$EMULATOR_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP"
}
trap cleanup EXIT

# --- build -----------------------------------------------------------------

echo "=== build"
TMP="$(mktemp -d)"
go build -o "$TMP/gcp-local" "$REPO_ROOT/cmd/gcp-local" \
    || fail "go build of ./cmd/gcp-local failed"

# --- start the emulator ------------------------------------------------------

echo "=== start emulator"
PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
"$TMP/gcp-local" start --port="$PORT" --no-daemon &
EMULATOR_PID=$!

up=0
for _ in $(seq 1 60); do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$PORT/healthz")" = "200" ]; then
        up=1
        break
    fi
    sleep 0.5
done
[ "$up" = "1" ] || fail "emulator did not become healthy on port $PORT within 30s"
echo "emulator healthy on port $PORT (pid $EMULATOR_PID)"

# --- environment -------------------------------------------------------------

export TF_VAR_port="$PORT"
export TF_VAR_project="test-project"
export GOOGLE_OAUTH_ACCESS_TOKEN="fake-token"
export GCP_LOCAL_HOST="localhost:$PORT"

declare -a PASS_LINES=()
declare -a GAP_LINES=()

record_pass() {
    PASS_LINES+=("PASS $1")
    echo "PASS $1"
}
record_gap() {
    GAP_LINES+=("GAP  $1")
    echo "GAP  $1"
}

tf() {
    terraform -chdir="$1" "${@:2}"
}

# --- core harness -------------------------------------------------------------

echo "=== terraform core"
tf "$TF_DIR" init -input=false -no-color >>"$TMP/tf-core-init.log" 2>&1 \
    || { cat "$TMP/tf-core-init.log"; fail "terraform init failed in tests/terraform"; }

plan_log="$TMP/tf-core-plan.log"
# -detailed-exitcode: 0 = no changes, 2 = changes present. plan_rc=2 is the
# expected outcome, so disable errexit around the call.
set +e
tf "$TF_DIR" plan -input=false -no-color -detailed-exitcode >"$plan_log" 2>&1
plan_rc=$?
set -e
[ "$plan_rc" = "2" ] || { cat "$plan_log"; fail "terraform plan did not report changes (rc=$plan_rc)"; }

add_count="$(awk '/^Plan: /{print $2}' "$plan_log")"
[ "$add_count" = "4" ] || { cat "$plan_log"; fail "terraform plan expected '4 to add', got '$add_count'"; }
record_pass "terraform plan: 4 to add"

tf "$TF_DIR" apply -auto-approve -input=false -no-color >>"$TMP/tf-core-apply.log" 2>&1 \
    || { cat "$TMP/tf-core-apply.log"; fail "terraform apply failed in tests/terraform"; }
record_pass "terraform apply: 4 resources created"

"$PYTHON" "$TF_DIR/verify.py" present \
    || fail "verify.py present failed after apply"
record_pass "verify present: all 4 resources"

tf "$TF_DIR" destroy -auto-approve -input=false -no-color >>"$TMP/tf-core-destroy.log" 2>&1 \
    || { cat "$TMP/tf-core-destroy.log"; fail "terraform destroy failed in tests/terraform"; }
record_pass "terraform destroy: 4 resources removed"

"$PYTHON" "$TF_DIR/verify.py" absent \
    || fail "verify.py absent failed after destroy"
record_pass "verify absent: all 4 resources gone"

# --- Cloud Run known gap -------------------------------------------------------

echo "=== terraform cloudrun (known gap #94)"
tf "$CLOUDRUN_DIR" init -input=false -no-color >>"$TMP/tf-run-init.log" 2>&1 \
    || { cat "$TMP/tf-run-init.log"; fail "terraform init failed in tests/terraform/cloudrun"; }

if tf "$CLOUDRUN_DIR" apply -auto-approve -input=false -no-color >>"$TMP/tf-run-apply.log" 2>&1; then
    echo "UNEXPECTED: Cloud Run apply succeeded; the emulator changed under us." >&2
    cat "$TMP/tf-run-apply.log"
    exit 1
fi
rm -f "$CLOUDRUN_DIR/terraform.tfstate"
record_gap "Cloud Run apply fails: emulator serves only /v2, no operation object (issue #94)"

# --- summary --------------------------------------------------------------------

echo
echo "=== summary"
for line in "${PASS_LINES[@]}"; do echo "$line"; done
for line in "${GAP_LINES[@]}"; do echo "$line"; done

exit 0
