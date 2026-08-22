# Changelog

All notable changes to mysq are documented here. The project follows Semantic Versioning.

## [Unreleased]

### Added

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

- Reorder investigation views to Overview, Connections, Queries, Engine, Findings, Tables, and Config; use Left/Right for tabs and Up/Down for viewport scrolling, while Queries keeps an Up/Down and Enter/Esc master-detail workflow with a smaller high-value column set that retains current user attribution.
- Replace the generic Overview engine strip with current MySQL load, query health, and contention panels; enrich Engine with sampled waits, file I/O, errors, replication, and instrumentation coverage without adding host metrics or new analysis rules.
- Refine Overview around redo pressure, current Top SQL, scan and spill efficiency, blocker identity, purge history, and conditional replication and data-coverage status.
- Rename the project, executable, module, environment variable, local state, exports, Docker fixtures, and terminal branding from `mysqldot` to `mysq`; retain automatic snapshot migration and the legacy connection variable as compatibility paths.
- Rebuilt the TUI visual system around an adaptive light/dark palette, semantic health colors, a browser-style tab strip, full-width responsive diagnostic panes, gauges, finding cards, and compact key hints.
- Ignore MySQL server daemons such as `event_scheduler` when detecting long-running statements, preventing false critical health reports based on server uptime.
- Keep the complete agent-bundle destination visible in a persistent export confirmation instead of truncating it behind footer shortcuts.
- Add a data-only Engine view inspired by Database Insights: current SQL/user/host attribution, wait events, InnoDB I/O and redo/checkpoint state, active transactions, metadata locks, and MySQL memory consumers without changing health analysis.
