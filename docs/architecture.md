# Architecture

mysqldot is organized around one deep diagnostic module: an inspection produces a complete `model.Context`, and every downstream behavior consumes it.

```text
MySQL 8.x
   │ read-only queries, one pinned connection
   ▼
collector ──► versioned Context ──► deterministic analyzer
                                      │
              ┌───────────────────────┼────────────────────────┐
              ▼                       ▼                        ▼
        terminal / TUI          history + diff          agent bundle
```

The `Context` interface includes the versioned JSON shape, collection timestamp and interval, explicit probe capabilities, server identity, raw counters, normalized statements with point-in-time user attribution, wait and memory summaries, active transactions and locks, evidence tables, deterministic findings, and health score. Renderers do not recompute facts. Exports do not re-query MySQL. History does not retain connection configuration.

## Modules

- `internal/collect` owns connection parsing, session safety, probes, capability degradation, and rate calculation. Its external interface is `Inspect(Target) -> Context`.
- `internal/analyze` owns deterministic thresholds, caveats, recommendations, ordering, and health scoring. It has no I/O.
- `internal/render` and `internal/tui` are independent adapters over the same context. The TUI's refresh callback is its only live seam.
- `internal/export` atomically materializes the agent contract and integrity manifest. It refuses overwrites.
- `internal/history` stores gzip JSON by server/database fingerprint. `internal/compare` remains pure and performs offline comparisons.
- `internal/sanitize` is intentionally early in the flow. Potential SQL literals are discarded before the context crosses any persistence or rendering seam.

The collector degrades individual optional probes, but server identity and two counter samples are required. This prevents a visually complete report built from absent data. Each optional failure appears in capabilities, collection warnings, the full report, the TUI Config view, and exports.

## Database cost and safety

An inspection uses one connection and samples `SHOW GLOBAL STATUS` twice around the configured interval. All other work happens before that rate window and is bounded catalog or Performance Schema access with fixed limits: 20 statement digests, 30 wait events, 30 memory consumers, 100 tables, 100 processes, 100 transactions, 100 metadata locks, and no application-row queries. `MAX_EXECUTION_TIME` is 10 seconds and the session is pinned read-only. Active user, process, transaction, and lock data is explicitly point-in-time and can change while the report is being consumed.

Some Performance Schema summary tables grow with schema size. Their queries do not scan application data, but operators should still validate run time on unusually large fleets before aggressive scheduling. mysqldot is an on-demand diagnostic, not a monitoring daemon.
