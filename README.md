# mysq

MySQL diagnostics for humans and agents.

`mysq` turns MySQL's own status counters and Performance Schema into a findings-first health report. It has an interactive terminal, focused drill-down commands, local history and offline diffs, CI health gates, and a native evidence bundle designed for coding and operations agents. Diagnostics are read-only; the TUI Queries tab also offers explicitly confirmed query cancellation.

No collector, web server, cloud account, or database-side objects are required.

```text
◆ MYSQ  MySQL intelligence, from the terminal
────────────────────────────────────────────────────────────────────────────────
connected · 127.0.0.1:3306/app · MySQL 8.4.6 · primary · 1.0s status sample

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

- Findings appear above metrics, including at 80×24. Enter on Overview opens the priority finding; Findings supports row selection, Enter for measured evidence and the next step, and Esc to return.
- `B` opens blocking-chain evidence from any view (Enter also opens it from Connections): captured root owners, transaction SQL, distinct downstream waiters, and blocker → waiter edges. Missing owners and cycles are explicit. Metadata-lock owners are candidates, not inferred blocking edges.
- Coverage and health are assessed once for all renderers. Unverified subsystems never join the healthy list, and failed refreshes retain the last snapshot under a **STALE** header. Capture time is shown on Overview; refresh remains manual.
- Seven navigable views: Overview, Connections, Queries, Engine, Findings, Tables, and Config.
- A restrained adaptive palette that respects light and dark terminal backgrounds; color communicates health instead of decorating every surface.
- A compact browser-style tab strip with live counts and full-width diagnostic content, collapsing to neighboring tabs in narrow split panes.
- Left/Right, Tab/Shift-Tab, and number keys switch views without losing each view's scroll position. Arrow keys are the universal navigation layer; `h`/`j`/`k`/`l`, Page Up/Down, Ctrl-U/D, Home/End, and `g`/`G` provide familiar Vim and pager aliases. In Queries, movement and paging follow the selected statement, Enter opens its full normalized SQL and execution evidence, and Esc returns to the selected row.
- `?` opens complete contextual keyboard help. `/` filters Queries, Tables, Connections, or Findings without changing the captured snapshot; Enter applies, Esc cancels editing, and Esc on a filtered view clears it.
- `r` reruns every diagnostic probe and saves a new local snapshot.
- Enter in Queries also reads current executions for the selected schema and digest, showing users, hosts, connection/thread/event IDs, database, duration, and state. Historical user attribution in the query summary remains tied to the snapshot; live session details are checked separately and are not persisted.
- Live matching examines up to 100 instrumented candidates. Prepared executions with no event digest use MySQL's parser to normalize their current SQL; unparseable or invisible executions are excluded. An empty result means no match was identified in that bounded read.
- `K` is available only in Queries: select one live execution with Up/Down, press Enter, then type exactly `kill` and press Enter. Esc cancels before dispatch. Historical digests with no visible current execution cannot be killed. No bulk cancellation or kill action is exposed in other tabs or CLI commands.
- Query investigation uses aligned session columns, a highlighted selection, SQL syntax colors, and a distinct destructive confirmation input. Labels, elapsed time, execution state, and action outcomes have separate visual emphasis in light and dark terminals.
- `e` writes the complete native agent bundle directly from the terminal and keeps its destination visible until dismissed with `Esc`.
- Cards, gauges, tables, findings, and key hints reflow at terminal breakpoints; very small terminals get an explicit resize state instead of a broken layout.

The TUI and static report are two renderers over the same diagnostic snapshot. Both consume the same recorded subsystem assessments and findings. The numeric score summarizes detected findings only; it does not measure diagnostic coverage.

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
| `schema/context-1.5.0.json` | JSON Schema for validation and tool generation |
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

SQL literals and comments are redacted before data enters the snapshot, including truncated quoted strings and MySQL hexadecimal, binary, and exponent literals. Redaction covers both backslash-escape modes and removes terminal controls and InnoDB physical-record dumps. Infrastructure identifiers and arbitrary diagnostic metadata can still be sensitive: the manifest explicitly declares `secret_free: false`. Review a bundle before sharing it. Connection credentials are not intentionally persisted.

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
| `blockers` | Rooted row-lock chains and metadata-lock owner candidates; scoped JSON with timestamp, target, capabilities, and caveats |
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

`diff` is offline: it never opens a database connection. Query identity includes schema and digest. Detected restarts, counter decreases, and replaced digests omit invalid cumulative deltas. `interval_mean_ms` reports latency per observed call between snapshots (null when no calls were observed); `mean_ms_delta` remains the difference of cumulative averages. Missing findings are `inconclusive_findings` when current coverage or contract changes prevent verifying resolution. Missing query findings remain inconclusive because digest capture and query finding lists are bounded. An absent query is never proof that it stopped running.

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

For query cancellation, use a deliberately authorized operator account with the monitoring grants plus `CONNECTION_ADMIN` to cancel other users' queries (the older `SUPER` privilege also permits this). Targets executing with `SYSTEM_USER` additionally require that privilege. `mysq init` does not grant cancellation privileges, and mysq never grants them automatically. Permission errors are displayed in the TUI.

Cancellation sends `KILL QUERY`, retaining the connection. MySQL interrupts asynchronously; transaction locks may remain until the client ends its transaction. A successful request is displayed as accepted, followed by a diagnostic refresh. Before sending, mysq rechecks the server UUID, connection, thread, statement event, schema, digest, user, and host on one pinned control connection. Finished, replaced, or unidentifiable executions are refused. MySQL only accepts a connection ID for `KILL QUERY`, so there is an unavoidable race between rechecking and dispatch; the confirmation explains this. Failed sends are never retried automatically. See the [MySQL KILL documentation](https://dev.mysql.com/doc/refman/8.4/en/kill.html).

## What it checks

- Connection saturation, running threads, aborted clients, QPS/TPS, and row throughput.
- InnoDB buffer hit/use/dirty ratios, purge history, redo waits, row-lock churn, and active blockers.
- Physical I/O and fsync rates, pending I/O, redo generation and checkpoint age, buffer-pool bytes, network throughput, scans, and sorts.
- Top Performance Schema wait events and memory consumers, preserving cumulative count, total, mean, max, current, and high-water values.
- Statement digests by total latency, no-index execution, examined/sent rows, and disk temp tables, plus an interval sample ranked by current database time.
- Current SQL attribution by database user, client host, database, digest, statement state, and wait event when instrumentation provides it.
- Table storage and I/O, missing primary keys, duplicate definitions, and review-only unused-index candidates.
- Process duration, active InnoDB transactions, current row locks, pending metadata locks, and granted owners including sleeping sessions on the same object. Completed statement/wait events are excluded from current attribution.
- Pending metadata-lock findings, sampled statement errors, and InnoDB log/buffer capacity waits. Small, inexpensive queries no longer warn solely because they dominate a tiny captured workload.
- Single-channel asynchronous replica health, lag, GTID positions, and last errors. Multiple channels are explicitly unavailable pending channel-aware support; the collector never silently grades only the first channel.
- Crash durability (`innodb_flush_log_at_trx_commit`, `sync_binlog`) and important operational settings.
- Probe capabilities, making partial visibility explicit rather than silently treating missing data as healthy. Failed probes, including replication, produce a warning and can fail a warning-level CI gate. `health.subsystems` records status, completeness, and missing-evidence reasons; `health.unknown` counts incomplete assessments. Older snapshots without these fields have unverified coverage.

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

`make e2e` binds a fresh `mysql:8.4` container to an ephemeral `127.0.0.1` port by default (set `MYSQ_MYSQL_PORT` to request a specific port), uses a tmpfs data directory, generates concurrent OLTP traffic, holds a real row-lock chain, runs a long statement, and then verifies every CLI command, all output formats, CI exit behavior, local history and diff, export checksums, a sleeping transaction holding up a metadata lock, and a real PTY-driven TUI investigation/refresh/export/quit flow. It tears the container and temporary evidence down on exit.

`make benchmark` uses a fresh, isolated Docker MySQL fixture on an ephemeral `127.0.0.1` port, runs the same concurrent workload, validates each command's JSON evidence, and reports median, p95, minimum, and maximum latency. See [docs/performance.md](docs/performance.md) for paired baseline comparisons and the latest measured result.

See [docs/architecture.md](docs/architecture.md) for the module seams and [SECURITY.md](SECURITY.md) for the threat model.

## License

Apache-2.0.
