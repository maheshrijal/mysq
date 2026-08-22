# Changelog

All notable changes to mysqldot are documented here. The project follows Semantic Versioning.

## [Unreleased]

### Added

- Findings-first MySQL 8.0/8.4 inspection with deterministic health scoring.
- Adaptive six-view interactive terminal with refresh and one-key agent export.
- Statement, table, index, process, connection-group, lock, replication, configuration, and InnoDB diagnostics.
- Text, versioned JSON, Markdown, focused JSON, and severity-based CI output contracts.
- Atomic 17-file agent bundle with JSON Schema, CSV/TXT evidence, ZIP support, and SHA-256 manifest.
- Compressed local snapshot history and offline metric/finding/query diffs.
- Docker-only MySQL 8.4 load and PTY end-to-end test harness.
- Static Docker image and cross-platform GoReleaser configuration.

### Changed

- Rebuilt the TUI visual system around an adaptive light/dark palette, semantic health colors, a browser-style tab strip, full-width responsive diagnostic panes, gauges, finding cards, and compact key hints.
- Ignore MySQL server daemons such as `event_scheduler` when detecting long-running statements, preventing false critical health reports based on server uptime.
- Make all four arrow keys navigate views consistently; retain `j`/`k` and paging keys for scrolling within a view.
- Keep the complete agent-bundle destination visible in a persistent export confirmation instead of truncating it behind footer shortcuts.
