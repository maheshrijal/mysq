#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
binary="$work_dir/mysq"
port="${MYSQ_MYSQL_PORT:-33306}"
monitor_dsn="mysq_monitor:mysq-monitor-test@tcp(127.0.0.1:${port})/app?parseTime=true"
load_dsn="loadgen:mysq-load-test@tcp(127.0.0.1:${port})/app?parseTime=true"
compose=(docker compose -f "$repo_root/docker-compose.e2e.yml")
load_pid=""

cleanup() {
  if [[ -n "$load_pid" ]]; then
    kill "$load_pid" >/dev/null 2>&1 || true
    wait "$load_pid" >/dev/null 2>&1 || true
  fi
  "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

cd "$repo_root"
go build -trimpath -ldflags "-X main.version=e2e" -o "$binary" ./cmd/mysq
"${compose[@]}" up -d --wait

go run ./test/e2e/load --dsn "$load_dsn" --duration 45s >"$work_dir/load.log" 2>&1 &
load_pid=$!
sleep 7

MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --full --no-store >"$work_dir/full.txt"
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --format json --no-store >"$work_dir/context.json"
go run ./test/e2e/verify --context "$work_dir/context.json"
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --format markdown --no-store >"$work_dir/report.md"

set +e
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --fail-on critical --no-store >"$work_dir/gate.txt" 2>"$work_dir/gate.err"
gate_code=$?
set -e
if [[ "$gate_code" -ne 2 ]]; then
  echo "expected critical health gate exit 2, got $gate_code" >&2
  exit 1
fi

for section in queries tables indexes processes transactions locks metadata-locks waits io errors memory engine coverage variables replication; do
  MYSQ_DATABASE_URL="$monitor_dsn" "$binary" "$section" --json --interval 250ms >"$work_dir/${section}.json"
done

bundle_dir="$work_dir/agent-bundle"
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" export --out "$bundle_dir" --zip --interval 250ms
go run ./test/e2e/verify --bundle "$bundle_dir"
test -s "$bundle_dir.zip"

history_dir="$work_dir/history"
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --format json --store "$history_dir" --interval 250ms >"$work_dir/history-1.json"
sleep 2
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --format json --store "$history_dir" --interval 250ms >"$work_dir/history-2.json"
"$binary" snapshots list --store "$history_dir" >"$work_dir/snapshots.txt"
"$binary" diff --store "$history_dir" --since 1s >"$work_dir/diff.txt"

"$binary" init --user observer >"$work_dir/init.sql"
"$binary" --help >"$work_dir/help.txt"
"$binary" --version >"$work_dir/version.txt"

cd "$work_dir"
expect "$repo_root/test/e2e/tui.exp" "$binary" "$monitor_dsn" >"$work_dir/tui.log" 2>&1
find "$work_dir" -maxdepth 1 -type d -name 'mysq-export-*' | grep -q .

grep -q "Database health" "$work_dir/full.txt"
grep -q "SUBSYSTEM BOARD" "$work_dir/full.txt"
grep -q "# mysq report" "$work_dir/report.md"
grep -q "CREATE USER" "$work_dir/init.sql"
grep -q "mysq" "$work_dir/help.txt"
grep -q "e2e" "$work_dir/version.txt"
grep -q "Health score" "$work_dir/diff.txt"

echo "mysq e2e passed: Docker MySQL, concurrent load, every CLI command, history/diff, agent export, CI gate, and interactive TUI"
