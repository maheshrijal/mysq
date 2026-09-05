# Changelog

All notable changes to mysq are documented here. The project follows Semantic Versioning.

## [Unreleased]

### Added

- Live Overview trends for QPS, running threads, row-lock waits, and physical InnoDB read/write throughput, with a shared timeline, responsive line charts/sparklines, five-minute in-memory history, and `p` pause/resume. A separate two-second sampler preserves diagnostic snapshots and query selection; missing/reset samples appear as gaps.
- Queries-only `K` cancellation with live session selection, exact typed `kill` confirmation, execution identity revalidation, and explicit accepted/failed outcomes. Query details show current executing users, hosts, connections, and statement timing independently of historical digest totals.
- Findings-first 80×24 overview, selectable findings with evidence drill-down, and `B` blocking-chain investigations in the TUI.
- `mysq blockers` and scoped JSON for root owners, captured edges, distinct waiters, metadata-lock candidates, and coverage caveats.
- Context 1.5.0 with shared subsystem assessments, incomplete-coverage counts, and blocking chains.
- Findings for pending metadata locks, sampled statement errors, and log/buffer-capacity waits.

- Findings-first MySQL 8.0/8.4 inspection with deterministic health scoring.
- Adaptive seven-view interactive terminal with refresh and one-key agent export.
- Statement, table, index, process, connection-group, transaction, row/metadata lock, wait-event, memory, replication, configuration, and InnoDB diagnostics.
- Text, versioned JSON, Markdown, focused JSON, and severity-based CI output contracts.
- Atomic 25-file agent bundle with JSON Schema, CSV/TXT evidence, ZIP support, and SHA-256 manifest.
- Compressed local snapshot history and offline metric/finding/query diffs.
- Docker-only MySQL 8.4 load and PTY end-to-end test harness.
- Static Docker image and cross-platform GoReleaser configuration.
- MySQL-native statement p95/p99/p999 latency, sampled wait pressure, file-I/O latency, server errors, replication worker state, and Performance Schema coverage data.
- Interval statement database-time attribution with Top SQL share in Overview and a dedicated export artifact.

### Changed

- Apply semantic colors to the main Connections, Queries, Engine, and Tables grids, including SQL syntax and explicit lock/primary-key/visibility signals. Fix literal ANSI fragments and flickering in the typed query-cancellation confirmation.
- Use Ghostty's native foreground/background and ANSI palette instead of cached light/dark RGB colors. Theme changes retain readable body text and selection. Give SQL identifiers and numeric evidence distinct colors, reduce bold labels, align query metrics, and remove repeated live-session metadata.
- Make query investigation and cancellation easier to scan with aligned session columns, a highlighted selection, SQL syntax colors, explicit steps, and semantic confirmation/result colors in both terminal themes.
- Failed refreshes show STALE; unavailable replication and other failed probes produce warning-level coverage findings. Multi-channel replication is explicitly unsupported rather than assessing only the first channel.
- Query comparisons use schema plus digest, omit detected reset/restart deltas, expose interval means, and avoid declaring resolution through coverage gaps.
- SQL redaction covers comments, truncated strings, both escape modes, numeric encodings, and terminal controls; InnoDB record dumps are omitted. Export manifests now declare `secret_free: false` because infrastructure metadata and arbitrary diagnostics still require review.
- Include sleeping metadata-lock owner candidates and exclude completed statement/wait events from current process attribution.
- Suppress tiny-workload dominance warnings and small-scan index warnings; clarify InnoDB primary-key recommendations.

- Reorder investigation views to Overview, Connections, Queries, Engine, Findings, Tables, and Config; use Left/Right for tabs and Up/Down for viewport scrolling, while Queries keeps an Up/Down and Enter/Esc master-detail workflow with a smaller high-value column set that retains current user attribution.
- Replace the generic Overview engine strip with current MySQL load, query health, and contention panels; enrich Engine with sampled waits, file I/O, errors, replication, and instrumentation coverage without adding host metrics or new analysis rules.
- Refine Overview around redo pressure, current Top SQL, scan and spill efficiency, blocker identity, purge history, and conditional replication and data-coverage status.
- Rename the project, executable, module, environment variable, local state, exports, Docker fixtures, and terminal branding from `mysqldot` to `mysq`; retain automatic snapshot migration and the legacy connection variable as compatibility paths.
- Rebuilt the TUI visual system around an adaptive light/dark palette, semantic health colors, a browser-style tab strip, full-width responsive diagnostic panes, gauges, finding cards, and compact key hints.
- Ignore MySQL server daemons such as `event_scheduler` when detecting long-running statements, preventing false critical health reports based on server uptime.
- Keep the complete agent-bundle destination visible in a persistent export confirmation instead of truncating it behind footer shortcuts.
- Add a data-only Engine view inspired by Database Insights: current SQL/user/host attribution, wait events, InnoDB I/O and redo/checkpoint state, active transactions, metadata locks, and MySQL memory consumers without changing health analysis.
