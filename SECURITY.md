# Security

## Database boundary

Run diagnostics with a dedicated monitoring account. The generated `mysq init` SQL grants only the core server privileges; database-level `SELECT` is an explicit per-schema choice for metadata visibility. For optional query cancellation or connection termination, use a deliberately authorized operator account with monitoring privileges and `CONNECTION_ADMIN`; do not reuse application writer credentials or a broadly privileged administrator account.

The collector contains read-only `SHOW` and `SELECT` statements, sets the session read-only, uses at most four client connections per full inspection (one for focused commands), and caps statement execution time. It creates no roles, tables, procedures, plugins, or extensions. `init` prints SQL and executes nothing.

`internal/control` is a separate operator boundary, used only by the TUI Queries and Connections tabs. In Queries, it examines up to 100 instrumented current candidates in the selected schema and sends one `KILL QUERY` only after selecting a matching execution and typing exactly `kill`. Prepared executions without a current-event digest are matched using MySQL's digest parser on their current SQL; unparseable SQL is excluded. Captured SQL is never executed. The TUI receives terminal-safe live SQL with literals for display; normalized execution identity still controls cancellation. Confirmation freezes the target and consumes navigation/refresh keys. The backend independently checks the confirmation and revalidates server, connection, thread, event, schema, digest, user, and host before dispatch; absent or changed executions are rejected. Current statement consumers and the target instrument must be enabled. Each control operation uses a five-second context and a pinned connection. No kill retry, bulk action, privilege escalation, or CLI cancellation endpoint is provided.

In Connections, the highlighted snapshot row is resolved to a fresh foreground session before the exact `kill` confirmation. The backend sends one `KILL CONNECTION` only after rechecking server UUID, connection/thread IDs, user, and host on the same pinned control session. It excludes the control session itself and rejects missing, changed, or incomplete identities. Sleeping sessions are supported without statement instrumentation. Current schema, SQL, command, and elapsed time may change: this action targets the entire connection, not a particular statement. Live SQL shown in details retains literals; terminal controls are removed. A successful request closes the session and rolls back its open transaction; rollback and lock cleanup may take time. Other users' sessions require `CONNECTION_ADMIN` (or `SUPER`); `SYSTEM_USER` targets additionally require `SYSTEM_USER`. MySQL enforces these privileges. See the [MySQL KILL reference](https://dev.mysql.com/doc/refman/8.0/en/kill.html).

MySQL's connection-ID-only kill API cannot atomically validate a statement event and cancel it. A statement can change between revalidation and dispatch; this residual race is disclosed in the confirmation. An accepted request does not prove interruption has completed. The connection and open transaction can survive, and locks may remain. A network error after dispatch leaves the outcome unknown. Read-only diagnostics and exports never invoke the control path.

Both execution and connection selections carry a private anchor to the exact pinned control session that observed them. Kill never reconnects: a broken session or a later lookup on a new session invalidates old selections. This prevents a confirmation surviving a MySQL restart from matching reused connection/thread/event IDs under the same persisted server UUID. The controller serializes access to its one session and closes it on TUI exit; failed operations release broken sessions.

mysq marks diagnostic, trend, and control connections with the client attribute `program_name=mysq`. Process listing excludes marked sessions before applying its 100-session limit; active-query-user attribution uses the same exclusion. This identifies clients, not privileges or trustworthy application identity. It does not hide other applications based on their username or query text. If connection attribute collection is disabled/truncated or an older mysq build has not set the marker, those other sessions may remain visible; the querying connection is always excluded.

## Credential handling

Prefer `MYSQ_DATABASE_URL` rather than a positional DSN so passwords do not enter shell history or the process list. DSNs are used only to establish a connection. Connection credentials are not intentionally persisted. Error and diagnostic text may come from the server; review exports before sharing.

## Diagnostic data

Statement digests are normalized by MySQL. Process-list, transaction, and InnoDB SQL receive an additional lexical redaction pass before they enter serialized context fields. The TUI additionally retains live process and transaction SQL in `LiveStatement` fields marked `json:"-"`; these preserve literals and comments but strip terminal escapes and controls. TUI rendering reads those fields without modifying redacted fields. History, exports (including CSV/TXT and ZIP), and CLI reports use the redacted fields. Displayed SQL and terminal scrollback may therefore contain sensitive literal values. It covers comments, incomplete strings, numeric literals, and both backslash-escape modes; ambiguous input may lose diagnostic detail. Terminal controls and physical-record dumps are removed. This is redaction, not anonymization or a guarantee about arbitrary diagnostic text. Exports declare `secret_free: false` and include a SHA-256 manifest. Review exports before sharing. Treat hostnames, schema names, table names, usernames, query shapes, configuration, and operational findings as sensitive infrastructure metadata even when literals are absent.

## Local files

History directories use mode `0700`; snapshot files use `0600`. Explicit export bundles use `0644` artifacts because they are intended for sharing, and are created inside a new user-selected directory. Export refuses to overwrite existing paths.

## Reporting vulnerabilities

Do not include a live DSN, password, unredacted database output, or production export in a public issue. Provide the version, MySQL version, failing command, capability warnings, and a minimal synthetic reproduction.
