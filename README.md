# mysqldot

MySQL diagnostics for humans and agents.

`mysqldot` is a single read-only CLI that turns MySQL's own status counters and Performance Schema into a findings-first health report. It has a polished interactive terminal, focused drill-down commands, local history and offline diffs, CI health gates, and a native evidence bundle designed for coding and operations agents.

No collector, web server, cloud account, or database-side objects are required.

```text
◆ MYSQLDOT  MySQL intelligence, from the terminal
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

Details: mysqldot inspect --full   ·   Agent bundle: mysqldot export   ·   Interactive: mysqldot tui
```

## Quick start

Build it from this checkout with Go 1.25 or newer:

```bash
make build
./bin/mysqldot --help
```

After the repository is published, `go install github.com/maheshrijal/mysqldot/cmd/mysqldot@latest` installs the same binary.

Keep credentials out of shell history and the process list:

```bash
export MYSQLDOT_DATABASE_URL='monitor:password@tcp(db.example:3306)/app?tls=true'

mysqldot inspect
mysqldot inspect --full
mysqldot tui
mysqldot export --zip
```

`mysql://monitor:password@db.example:3306/app?tls=true` URLs are accepted too. A positional connection takes precedence over `MYSQLDOT_DATABASE_URL`, which takes precedence over `DATABASE_URL`.

## The terminal

`mysqldot inspect` is the fast, findings-first read. `--full` adds a subsystem board and the highest-value statement, table, connection, lock, replication, and collection details.

`mysqldot tui` opens the live interactive view:

- Six navigable views: Overview, Findings, Queries, Tables, Connections, and Config.
- A restrained adaptive palette that respects light and dark terminal backgrounds; color communicates health instead of decorating every surface.
- A wide-screen navigation rail and owned diagnostic pane inspired by mature OSS TUIs, collapsing to a compact navigator in split panes.
- Arrow, tab, number-key, page, and top/bottom navigation.
- `r` reruns every diagnostic probe and saves a new local snapshot.
- `e` writes the complete native agent bundle directly from the terminal.
- Cards, gauges, tables, findings, and key hints reflow at terminal breakpoints; very small terminals get an explicit resize state instead of a broken layout.

The TUI and static report are two renderers over the same diagnostic snapshot. They cannot disagree about health or findings.

## Agent-native export

```bash
mysqldot export --out incident-2026-08-22 --zip
# Or collect, render, store, and export in one run:
mysqldot inspect --full --export-dir incident-2026-08-22
```

An export is written atomically and refuses to overwrite an existing path. It contains:

| Artifact | Purpose |
|---|---|
| `summary.md` | Findings-first narrative for a human or agent |
| `context.json` | Complete versioned diagnostic contract |
| `schema/context-1.0.0.json` | JSON Schema for validation and tool generation |
| `findings.json` / `metrics.json` | Small deterministic reasoning inputs |
| `queries.csv` | Normalized statement digests and cost |
| `tables.csv` / `indexes.csv` | Storage, I/O, keys, and usage evidence |
| `processes.csv` / `connections.csv` / `locks.csv` | Redacted concurrency snapshot and user/host grouping |
| `variables.cnf` | Sorted captured configuration; evidence, not an apply file |
| `raw/innodb-status.txt` | Redacted InnoDB monitor output |
| `raw/global-status.json` | End-of-sample counters |
| `raw/capabilities.json` | Exact probe coverage and degradation reasons |
| `manifest.json` | Media types, descriptions, and SHA-256 for every artifact |

The bundle is secret-free by construction. SQL string and numeric literals are removed before data enters the in-memory snapshot, so later renderers cannot accidentally choose to leak them. DSNs and passwords are never persisted.

## Commands

| Command | What it does |
|---|---|
| `inspect` | Findings-first report; `--full`, JSON, Markdown, CI gate, history, and inline export |
| `tui` | Live interactive terminal with refresh and native export |
| `queries` | Statement digests ranked by total latency |
| `tables` | Table size, estimated rows, I/O, and primary-key state |
| `indexes` | Index columns, uniqueness, visibility, and read/write counters |
| `processes` | Redacted process-list snapshot |
| `locks` | Active InnoDB row-lock wait graph edges |
| `variables` | Sorted global configuration |
| `replication` | Replica thread state, lag, GTID sets, and redacted errors |
| `export` | Atomic JSON/Markdown/CSV/TXT agent bundle, optionally zipped |
| `snapshots list` | Local snapshot inventory |
| `diff` | Offline health, findings, metrics, and statement comparison |
| `init` | Print least-privilege setup SQL; never execute it |

All focused commands support `--json`. `inspect --format` supports `text`, `json`, and `markdown`.

### CI health gates

```bash
mysqldot inspect --format json --no-store --fail-on warning > mysqldot.json
```

Exit codes are stable: `0` passed, `1` warning/note gate, `2` critical gate, `3` connection or collection failure, and `64` invalid gate usage. Use `--no-store` for ephemeral CI databases.

### History and offline comparison

Every `inspect` and TUI refresh saves a compressed local snapshot under `$XDG_STATE_HOME/mysqldot/snapshots` (otherwise `~/.local/state/mysqldot/snapshots`). The database fingerprint is derived from `server_uuid` and database name, not credentials.

```bash
mysqldot snapshots list
mysqldot diff --since 1h
mysqldot diff --fingerprint 9fe955c8c0732deb4b5dbc65 --since 24h --json
```

`diff` is offline: it never opens a database connection.

## Monitoring privileges

Have an administrator review the generated SQL:

```bash
mysqldot init --user mysqldot_monitor
```

The core grants are `PROCESS`, `REPLICATION CLIENT`, and `SELECT` on `performance_schema.*`. MySQL ties `information_schema` object visibility to privileges on the underlying application objects. Grant `SELECT` only on each database whose table and index metadata should be visible:

```sql
GRANT SELECT ON app.* TO 'mysqldot_monitor'@'%';
```

That grant can read application rows, even though mysqldot never queries them. If that tradeoff is unacceptable, omit it: server, workload, process, lock, replication, and configuration diagnostics remain available, while missing table/index coverage is reported explicitly.

The role is the primary safety boundary. mysqldot also sets `transaction_read_only=ON`, applies a 10-second statement execution limit, uses a single connection, and contains no mutating SQL in its collector.

## What it checks

- Connection saturation, running threads, aborted clients, QPS/TPS, and row throughput.
- InnoDB buffer hit/use/dirty ratios, purge history, redo waits, row-lock churn, and active blockers.
- Statement digests by total latency, no-index execution, examined/sent rows, and disk temp tables.
- Table storage and I/O, missing primary keys, duplicate definitions, and review-only unused-index candidates.
- Process duration and normalized current statements.
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
make check
```

`make e2e` binds a fresh `mysql:8.4` container only to `127.0.0.1:33306`, uses a tmpfs data directory, generates concurrent OLTP traffic, holds a real row-lock chain, runs a long statement, and then verifies every CLI command, all output formats, CI exit behavior, local history and diff, export checksums, and a real PTY-driven TUI refresh/export/quit flow. It tears the container and temporary evidence down on exit.

See [docs/architecture.md](docs/architecture.md) for the module seams and [SECURITY.md](SECURITY.md) for the threat model.

## License

Apache-2.0.
