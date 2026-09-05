# Architecture

mysq is organized around a diagnostic snapshot: an inspection produces a complete `model.Context`, consumed by reports, history, and exports. Explicit TUI operator actions use a separate live control interface.

```text
MySQL 8.x
   │ read-only queries, up to four pinned connections
   ▼
collector ──► versioned Context ──► deterministic analyzer
                                      │
              ┌───────────────────────┼────────────────────────┐
              ▼                       ▼                        ▼
        terminal / TUI          history + diff          agent bundle
```

The `Context` interface includes the versioned JSON shape, collection timestamp and interval, explicit probe capabilities, server identity, raw counters, normalized statements with point-in-time user attribution and tail latency, interval statement database-time attribution, sampled and cumulative waits, file I/O, server errors, instrumentation coverage, memory summaries, replication workers, active transactions and locks, evidence tables, deterministic findings, and health score. Static renderers do not re-query MySQL. History does not retain connection configuration.

## Modules

- `internal/collect` owns connection parsing, session safety, probes, capability degradation, and rate calculation. Its external interface is `Inspect(Target) -> Context`.
- `internal/analyze` owns deterministic thresholds, caveats, recommendations, ordering, and health scoring. It has no I/O.
- `internal/render` and `internal/tui` are independent adapters over the same context. The TUI has a diagnostic refresh callback and an injected `QueryController` for current executions and confirmed cancellation.
- `internal/control` resolves schema/digest to ephemeral live executions, checks their identity, and sends one confirmed `KILL QUERY` on a pinned connection. It is separate from the read-only collector and has no persistence or CLI endpoint.
- `internal/export` atomically materializes the agent contract and integrity manifest. It refuses overwrites.
- `internal/history` stores gzip JSON by server/database fingerprint. `internal/compare` remains pure and performs offline comparisons.
- `internal/sanitize` is intentionally early in the flow. Potential SQL literals are discarded before the context crosses any persistence or rendering seam.

The full snapshot collector degrades individual optional probes, but server identity and two counter samples are required. This prevents a visually complete report built from absent data. Each optional failure appears in capabilities, collection warnings, the full report, the TUI Config view, and exports. Focused commands cross a separate collector interface that runs only the probes needed for that section; point-in-time sections return immediately, while rate-based sections retain the configured sample interval. Counter families have different observation windows, so `context.json.sample_intervals_ms` records the observed window for each family; the legacy `interval_ms` remains the global-status window for full inspections.

## Terminal colors

Ghostty is the primary terminal target. TUI styling uses default foreground/background and ANSI palette slots 1–6 for semantic accents, with slot 8 reserved for borders. Reverse video highlights selections using the terminal's current foreground/background. No TUI color depends on Lip Gloss's cached background detection or fixed RGB values. Ghostty can therefore update the displayed theme without a MySQL refresh, input interception, or background color polling. Other ANSI terminals remain supported; accent hues follow their configured palette. Regression tests verify identical emitted frames with opposite cached appearance values and reject fixed RGB escape sequences.

## Live trends

The TUI injects a separate `TrendSampler` backed by one reusable connection. Every two seconds it reads seven global status counters and the server UUID in one SELECT from `performance_schema.global_status`, with a five-second timeout and at most one sample in flight. This does not call the full inspector, scan application tables, or save snapshots. Quitting cancels the sampling context and closes the pool.

Rates use measured elapsed time between consecutive successful samples. The sampler subtracts its own SELECT from Questions and its own thread from Threads_running. Other client activity, including diagnostic inspections, remains server-wide. I/O measures physical InnoDB bytes, not logical row reads or network traffic. UUID changes, decreasing counters/uptime, failed reads, and extended sampling gaps invalidate a delta. Pause freezes the display and discards the baseline; generation IDs reject late results after pause/resume.

Up to five minutes of timestamped observations remain in bounded memory. Graphs use the same observed time window, growing from ten seconds to five minutes, and zero-based per-chart scales; read/write series share an I/O scale. Braille line charts occupy spare Overview height, falling back to peak-preserving time-bucket sparklines on compact terminals. Missing intervals stay blank. Telemetry does not change `model.Context`, health findings, selection, query confirmations, history, or exports; `r` still refreshes diagnostic evidence explicitly.

## Database cost and safety

Connection resolution accepts `host[:port]/database`, MySQL URLs, and native driver DSNs. An argument wins over `MYSQ_DATABASE_URL`, legacy `MYSQLDOT_DATABASE_URL`, and `DATABASE_URL`. When the selected connection omits a username, `DBOPS_MYSQL_USER` and `DBOPS_MYSQL_PWD` supply the credential pair; an explicit username keeps its own password, including an empty one. Credentials alone do not select an endpoint. `mysq tui` opens a local endpoint prompt if no argument or connection variable exists; validation precedes database access and nothing from the prompt is persisted. Other database commands return setup guidance instead of prompting.

A full inspection uses one primary connection plus at most three optional-probe worker connections and samples `SHOW GLOBAL STATUS`, statement digest and global counters, wait summaries, file-I/O summaries, and error summaries around the configured interval. The emitted collections are bounded to 20 cumulative statement digests, 20 interval statement samples, 30 waits, 30 file instruments, 30 errors, 30 memory consumers, 100 tables, 100 processes, 100 transactions, and 100 metadata locks. It never queries application rows. Every connection is pinned read-only. The primary connection and focused commands retain the 10-second `MAX_EXECUTION_TIME` limit; full-inspection worker sessions use three seconds, with a three-second client budget per task (including any connection setup). Timed-out optional probes are marked unavailable. Each worker validates server UUID and selected database against the primary before reading evidence, including after reconnection. Active user, process, transaction, and lock data is explicitly point-in-time and can change while the report is being consumed.

Independent metadata probes run concurrently, followed by active-user attribution, which depends on selected digests. Wait, file-I/O, error, and digest counter endpoints run in bounded parallel batches. Workers finish their first batch before the primary statement/global baselines; the final global/statement reads finish before workers resume. This keeps collector work outside the primary status window. Each counter family records its own measured interval. A failed first endpoint skips its second read, and collector failures invalidate potentially contaminated server-error deltas. Per-task results are joined before capability/warning slices are updated. The first screen still waits for the complete, possibly partial snapshot; there is no progressive rendering.

Some Performance Schema summary tables grow with schema size. Their queries do not scan application data, but operators should still validate run time on unusually large fleets before aggressive scheduling. mysq is an on-demand diagnostic, not a monitoring daemon.

Opening query detail fetches live users and session evidence independently of cumulative digest statistics. `K` in Queries opens a fresh session picker, then an exact `kill` confirmation for one frozen target. Async lookups carry request identities so abandoned results cannot retarget a new view. During confirmation, ordinary navigation is consumed as input. The action result remains visible while a full diagnostic refresh runs. Control rechecks server UUID, connection/thread/event IDs, schema/digest, user, and host before dispatch, rejects unavailable instrumentation, and never automatically retries. MySQL cannot atomically compare the event ID and kill it; this race and the possibility of surviving transaction locks are explicit in the confirmation and result. Live execution records do not change the snapshot schema or enter exports/history.

The control adapter retains one pinned `sql.Conn` and serializes lookup/dispatch/close. Each returned execution privately references that exact connection. `Kill` never opens a connection and refuses an absent or different anchor before revalidation; a disconnected session fails closed. A later successful lookup can reconnect, but cannot revive selections from the old session. This establishes continuity across selection and confirmation because UUID and execution IDs alone can repeat after a server restart. CLI teardown cancels outstanding operations and closes the control adapter.

## Investigation and coverage contract (1.5.0)

The analyzer records `health.subsystems` with independent status and completeness, plus `health.unknown`. A subsystem can have an actionable warning and incomplete evidence at the same time. Missing probe records are unknown; successful replication collection with no channel is not applicable. Detected collection limits and disabled required consumers also reduce coverage. This grades captured, visible evidence, not every object or instrument on the server. The historical numeric score remains a finding tally, not coverage or a probability of health.

`blocking_chains` connects observed row-lock edges to captured transaction owners. Chains are ordered by distinct waiter count; duplicate edges do not inflate that count. Missing owners and graph cycles carry caveats. Matching metadata-lock objects only establishes candidate owners, so metadata locks remain separate. The TUI investigation uses the complete snapshot even when a list is filtered. `mysq blockers --json` collects only identity, row waits, transactions, and metadata locks, returning target, time, scope, capabilities, and the same chain representation.

Findings and query selection survive refresh by identity. Investigation views remain scrollable, support contextual help and export, and return to the originating view with Escape. Failed refreshes retain evidence but show STALE. Full inspections still refresh manually; no polling daemon or new service is introduced.

Connections uses the same row-selection keys as Queries, preserving connection/thread identity across snapshot reordering and restoring selection when a filter is canceled. Enter opens session details; `B` opens blocking chains. `K` resolves the selected row to fresh session evidence before opening the shared exact-confirmation input. Connection lookup messages carry request identities, and confirmation freezes a separate `control.Connection` token. `Queries.Connection` and `Queries.KillConnection` share the existing serialized, pinned control session, but do not require a current statement or its instrumentation. They validate server UUID, connection/thread IDs, user, and host and exclude the control connection itself. The connection action sends `KILL CONNECTION` once, reports acceptance or failure, and requests a diagnostic refresh. Statement changes are allowed because the whole session is the target. Query cancellation remains `KILL QUERY` with event/digest validation. Neither action enters collection, history, or exports.

The process collector limits distinct foreground threads before joining statement evidence. It selects the latest current statement and one latest current wait per thread, avoiding fan-out from nested idle/socket waits. All mysq-owned SQL pools use `OpenDatabase`, which supplies the `program_name=mysq` connection attribute; process and active-user queries exclude that marker on the server. Parsing a target DSN alone does not mark application connections. Attribution depends on MySQL retaining connection attributes.

TUI collectors and controllers opt into `LiveSQL`. Process and transaction records keep terminal-safe original text in an ephemeral `LiveStatement` field (`json:"-"`) alongside the existing redacted `Statement`; MySQL-normalized historical digests remain unchanged. Display helpers prefer live text and never mutate persisted fields. Connection rows stay on one line with an explicit selection marker, and Enter opens the full captured text. Query execution detail/picker/confirmation and blocking-chain views also use live text. Export and history tests reject literal leakage from both representations.
