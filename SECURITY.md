# Security

## Database boundary

Run diagnostics with a dedicated monitoring account. The generated `mysq init` SQL grants only the core server privileges; database-level `SELECT` is an explicit per-schema choice for metadata visibility. For optional query cancellation, use a deliberately authorized operator account with monitoring privileges and `CONNECTION_ADMIN`; do not reuse application writer credentials or a broadly privileged administrator account.

The collector contains read-only `SHOW` and `SELECT` statements, sets the session read-only, uses a single connection, and caps statement execution time. It creates no roles, tables, procedures, plugins, or extensions. `init` prints SQL and executes nothing.

`internal/control` is a separate operator boundary, used only by the TUI Queries tab. It examines up to 100 instrumented current candidates in the selected schema and sends one `KILL QUERY` only after selecting a matching execution and typing exactly `kill`. Prepared executions without a current-event digest are matched using MySQL's digest parser on their current SQL; unparseable SQL is excluded. Raw SQL is never executed or returned to the TUI. Confirmation freezes the target and consumes navigation/refresh keys. The backend independently checks the confirmation and revalidates server, connection, thread, event, schema, digest, user, and host before dispatch; absent or changed executions are rejected. Current statement consumers and the target instrument must be enabled. Each control operation uses a five-second context and a pinned connection. No kill retry, bulk action, `KILL CONNECTION`, privilege escalation, or CLI cancellation endpoint is provided.

MySQL's connection-ID-only kill API cannot atomically validate a statement event and cancel it. A statement can change between revalidation and dispatch; this residual race is disclosed in the confirmation. An accepted request does not prove interruption has completed. The connection and open transaction can survive, and locks may remain. A network error after dispatch leaves the outcome unknown. Read-only diagnostics and exports never invoke the control path.

## Credential handling

Prefer `MYSQ_DATABASE_URL` rather than a positional DSN so passwords do not enter shell history or the process list. DSNs are used only to establish a connection. Connection credentials are not intentionally persisted. Error and diagnostic text may come from the server; review exports before sharing.

## Diagnostic data

Statement digests are normalized by MySQL. Process-list, transaction, and InnoDB SQL receive an additional lexical redaction pass before they enter the context. It covers comments, incomplete strings, numeric literals, and both backslash-escape modes; ambiguous input may lose diagnostic detail. Terminal controls and physical-record dumps are removed. This is redaction, not anonymization or a guarantee about arbitrary diagnostic text. Exports declare `secret_free: false` and include a SHA-256 manifest. Review exports before sharing. Treat hostnames, schema names, table names, usernames, query shapes, configuration, and operational findings as sensitive infrastructure metadata even when literals are absent.

## Local files

History directories use mode `0700`; snapshot files use `0600`. Explicit export bundles use `0644` artifacts because they are intended for sharing, and are created inside a new user-selected directory. Export refuses to overwrite existing paths.

## Reporting vulnerabilities

Do not include a live DSN, password, unredacted database output, or production export in a public issue. Provide the version, MySQL version, failing command, capability warnings, and a minimal synthetic reproduction.
