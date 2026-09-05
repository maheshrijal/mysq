# mysq

Find slow queries, blocked transactions, and MySQL performance problems from your terminal.

Open an interactive dashboard, get a quick health report, or export the evidence to share with your team. Diagnostics read MySQL's own statistics; there is no service to deploy. Cancelling a query is a separate, explicitly confirmed action.

[Install](#install) · [Connect](#connect) · [Use the dashboard](#use-the-dashboard) · [Export a report](#export-a-report) · [Troubleshooting](#troubleshooting)

## Install

macOS and Linux, on Intel/AMD or Apple Silicon/ARM:

```sh
curl -fsSL https://raw.githubusercontent.com/maheshrijal/mysq/main/install.sh | sh
```

Open a new terminal after installation, or run the PATH command it prints. Run the same command to update.

<details>
<summary>Windows, Go, and installer options</summary>

**Windows:** download the amd64 or arm64 ZIP from [Releases](https://github.com/maheshrijal/mysq/releases), extract `mysq.exe`, and add its directory to `Path`.

**Go 1.25+:** this also works before the first binary release is published:

```sh
go install github.com/maheshrijal/mysq/cmd/mysq@latest
```

Add `$(go env GOPATH)/bin` to PATH, or your `GOBIN` directory if configured.

The curl installer needs a published release. It verifies SHA-256 checksums, installs into `~/.local/bin`, and configures PATH for sh, Bash, Zsh, and Fish. Set `INSTALL_DIR` to choose another directory, `VERSION` to pin a published tag, or `MYSQ_NO_MODIFY_PATH=1` to manage PATH yourself:

```sh
curl -fsSL https://raw.githubusercontent.com/maheshrijal/mysq/main/install.sh | env INSTALL_DIR="$HOME/bin" sh
```

</details>

## Connect

Export your MySQL connection string, then open the dashboard:

```sh
export MYSQ_DATABASE_URL='mysql://mysq_monitor:password@localhost:3306/app'
mysq tui
```

Replace the example credentials, host, and database with yours. You need network access and a [monitoring account](#monitoring-permissions). MySQL URLs require percent-encoding special characters in credentials; for example, `@` becomes `%40`.

The same connection works for reports and focused commands:

```sh
mysq inspect
mysq queries
mysq blockers
```

You can also pass a connection string directly:

```sh
mysq tui 'mysql://mysq_monitor:password@localhost:3306/app'
```

For TLS with certificate verification, append `?tls=true`. Port **3306** is the default when omitted.

<details>
<summary>Connection formats and compatibility</summary>

Native MySQL DSNs are supported, including TCP and Unix sockets:

```sh
export MYSQ_DATABASE_URL='mysq_monitor:password@tcp(localhost:3306)/app'
export MYSQ_DATABASE_URL='mysq_monitor:password@unix(/var/run/mysqld/mysqld.sock)/app'
```

A command argument overrides `MYSQ_DATABASE_URL`. The legacy `MYSQLDOT_DATABASE_URL` and then `DATABASE_URL` are accepted as fallbacks. Omitting the database inspects all databases visible to your account.

For compatibility with existing shell setups, `DBOPS_MYSQL_USER` and `DBOPS_MYSQL_PWD` can supply credentials when the connection string has no username. With those exported, `mysq tui 'host[:port]/database'` works, and `mysq tui` asks for the endpoint if none is configured. An explicit username always keeps its own password, including an empty one.

The examples use placeholder passwords. For sensitive credentials, load environment variables through your usual secret-management workflow instead of typing passwords into shell history.

</details>

## Use the dashboard

```sh
mysq tui
```

**Overview** highlights the most important finding first. **Connections** shows sessions and locks; **Queries** shows expensive statements and their users. **Engine**, **Findings**, **Tables**, and **Config** provide the supporting details.

| Key | Action |
|---|---|
| `1`–`7`, Left/Right, Tab | Switch views |
| Up/Down | Scroll or select a query/finding |
| Enter | Investigate the selected item |
| Esc | Go back or cancel |
| `/` | Filter queries, connections, findings, or tables |
| `B` | Investigate blocking chains |
| `r` | Refresh the full diagnostic snapshot |
| `p` | Pause/resume live graphs |
| `G` / `g` | Scroll to the bottom/top |
| `e` | Export the current snapshot |
| `?` / `q` | Keyboard help / quit |

Overview graphs update every two seconds and retain up to five minutes: queries/sec, running threads, row-lock waits, and InnoDB read/write throughput. Large terminals show four charts; smaller layouts use sparklines below the summary. Use `G` to reach them. Graphs update independently; press `r` to refresh findings and tables.

Ghostty is the primary terminal target. Colors follow your terminal theme, and layouts adapt to the window size. Use at least 52 columns × 18 rows; 100 columns or more gives tables and graphs more room.

### Cancel a running query

In **Queries**, press `K`, select one current execution, and press Enter to review its user, host, and connection. Type exactly **`kill`**, then press Enter to confirm.

This requires an account authorized to cancel that query. Cancelling keeps the connection open, and transaction locks may remain. If the control connection is lost, select the execution again. MySQL cannot atomically check a statement and cancel it, so a new statement can race the final dispatch. See [Security](SECURITY.md) for the full behavior.

## Investigate from the command line

Once your connection is exported:

```sh
mysq inspect                 # Health report with the most important findings
mysq inspect --full          # Add detailed diagnostic tables
mysq queries                 # Expensive normalized statements
mysq blockers                # Who is blocking whom
mysq processes               # Current connections and users
mysq tables                  # Table size, activity, and primary keys
mysq indexes                 # Index definitions and usage
mysq engine                  # InnoDB I/O, redo, and buffer activity
mysq replication             # Replica state and lag
```

Use `mysq --help` for all commands and `mysq <command> --help` for options. Focused commands support `--json`; use `mysq inspect --format json` for the full report.

## Export a report

```sh
mysq export --out incident-report --zip
```

This creates a new directory and ZIP containing a Markdown summary, JSON evidence, CSV tables, and checksums. It refuses to overwrite an existing export. In the dashboard, `e` exports the current snapshot without collecting again.

SQL literals are redacted, but usernames, hosts, database names, and query shapes remain useful—and potentially sensitive—evidence. Review the export before sharing it. Nothing is sent to an AI service.

## Compare with an earlier run

Inspections and dashboard refreshes save snapshots locally. After collecting more than one:

```sh
mysq snapshots list
mysq diff --since 1h
```

Comparison works offline. Snapshots live in `$XDG_STATE_HOME/mysq/snapshots`, or `~/.local/state/mysq/snapshots` by default. Use `mysq inspect --no-store` to skip saving a run. Live graph samples are only kept in memory.

## Monitoring permissions

Ask your database administrator to review the setup SQL printed by:

```sh
mysq init --user mysq_monitor
```

This prints SQL; it does not create an account or apply grants. Core monitoring uses `PROCESS`, `REPLICATION CLIENT`, and `SELECT` on `performance_schema.*`.

To see table and index metadata for a database, MySQL also requires privileges on its objects. An administrator can choose to grant:

```sql
GRANT SELECT ON app.* TO 'mysq_monitor'@'%';
```

That grant permits reading application rows, although mysq does not query them. Without it, some table/index information will be unavailable. Query cancellation needs separate privileges; cancelling another user's query requires `CONNECTION_ADMIN` or `SUPER`, with additional restrictions for protected accounts.

## Troubleshooting

| Problem | What to check |
|---|---|
| `mysq: command not found` | Open a new terminal or run the PATH command printed by the installer. Check for any reported shell-configuration write failures. |
| Access denied | Check the exported username/password and whether the MySQL account permits connections from your host. |
| Timeout or connection refused | Check the endpoint, port, VPN/tunnel, and firewall. For Docker's published port, try `127.0.0.1`. |
| Connecting to the wrong server | An argument overrides saved connection variables. Check `MYSQ_DATABASE_URL` and the legacy/fallback variables listed above. |
| TLS certificate error | Check the hostname and trusted certificate chain; `tls=true` verifies both. |
| Missing queries, tables, or users | Check monitoring grants and Performance Schema. `mysq coverage` shows instrumentation gaps. |
| `PARTIAL` or `STALE` | Some evidence is unavailable, or the last refresh failed. Inspect the reported reason and press `r` after fixing it. |
| Graphs are empty | Allow a few seconds to collect history; check for a paused state or sampling error. |

MySQL 8.0 and 8.4 are supported. Percona Server is recognized; MariaDB is not yet a compatibility target.

## Contributing

For local development, run `make build`, `make check`, and `make e2e`. See [architecture](docs/architecture.md), [performance testing](docs/performance.md), and [security](SECURITY.md) for implementation details.

## License

[Apache-2.0](LICENSE).
