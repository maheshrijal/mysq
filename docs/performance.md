# Performance

mysq has an end-to-end command benchmark because its useful latency includes the MySQL connection, real Performance Schema queries, sampling, analysis, and output serialization. Microbenchmarks of individual Go helpers would miss the dominant costs.

Run it with:

```bash
make benchmark
```

The benchmark creates a uniquely named fresh `mysql:8.4` container backed by tmpfs on an ephemeral `127.0.0.1` port, seeds the end-to-end schema, and keeps eight concurrent OLTP workers running until the benchmark finishes. Each command gets five warmups followed by 50 measured process executions. Every invocation must emit typed, section-appropriate JSON evidence; validation happens after the invocation timer stops. The supervisor fails the run if the workload exits early. It reports wall-clock median, p95, minimum, and maximum latency, then tears down only its own container, processes, and temporary artifacts.

For a longer run:

```bash
bash test/benchmark/run.sh --runs 100 --warmup 10
```

To compare a prebuilt baseline without changing the checkout, set `MYSQ_BENCHMARK_BASELINE` to that binary. The runner alternates baseline-first and candidate-first execution for every command on the same live fixture, removing the systematic workload-age skew of separate serial runs. `MYSQ_BENCHMARK_BINARY` can override the candidate binary; otherwise the current checkout is built.

## 2026-08-23 result

Measured on an Apple M4 Pro with Go 1.26.7, Docker Engine 29.7.2, and MySQL 8.4. The baseline was commit `cd9f70c`; baseline and candidate were paired and order-balanced on the same fresh container using the 50-run benchmark above.

| Command | Baseline median | Optimized median | Change | Baseline p95 | Optimized p95 |
|---|---:|---:|---:|---:|---:|
| `inspect --full` | 167.84 ms | 166.15 ms | -1.0% | 188.58 ms | 187.63 ms |
| `queries` | 172.71 ms | 19.85 ms | -88.5% | 188.14 ms | 26.76 ms |
| `tables` | 164.68 ms | 13.09 ms | -92.0% | 185.69 ms | 24.52 ms |
| `variables` | 165.51 ms | 15.36 ms | -90.7% | 182.12 ms | 18.86 ms |
| `waits` | 172.27 ms | 145.18 ms | -15.7% | 195.28 ms | 151.63 ms |
| `engine` | 166.73 ms | 120.18 ms | -27.9% | 174.63 ms | 124.97 ms |

The bottleneck was structural: every focused command invoked the complete collector, including unrelated catalog and Performance Schema probes and the minimum 100 ms sampling window. Point-in-time commands now collect only their required evidence and return immediately. Sampled commands still honor the observation interval, but no longer perform unrelated probes. The full inspection deliberately retains its complete evidence contract and shows no material median regression.

Absolute timings vary with host load. Compare changes using paired binaries, the same run count, and the same machine; the relative gap between full and focused collection is the durable signal.
