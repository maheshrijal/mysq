#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
binary="${MYSQ_BENCHMARK_BINARY:-$work_dir/mysq}"
port="${MYSQ_BENCHMARK_PORT:-33307}"
project="mysq-benchmark"
monitor_dsn="mysq_monitor:mysq-monitor-test@tcp(127.0.0.1:${port})/app?parseTime=true"
load_dsn="loadgen:mysq-load-test@tcp(127.0.0.1:${port})/app?parseTime=true"
compose=(docker compose --project-name "$project" -f "$repo_root/docker-compose.e2e.yml")
load_pid=""

cleanup() {
  if [[ -n "$load_pid" ]]; then
    kill "$load_pid" >/dev/null 2>&1 || true
    wait "$load_pid" >/dev/null 2>&1 || true
  fi
  MYSQ_MYSQL_PORT="$port" "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

cd "$repo_root"
if [[ -z "${MYSQ_BENCHMARK_BINARY:-}" ]]; then
  go build -trimpath -ldflags "-X main.version=benchmark" -o "$binary" ./cmd/mysq
fi
MYSQ_MYSQL_PORT="$port" "${compose[@]}" up -d --wait

go run ./test/e2e/load --dsn "$load_dsn" --duration 2m >"$work_dir/load.log" 2>&1 &
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

MYSQ_BENCHMARK_DSN="$monitor_dsn" go run ./test/benchmark --binary "$binary" "$@"
