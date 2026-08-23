# Performance

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

Measured on an Apple M4 Pro with Go 1.26.7, Docker Engine 29.7.2, and MySQL 8.4. The baseline binary was built from commit `cd9f70c`; the candidate product code and benchmark harness were commit `04547cb`. Baseline and candidate were paired and order-balanced on the same fresh container using the command above. Later E2E fixture and verification changes leave the measured product binary unchanged but are not part of this recorded benchmark harness.

| Command | Baseline median | Optimized median | Change | Baseline p95 | Optimized p95 |
|---|---:|---:|---:|---:|---:|
| `inspect --full` | 180.33 ms | 182.85 ms | +1.4% | 189.88 ms | 196.75 ms |
| `queries` | 175.58 ms | 19.16 ms | -89.1% | 205.87 ms | 25.78 ms |
| `tables` | 174.77 ms | 14.31 ms | -91.8% | 185.95 ms | 20.96 ms |
| `variables` | 176.55 ms | 15.36 ms | -91.3% | 198.67 ms | 21.70 ms |
| `waits` | 175.25 ms | 149.38 ms | -14.8% | 182.80 ms | 154.89 ms |
| `engine` | 174.74 ms | 120.93 ms | -30.8% | 196.32 ms | 127.22 ms |

The bottleneck was structural: every focused command invoked the complete collector, including unrelated catalog and Performance Schema probes and the minimum 100 ms sampling window. Point-in-time commands now collect only their required evidence and return immediately. Sampled commands still honor the observation interval, but no longer perform unrelated probes. The full inspection deliberately retains its complete evidence contract and shows no material median regression.

Absolute timings vary with host load. Compare changes using paired binaries, the same run count, and the same machine; the relative gap between full and focused collection is the durable signal.
