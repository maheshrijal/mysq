# Performance

## Debug timing logs

Use `mysq tui --debug-log /path/to/new-debug.jsonl` with your usual connection setup to investigate a real slowdown. For a single report, use `mysq inspect --no-store --debug-log /path/to/new-debug.jsonl`. The flag enables logging; there is no separate `--debug` switch. Parent directories must already exist. The file is created with mode `0600`, and existing paths, including symlinks, are rejected before the command runs. Normal terminal and JSON output stay unchanged.

Reproduce the slow action and quit with `q`. Events are written immediately, so another terminal can follow the file with `tail -f /path/to/new-debug.jsonl`. Each line contains a timestamp, phase, and operation. Start/end pairs share an `id`; end events include wall-clock `duration_ms`. A start without an end identifies an operation still running, or interrupted at shutdown. Session events identify the binary version. Result events provide `ok`, `error`, `timeout`, or `canceled` where instrumented, without recording raw errors.

`collect.openConnection` includes connection establishment and session setup. Collector function spans cover database calls and row decoding. `sampling.wait` identifies the intentional observation delay (one second by default). `analyze`, `history.save`, `trends.sample`, and `tui.update`, `tui.rebuild`, and `tui.view` separate local work from collection. Parent spans include their children: do not add all durations together. TUI spans measure application work, not the terminal emulator's painting or keyboard delivery. This is timing instrumentation, not a CPU profile or SQL trace.

The log excludes connection strings, endpoints, SQL text, results, and keyboard input. Visible errors and ordinary exports retain their existing behavior; the timing file is separate. Logging adds file I/O, so use a local disk and capture a short reproduction. Files are not rotated. Without the flag, timing calls are no-ops and no log file is opened.

## Parallel full inspections

Full inspections use three optional-probe workers alongside one pinned primary connection. Independent metadata probes and counter endpoint batches overlap, while the primary global-status window remains free of collector queries. Optional tasks have a three-second client budget and worker sessions have a three-second server statement limit. Slow optional sections degrade explicitly; focused commands retain their ten-second statement limit. A failed counter baseline is not queried again. NULL error numbers are retained as the uninstrumented error bucket (number zero).

These changes shorten serial waits without increasing concurrency beyond four diagnostic clients. Full snapshots still wait for all scheduled work; three seconds is a per-task budget, not a whole-startup guarantee. TUI graph sampling and explicitly opened query-control sessions are separate from this limit. On a busy server, parallel reads can contend for resources, so compare debug captures on the affected database rather than extrapolating a localhost benchmark.

A 2026-09-05 local comparison used MySQL 8.4, a TCP proxy delaying each direction by 12.5 ms per relay read, and `inspect --format json --no-store --interval 100ms`. Six measured pairs after one warmup pair alternated execution order on the same fixture. The sequential `caadd38` binary had a 1,198.30 ms median; the parallel candidate had a 941.20 ms median (21.5% lower), with all 19 current capabilities available and every sample window at least 100 ms. This isolates network-latency savings; it does not reproduce the affected database's expensive summary queries or predict its startup time.

## Command benchmark

mysq has an end-to-end command benchmark because its useful latency includes the MySQL connection, real Performance Schema queries, sampling, analysis, and output serialization. Microbenchmarks of individual Go helpers would miss the dominant costs.

Run it with:

```bash
make benchmark
```

The benchmark creates a uniquely named fresh `mysql:8.4` container backed by tmpfs on an ephemeral `127.0.0.1` port, seeds the end-to-end schema, and keeps eight concurrent OLTP workers running until the benchmark finishes. Each command gets five warmups followed by 50 measured process executions. Every invocation must emit typed, section-appropriate JSON evidence: full inspections require all 17 fixture probes and the requested interval marker, while each focused sampled command must produce a derived counter delta or rate somewhere in the measured run. That proves its two-endpoint path remained active without rejecting a legitimately quiet 100 ms window. Legitimately quiet statement and full-inspection wait samples may remain empty. Validation happens after the invocation timer stops. The supervisor fails the run if the workload exits early. It reports wall-clock median, p95, minimum, and maximum latency, then tears down only its own container, processes, and temporary artifacts.

For a longer run:

```bash
bash test/benchmark/run.sh --runs 100 --warmup 10
```

To compare a prebuilt baseline without changing the checkout, set `MYSQ_BENCHMARK_BASELINE` to that binary. The runner alternates baseline-first and candidate-first execution for every command on the same live fixture, removing the systematic workload-age skew of separate serial runs. `MYSQ_BENCHMARK_BINARY` can override the candidate binary; otherwise the current checkout is built.

The recorded comparison below used the default paired invocation, with a baseline binary built from `cd9f70c`:

```bash
MYSQ_BENCHMARK_BASELINE=/absolute/path/to/mysq-cd9f70c \
  bash test/benchmark/run.sh --runs 50 --warmup 5
```

## 2026-08-23 result

Measured on an Apple M4 Pro with Go 1.26.7, Docker Engine 29.7.2, and MySQL 8.4. The baseline binary was built from commit `cd9f70c`; the candidate product code and benchmark harness were commit `3ec920f`. Baseline and candidate were paired and order-balanced on the same fresh container using the command above.

| Command | Baseline median | Optimized median | Change | Baseline p95 | Optimized p95 |
|---|---:|---:|---:|---:|---:|
| `inspect --full` | 176.54 ms | 177.08 ms | +0.3% | 180.54 ms | 181.81 ms |
| `queries` | 168.35 ms | 18.69 ms | -88.9% | 176.31 ms | 23.75 ms |
| `tables` | 167.26 ms | 12.57 ms | -92.5% | 176.09 ms | 21.37 ms |
| `variables` | 166.40 ms | 11.70 ms | -93.0% | 176.16 ms | 15.05 ms |
| `waits` | 174.64 ms | 145.57 ms | -16.6% | 181.72 ms | 150.02 ms |
| `io` | 173.89 ms | 119.17 ms | -31.5% | 182.27 ms | 122.10 ms |
| `errors` | 175.04 ms | 121.85 ms | -30.4% | 180.95 ms | 125.30 ms |
| `engine` | 174.75 ms | 122.70 ms | -29.8% | 182.39 ms | 126.50 ms |

The bottleneck was structural: every focused command invoked the complete collector, including unrelated catalog and Performance Schema probes and the minimum 100 ms sampling window. Point-in-time commands now collect only their required evidence and return immediately. Sampled commands still honor the observation interval, but no longer perform unrelated probes. The full inspection deliberately retains its complete evidence contract and shows no material median regression.

Absolute timings vary with host load. Compare changes using paired binaries, the same run count, and the same machine; the relative gap between full and focused collection is the durable signal.
