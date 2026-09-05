#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
binary="$work_dir/mysq"
load_binary="$work_dir/load"
requested_port="${MYSQ_MYSQL_PORT:-0}"
project="${MYSQ_E2E_PROJECT:-mysq-e2e-$$-$RANDOM}"
compose=(docker compose --project-name "$project" -f "$repo_root/docker-compose.e2e.yml")
load_pid=""
connection_pid=""

stop_load() {
  if [[ -n "$load_pid" ]]; then
    kill "$load_pid" >/dev/null 2>&1 || true
    wait "$load_pid" >/dev/null 2>&1 || true
    load_pid=""
  fi
}

cleanup() {
  stop_load
  MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  if [[ -n "$connection_pid" ]]; then wait "$connection_pid" || true; fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

cd "$repo_root"
go build -trimpath -ldflags "-X main.version=e2e" -o "$binary" ./cmd/mysq
go build -trimpath -o "$load_binary" ./test/e2e/load
MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" up -d --wait --force-recreate
mysql_container="$(MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" ps -q mysql)"
if [[ -z "$mysql_container" ]]; then
  echo "could not resolve e2e MySQL container" >&2
  exit 1
fi
published_address="$(MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" port mysql 3306)"
port="${published_address##*:}"
if [[ ! "$port" =~ ^[0-9]+$ ]]; then
  echo "could not resolve e2e MySQL port from: $published_address" >&2
  exit 1
fi
monitor_dsn="mysq_monitor:mysq-monitor-test@tcp(127.0.0.1:${port})/app?parseTime=true"
load_dsn="loadgen:mysq-load-test@tcp(127.0.0.1:${port})/app?parseTime=true"

"$load_binary" --dsn "$load_dsn" --duration 45s >"$work_dir/load.log" 2>&1 &
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
  echo "timed out waiting for deterministic load fixture" >&2
  exit 1
fi
sleep 2

DBOPS_MYSQL_USER=mysq_monitor DBOPS_MYSQL_PWD=mysq-monitor-test "$binary" inspect "127.0.0.1:${port}/app" --full --no-store >"$work_dir/full.txt"
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

for section in queries tables indexes processes transactions blockers locks metadata-locks waits io errors memory engine coverage variables replication; do
  MYSQ_DATABASE_URL="$monitor_dsn" "$binary" "$section" --json --interval 250ms >"$work_dir/${section}.json"
done
go run ./test/e2e/verify --focused-dir "$work_dir"

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

# The workload has already exercised collection, focused commands, export, and
# history. Stop it before the navigation-only PTY phase so hosted-runner load
# cannot starve Bubble Tea's initial full refresh.
stop_load

MYSQ_E2E_LOAD_DSN="$load_dsn" MYSQ_E2E_MONITOR_DSN="$monitor_dsn" go test ./internal/collect -run 'TestFixture(SleepingMetadataOwner|TrendSampler|ActiveUsersBeyondSessionLimit)$' -count=1
MYSQ_E2E_LOAD_DSN="$load_dsn" MYSQ_E2E_MONITOR_DSN="$monitor_dsn" MYSQ_E2E_CONTROL_DSN="mysq_operator:mysq-operator-test@tcp(127.0.0.1:${port})/app?parseTime=true" go test ./internal/control -race -run 'TestFixtureKill(Query|Connection)$' -count=1

# With no application client active, a full inspection must not count its own
# digest-sampler SELECTs as workload inside the global-status window. Briefly
# pause the recurring liveness query and let any in-flight check finish first.
docker exec "$mysql_container" touch /tmp/mysq-health-paused
# The healthcheck timeout is 3s; four seconds guarantees an already-running
# mysqladmin invocation has finished before the zero-question window opens.
sleep 4
MYSQ_DATABASE_URL="$monitor_dsn" "$binary" inspect --format json --no-store --interval 250ms >"$work_dir/idle-context.json"
go run ./test/e2e/verify --idle-context "$work_dir/idle-context.json"
docker exec "$mysql_container" rm -f /tmp/mysq-health-paused

# Drive the real typed-confirmation flow against one fixture-owned client.
# --unbuffered exposes its ID before the long statement, and --skip-reconnect
# proves that KILL CONNECTION closes that exact session.
docker exec -e MYSQL_PWD=mysq-load-test "$mysql_container" mysql --user=loadgen --database=app --batch --skip-column-names --unbuffered --skip-reconnect --execute='SELECT CONNECTION_ID(); SELECT 1 WHERE SLEEP(90)=0' >"$work_dir/connection.log" 2>"$work_dir/connection.err" &
connection_pid=$!
connection_id=""
for _ in {1..100}; do
  connection_id="$(sed -n '1p' "$work_dir/connection.log")"
  if [[ "$connection_id" =~ ^[0-9]+$ ]]; then break; fi
  sleep 0.1
done
if [[ ! "$connection_id" =~ ^[0-9]+$ ]]; then
  cat "$work_dir/connection.err" >&2
  echo "fixture connection did not publish its ID" >&2
  exit 1
fi
set +e
MYSQ_DATABASE_URL="mysq_operator:mysq-operator-test@tcp(127.0.0.1:${port})/app?parseTime=true" expect "$repo_root/test/e2e/tui.exp" "$binary" "127.0.0.1:${port}/app" "$work_dir/connection-pty.log" "$connection_id" >"$work_dir/connection-harness.log" 2>&1
connection_status=$?
set -e
if [[ "$connection_status" -ne 0 ]]; then
  cat "$work_dir/connection-harness.log" "$work_dir/connection-pty.log" >&2
  exit "$connection_status"
fi
if wait "$connection_pid"; then
  echo "fixture connection unexpectedly completed instead of being terminated" >&2
  exit 1
fi
connection_pid=""
echo "verified TUI connection selection, details, rejected confirmation, Esc, and confirmed termination"

"$binary" init --user observer >"$work_dir/init.sql"
"$binary" --help >"$work_dir/help.txt"
"$binary" --version >"$work_dir/version.txt"

cd "$work_dir"
tui_harness_log="$work_dir/tui-harness.log"
tui_pty_log="$work_dir/tui-pty.log"
set +e
MYSQ_DATABASE_URL= MYSQLDOT_DATABASE_URL= DATABASE_URL= DBOPS_MYSQL_USER=mysq_monitor DBOPS_MYSQL_PWD=mysq-monitor-test expect "$repo_root/test/e2e/tui.exp" "$binary" "127.0.0.1:${port}/app" "$tui_pty_log" >"$tui_harness_log" 2>&1
tui_status=$?
set -e
if [[ "$tui_status" -ne 0 ]]; then
  printf 'TUI harness log:\n' >&2
  if [[ -r "$tui_harness_log" ]]; then
    cat "$tui_harness_log" >&2
  else
    printf '(missing: %s)\n' "$tui_harness_log" >&2
  fi
  printf 'TUI PTY log:\n' >&2
  if [[ -r "$tui_pty_log" ]]; then
    cat "$tui_pty_log" >&2
  else
    printf '(missing: %s)\n' "$tui_pty_log" >&2
  fi
  exit "$tui_status"
fi
find "$work_dir" -maxdepth 1 -type d -name 'mysq-export-*' | grep -q .

grep -q "Database health" "$work_dir/full.txt"
grep -q "SUBSYSTEM BOARD" "$work_dir/full.txt"
grep -q "# mysq report" "$work_dir/report.md"
grep -q "CREATE USER" "$work_dir/init.sql"
grep -q "mysq" "$work_dir/help.txt"
grep -q "e2e" "$work_dir/version.txt"
grep -q "Health score" "$work_dir/diff.txt"

echo "mysq e2e passed: Docker MySQL, concurrent load, every CLI command, history/diff, agent export, CI gate, and interactive TUI"
