#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
binary="${MYSQ_BENCHMARK_BINARY:-$work_dir/mysq}"
baseline="${MYSQ_BENCHMARK_BASELINE:-}"
requested_port="${MYSQ_BENCHMARK_PORT:-0}"
project="mysq-benchmark-$$-$RANDOM"
compose=(docker compose --project-name "$project" -f "$repo_root/docker-compose.e2e.yml")
load_pid=""
runner_pid=""

cleanup() {
  if [[ -n "$runner_pid" ]]; then
    kill "$runner_pid" >/dev/null 2>&1 || true
    wait "$runner_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$load_pid" ]]; then
    kill "$load_pid" >/dev/null 2>&1 || true
    wait "$load_pid" >/dev/null 2>&1 || true
  fi
  MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

cd "$repo_root"
if [[ -z "${MYSQ_BENCHMARK_BINARY:-}" ]]; then
  go build -trimpath -ldflags "-X main.version=benchmark" -o "$binary" ./cmd/mysq
fi
go build -trimpath -o "$work_dir/load" ./test/e2e/load
go build -trimpath -o "$work_dir/runner" ./test/benchmark
MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" up -d --wait --force-recreate
published_address="$(MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" port mysql 3306)"
port="${published_address##*:}"
if [[ ! "$port" =~ ^[0-9]+$ ]]; then
  echo "could not resolve benchmark MySQL port from: $published_address" >&2
  exit 1
fi
monitor_dsn="mysq_monitor:mysq-monitor-test@tcp(127.0.0.1:${port})/app?parseTime=true"
load_dsn="loadgen:mysq-load-test@tcp(127.0.0.1:${port})/app?parseTime=true"

# The supervisor below owns this process and stops it when the benchmark exits.
# A one-year deadline is only a final leak guard, not the benchmark lifetime.
"$work_dir/load" --dsn "$load_dsn" --duration 8760h >"$work_dir/load.log" 2>&1 &
load_pid=$!
load_ready=false
for _ in {1..120}; do
  if grep -q '^load ready$' "$work_dir/load.log"; then
    load_ready=true
    break
  fi
  if ! kill -0 "$load_pid" >/dev/null 2>&1; then
    wait "$load_pid" || true
    cat "$work_dir/load.log" >&2
    echo "load generator exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.5
done
if [[ "$load_ready" != true ]]; then
  cat "$work_dir/load.log" >&2
  echo "timed out waiting for benchmark load" >&2
  exit 1
fi

runner=(--binary "$binary")
if [[ -n "$baseline" ]]; then
  runner+=(--baseline "$baseline")
fi
MYSQ_BENCHMARK_DSN="$monitor_dsn" "$work_dir/runner" "${runner[@]}" "$@" &
runner_pid=$!
while kill -0 "$runner_pid" >/dev/null 2>&1; do
  if ! kill -0 "$load_pid" >/dev/null 2>&1; then
    wait "$load_pid" || true
    load_pid=""
    cat "$work_dir/load.log" >&2
    echo "benchmark workload exited before the benchmark completed" >&2
    exit 1
  fi
  sleep 0.25
done

set +e
wait "$runner_pid"
runner_status=$?
set -e
runner_pid=""
if [[ "$runner_status" -ne 0 ]]; then
  exit "$runner_status"
fi
if ! kill -0 "$load_pid" >/dev/null 2>&1; then
  wait "$load_pid" || true
  load_pid=""
  cat "$work_dir/load.log" >&2
  echo "benchmark workload exited before the benchmark completed" >&2
  exit 1
fi
