#!/usr/bin/env bash
# Runs FizzBee on one model. Exit 0 only when the model checker passes.
# fizz exits 0 even when the model fails, so read its output.
set -uo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <model.fizz>" >&2; exit 2; }
command -v fizz >/dev/null || { echo "fizz not found; see docs/factory.md" >&2; exit 2; }

dir=$(cd "$(dirname "$1")" && pwd)
out=$(cd "$dir" && fizz "$(basename "$1")" 2>&1)
code=$?
printf '%s\n' "$out"

[ "$code" -eq 0 ] || exit 1
printf '%s\n' "$out" | grep -q '^FAILED' && exit 1
printf '%s\n' "$out" | grep -q '^Valid Nodes' || exit 1
exit 0
