# Performance

mysq has an end-to-end command benchmark because its useful latency includes the MySQL connection, real Performance Schema queries, sampling, analysis, and output serialization. Microbenchmarks of individual Go helpers would miss the dominant costs.

Run it with:

```bash
make benchmark
```

The benchmark creates a fresh `mysql:8.4` container backed by tmpfs on `127.0.0.1:33307`, seeds the end-to-end schema, and runs eight concurrent OLTP workers. Each command gets three warmups followed by 15 measured process executions. The runner discards command output and reports wall-clock median, p95, minimum, and maximum latency. It tears down the container and temporary artifacts after the run.

For a longer run:

```bash
bash test/benchmark/run.sh --runs 50 --warmup 5
```

To compare a prebuilt baseline without changing the checkout, set `MYSQ_BENCHMARK_BINARY` to that binary before running the script.

## 2026-08-23 result

Measured on an Apple M4 Pro with Go 1.26.7, Docker Engine 29.7.2, and MySQL 8.4. The baseline was commit `cd9f70c`; both binaries were measured with the benchmark above against separate fresh, identically configured containers.

| Command | Baseline median | Optimized median | Change | Baseline p95 | Optimized p95 |
|---|---:|---:|---:|---:|---:|
| `inspect --full` | 165.73 ms | 169.48 ms | +2.3% | 194.07 ms | 191.89 ms |
| `queries` | 177.85 ms | 10.08 ms | -94.3% | 188.24 ms | 11.64 ms |
| `tables` | 186.30 ms | 8.79 ms | -95.3% | 200.49 ms | 9.48 ms |
| `variables` | 186.69 ms | 8.75 ms | -95.3% | 198.91 ms | 10.89 ms |
| `waits` | 189.60 ms | 163.26 ms | -13.9% | 202.90 ms | 167.96 ms |
| `engine` | 180.50 ms | 135.55 ms | -24.9% | 192.02 ms | 147.42 ms |

The bottleneck was structural: every focused command invoked the complete collector, including unrelated catalog and Performance Schema probes and the minimum 100 ms sampling window. Point-in-time commands now collect only their required evidence and return immediately. Sampled commands still honor the observation interval, but no longer perform unrelated probes. The full inspection deliberately retains its complete evidence contract and shows no material median regression.

Absolute timings vary with host load and the growth of the active benchmark dataset. Compare changes using fresh containers, the same run count, and the same machine; the relative gap between full and focused collection is the durable signal.
