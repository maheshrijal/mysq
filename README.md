# mysq

MySQL diagnostics for humans and agents.

`mysq` is a single read-only CLI that turns MySQL's own status counters and Performance Schema into a findings-first health report. It has a polished interactive terminal, focused drill-down commands, local history and offline diffs, CI health gates, and a native evidence bundle designed for coding and operations agents.

No collector, web server, cloud account, or database-side objects are required.

```text
◆ MYSQ  MySQL intelligence, from the terminal
────────────────────────────────────────────────────────────────────────────────
connected · 127.0.0.1:3306/app · MySQL 8.4.6 · primary · 1.0s sample

Database health  ━━━━━━━━━━━━━━━━━┄┄┄┄┄┄┄   72/100
qps 812.4  ·  tps 188.7  ·  running 9  ·  connections 18/151  ·  cache 99.86%

CRITICAL
● Transactions are blocked on row locks  4 active InnoDB lock waits were captured.

WARNING
● A statement dominates database time  Digest 0f6a04b1 accounts for 54.2% of captured latency.
● InnoDB tables are missing primary keys  1 table has no primary key.

GOOD
● connections · buffer pool · replication · durability · instrumentation

Details: mysq inspect --full   ·   Agent bundle: mysq export   ·   Interactive: mysq tui
```

## Quick start

Build it from this checkout with Go 1.25 or newer:

```bash
make build
./bin/mysq --help
```

After the repository is published, `go install github.com/maheshrijal/mysq/cmd/mysq@latest` installs the same binary.

Keep credentials out of shell history and the process list:

```bash
export MYSQ_DATABASE_URL='monitor:password@tcp(db.example:3306)/app?tls=true'

mysq inspect
mysq inspect --full
mysq tui
mysq export --zip
```

`mysql://monitor:password@db.example:3306/app?tls=true` URLs are accepted too. A positional connection takes precedence over `MYSQ_DATABASE_URL`, which takes precedence over the legacy `MYSQLDOT_DATABASE_URL` and then `DATABASE_URL`.

## The terminal

`mysq inspect` is the fast, findings-first read. `--full` adds sampled MySQL waits, file-I/O latency, statement tail latency, errors, instrumentation coverage, engine I/O and redo, memory consumers, transactions, tables, connections, locks, replication, and collection details.

`mysq tui` opens the live interactive view:

- Seven navigable views: Overview, Connections, Queries, Engine, Findings, Tables, and Config.
- A restrained adaptive palette that respects light and dark terminal backgrounds; color communicates health instead of decorating every surface.
- A compact browser-style tab strip with live counts and full-width diagnostic content, collapsing to neighboring tabs in narrow split panes.
- Left/Right, Tab/Shift-Tab, and number keys switch views without losing each view's scroll position. Arrow keys are the universal navigation layer; `h`/`j`/`k`/`l`, Page Up/Down, Ctrl-U/D, Home/End, and `g`/`G` provide familiar Vim and pager aliases. In Queries, movement and paging follow the selected statement, Enter opens its full normalized SQL and execution evidence, and Esc returns to the selected row.
- `?` opens complete contextual keyboard help. `/` filters Queries, Tables, Connections, or Findings without changing the captured snapshot; Enter applies, Esc cancels editing, and Esc on a filtered view clears it.
- `r` reruns every diagnostic probe and saves a new local snapshot.
- `e` writes the complete native agent bundle directly from the terminal and keeps its destination visible until dismissed with `Esc`.
- Cards, gauges, tables, findings, and key hints reflow at terminal breakpoints; very small terminals get an explicit resize state instead of a broken layout.

The TUI and static report are two renderers over the same diagnostic snapshot. They cannot disagree about health or findings.

## Agent-native export

```bash
mysq export --out incident-2026-08-22 --zip
# Or collect, render, store, and export in one run:
mysq inspect --full --export-dir incident-2026-08-22
```

An export is written atomically and refuses to overwrite an existing path. It contains:

| Artifact | Purpose |
|---|---|
| `summary.md` | Findings-first narrative for a human or agent |
| `context.json` | Complete versioned diagnostic contract |
| `schema/context-1.4.0.json` | JSON Schema for validation and tool generation |
| `findings.json` / `metrics.json` | Small deterministic reasoning inputs |
| `queries.csv` | Normalized statement digests, tail latency, errors, and cost |
| `statement-samples.csv` | Statements ranked by database time during the collection interval |
| `tables.csv` / `indexes.csv` | Storage, I/O latency, keys, and usage evidence |
| `processes.csv` / `connections.csv` / `locks.csv` | Redacted concurrency snapshot and user/host grouping |
| `transactions.csv` / `metadata-locks.csv` | Active transaction and metadata-lock evidence |
| `wait-events.csv` / `file-io.csv` | Sampled Performance Schema wait and file-I/O pressure |
| `server-errors.csv` / `memory-consumers.csv` | MySQL errors and internal memory allocation |
| `variables.cnf` | Sorted captured configuration; evidence, not an apply file |
| `raw/innodb-status.txt` | Redacted InnoDB monitor output |
| `raw/global-status.json` | End-of-sample counters |
| `raw/capabilities.json` | Exact probe coverage and degradation reasons |
| `raw/instrumentation.json` | Digest capacity, disabled consumers, and lost-event counters |
| `manifest.json` | Media types, descriptions, and SHA-256 for every artifact |

The bundle is secret-free by construction. SQL string and numeric literals are removed before data enters the in-memory snapshot, so later renderers cannot accidentally choose to leak them. DSNs and passwords are never persisted.

## Commands

| Command | What it does |
|---|---|
| `inspect` | Findings-first report; `--full`, JSON, Markdown, CI gate, history, and inline export |
| `tui` | Live interactive terminal with refresh and native export |
| `queries` | Statement digests with p95/p99/max latency, errors, user, and row efficiency |
| `tables` | Table size, estimated rows, I/O latency, and primary-key state |
| `indexes` | Index columns, uniqueness, visibility, and read/write latency |
| `processes` | Redacted process-list snapshot |
| `transactions` | Active InnoDB transactions, age, ownership, rows, and normalized SQL |
| `locks` | Active InnoDB row-lock wait graph edges |
| `metadata-locks` | Active and pending metadata locks with owners and objects |
| `waits` | Sampled wait share and pressure with cumulative context |
| `io` | Sampled MySQL file-I/O throughput and latency |
| `errors` | Sampled and cumulative MySQL server errors and warnings |
| `memory` | Top MySQL memory consumers and high-water allocation |
| `engine` | Sampled InnoDB I/O, redo/checkpoint, buffer, network, and scan metrics |
| `coverage` | Performance Schema consumers, digest capacity, and lost events |
| `variables` | Sorted global configuration |
| `replication` | Receiver, applier and worker state, lag, retries, GTID sets, and redacted errors |
| `export` | Atomic JSON/Markdown/CSV/TXT agent bundle, optionally zipped |
| `snapshots list` | Local snapshot inventory |
| `diff` | Offline health, findings, metrics, and statement comparison |
| `init` | Print least-privilege setup SQL; never execute it |

All focused commands support `--json`. `inspect --format` supports `text`, `json`, and `markdown`.

### CI health gates

```bash
mysq inspect --format json --no-store --fail-on warning > mysq.json
```

Exit codes are stable: `0` passed, `1` warning/note gate, `2` critical gate, `3` connection or collection failure, and `64` invalid gate usage. Use `--no-store` for ephemeral CI databases.

### History and offline comparison

Every `inspect` and TUI refresh saves a compressed local snapshot under `$XDG_STATE_HOME/mysq/snapshots` (otherwise `~/.local/state/mysq/snapshots`). The database fingerprint is derived from `server_uuid` and database name, not credentials.

On first use after upgrading from mysqldot, mysq moves the legacy state directory to the new location so existing snapshots remain available.

```bash
mysq snapshots list
mysq diff --since 1h
mysq diff --fingerprint 9fe955c8c0732deb4b5dbc65 --since 24h --json
```

`diff` is offline: it never opens a database connection.

## Monitoring privileges

Have an administrator review the generated SQL:

```bash
mysq init --user mysq_monitor
```

The core grants are `PROCESS`, `REPLICATION CLIENT`, and `SELECT` on `performance_schema.*`. MySQL ties `information_schema` object visibility to privileges on the underlying application objects. Grant `SELECT` only on each database whose table and index metadata should be visible:

```sql
GRANT SELECT ON app.* TO 'mysq_monitor'@'%';
```

That grant can read application rows, even though mysq never queries them. If that tradeoff is unacceptable, omit it: server, workload, process, lock, replication, and configuration diagnostics remain available, while missing table/index coverage is reported explicitly.

The role is the primary safety boundary. mysq also sets `transaction_read_only=ON`, applies a 10-second statement execution limit, uses a single connection, and contains no mutating SQL in its collector.

## What it checks

- Connection saturation, running threads, aborted clients, QPS/TPS, and row throughput.
- InnoDB buffer hit/use/dirty ratios, purge history, redo waits, row-lock churn, and active blockers.
- Physical I/O and fsync rates, pending I/O, redo generation and checkpoint age, buffer-pool bytes, network throughput, scans, and sorts.
- Top Performance Schema wait events and memory consumers, preserving cumulative count, total, mean, max, current, and high-water values.
- Statement digests by total latency, no-index execution, examined/sent rows, and disk temp tables, plus an interval sample ranked by current database time.
- Current SQL attribution by database user, client host, database, digest, statement state, and wait event when instrumentation provides it.
- Table storage and I/O, missing primary keys, duplicate definitions, and review-only unused-index candidates.
- Process duration, active InnoDB transactions, current row locks, and active or pending metadata locks.
- Replica thread health, lag, GTID positions, and last errors.
- Crash durability (`innodb_flush_log_at_trx_commit`, `sync_binlog`) and important operational settings.
- Probe capabilities, making partial visibility explicit rather than silently treating missing data as healthy.

Findings are deterministic Go code; no model decides database health. Counter findings always carry their scope. An index with zero reads is a review candidate—not permission to drop it—because counters reset, replicas and rare jobs may use it, and the sampled workload may be incomplete.

## Support

MySQL 8.0 and 8.4 LTS are the supported targets. The collector recognizes Percona Server and degrades individual probes rather than losing an otherwise useful inspection. MariaDB is identified but is not yet a compatibility target because its Performance Schema and replication surfaces diverge.

## Development and Docker-only end-to-end test

```bash
make test
make test-race
make e2e
make benchmark
make check
```

`make e2e` binds a fresh `mysql:8.4` container only to `127.0.0.1:33306`, uses a tmpfs data directory, generates concurrent OLTP traffic, holds a real row-lock chain, runs a long statement, and then verifies every CLI command, all output formats, CI exit behavior, local history and diff, export checksums, and a real PTY-driven TUI refresh/export/quit flow. It tears the container and temporary evidence down on exit.

`make benchmark` uses a fresh, isolated Docker MySQL fixture on an ephemeral `127.0.0.1` port, runs the same concurrent workload, validates each command's JSON evidence, and reports median, p95, minimum, and maximum latency. See [docs/performance.md](docs/performance.md) for paired baseline comparisons and the latest measured result.

See [docs/architecture.md](docs/architecture.md) for the module seams and [SECURITY.md](SECURITY.md) for the threat model.

## License

Apache-2.0.
