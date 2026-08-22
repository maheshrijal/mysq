# Security

## Database boundary

Run mysq with a dedicated monitoring account. The generated `mysq init` SQL grants only the core server privileges; database-level `SELECT` is an explicit per-schema choice for metadata visibility. Never use an application writer or administrator account.

The collector contains read-only `SHOW` and `SELECT` statements, sets the session read-only, uses a single connection, and caps statement execution time. It creates no roles, tables, procedures, plugins, or extensions. `init` prints SQL and executes nothing.

## Credential handling

Prefer `MYSQ_DATABASE_URL` rather than a positional DSN so passwords do not enter shell history or the process list. DSNs are used only to establish a connection. They are absent from contexts, local history, rendered reports, logs, and exports.

## Diagnostic data

Statement digests are normalized by MySQL. Process-list and InnoDB SQL lines receive an additional conservative literal-redaction pass before they enter the context. Exports declare `secret_free: true` and include a SHA-256 manifest. Treat hostnames, schema names, table names, usernames, query shapes, configuration, and operational findings as sensitive infrastructure metadata even when literals are absent.

## Local files

History directories use mode `0700`; snapshot files use `0600`. Explicit export bundles use `0644` artifacts because they are intended for sharing, and are created inside a new user-selected directory. Export refuses to overwrite existing paths.

## Reporting vulnerabilities

Do not include a live DSN, password, unredacted database output, or production export in a public issue. Provide the version, MySQL version, failing command, capability warnings, and a minimal synthetic reproduction.
