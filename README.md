# mysq

Find slow queries, blocked transactions, and MySQL performance problems from your terminal.

Open an interactive dashboard, get a quick health report, or export the evidence to share with your team. Diagnostics read MySQL's own statistics; there is no service to deploy. Cancelling a query is a separate, explicitly confirmed action.

[Install](#install) · [Connect](#connect) · [Use the dashboard](#use-the-dashboard) · [Export a report](#export-a-report) · [Troubleshooting](#troubleshooting)

## Install

You need [Go 1.25 or newer](https://go.dev/dl/). On macOS or Linux:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install github.com/maheshrijal/mysq/cmd/mysq@latest
export PATH="$HOME/.local/bin:$PATH"

mysq --help
```

Add the `export PATH` line to `~/.zshrc` or `~/.bashrc` to keep `mysq` available in new terminals. Run the same `go install` command to update.

There are no prebuilt [GitHub releases](https://github.com/maheshrijal/mysq/releases) yet. The Go command above installs the current code; a release-download script is not available yet.

<details>
<summary>Windows installation</summary>

With Go installed, run in PowerShell:

```powershell
go install github.com/maheshrijal/mysq/cmd/mysq@latest
$env:Path += ";$(go env GOPATH)\bin"
mysq --help
```

If you have set `GOBIN`, add that directory to `Path` instead.

</details>

## Connect

You need network access to MySQL 8.0 or 8.4 and a monitoring account. If you need an account, see [Monitoring permissions](#monitoring-permissions).

### 1. Export your credentials

If your terminal already has `DBOPS_MYSQL_USER` and `DBOPS_MYSQL_PWD`, you can skip this step. mysq uses them automatically.

In Bash or zsh, set your username and enter the password at the hidden prompt:

```sh
export DBOPS_MYSQL_USER='mysq_monitor'
printf 'MySQL password: '
IFS= read -r -s DBOPS_MYSQL_PWD
printf '\n'
export DBOPS_MYSQL_PWD
```

The password is not echoed or written into the command you type. Credentials remain in your shell environment and are inherited by mysq.

<details>
<summary>PowerShell credentials</summary>

```powershell
$env:DBOPS_MYSQL_USER = 'mysq_monitor'
$password = Read-Host 'MySQL password' -AsSecureString
$env:DBOPS_MYSQL_PWD = [System.Net.NetworkCredential]::new('', $password).Password
Remove-Variable password
```

</details>

### 2. Choose your database

Pass `host/database` to open the dashboard:

```sh
mysq tui 'db.example.com/app'
```

Port **3306** is the default. Include a different port when needed:

```sh
mysq tui '127.0.0.1:3307/app'
```

Or just run:

```sh
mysq tui
```

When no endpoint is configured, the TUI asks for **host[:port]/database**. Enter the endpoint, then press **Enter** to connect or **Esc** to cancel. Your password comes from the exported credentials. The prompt does not save the endpoint or credentials.

For a quick report instead of a dashboard, use the same endpoint with `inspect`:

```sh
mysq inspect 'db.example.com/app'
```

### 3. Reuse the connection

To avoid entering the endpoint on every run, export it once:

```sh
export MYSQ_DATABASE_URL='db.example.com/app'

mysq tui
mysq inspect
mysq queries
mysq blockers
```

`MYSQ_DATABASE_URL` can hold the endpoint alone; credentials still come from `DBOPS_MYSQL_USER` and `DBOPS_MYSQL_PWD`. Omitting `/database` inspects all databases visible to your account. Quote endpoints so shells such as zsh do not expand special characters.

For encrypted connections with certificate verification, append `?tls=true`:

```sh
export MYSQ_DATABASE_URL='db.example.com/app?tls=true'
```

The server certificate must be trusted by your machine and match the hostname.

<details>
<summary>Existing MySQL URLs, DSNs, and connection precedence</summary>

Existing formats also work:

```sh
export MYSQ_DATABASE_URL='mysql://db.example.com:3306/app?tls=true'
# Native driver format, with credentials taken from the DBOPS variables:
export MYSQ_DATABASE_URL='tcp(db.example.com:3306)/app?tls=true'
```

A connection string that includes a username uses its own credentials, including an empty password. mysq does not combine that username with `DBOPS_MYSQL_PWD`. Full URLs require percent-encoding special characters in credentials; using the separate environment variables avoids that step.

Endpoint precedence is: command argument → `MYSQ_DATABASE_URL` → legacy `MYSQLDOT_DATABASE_URL` → `DATABASE_URL`. The TUI prompts only when none is set. Other database commands require an endpoint and print setup guidance if one is missing. Credentials alone never silently select localhost.

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
| `mysq: command not found` | Add the installation directory to `PATH`, then open a new terminal. |
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
