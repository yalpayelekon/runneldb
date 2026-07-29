# Vision

RunnelDB aims to become an embedded SQL database that feels native in Go:
simple to ship, easy to inspect, and designed for modern concurrent services.

## Principles

- **Embeddable first.** One library, one file, no daemon required.
- **Pure Go.** No CGO in the core.
- **Concurrency is a feature.** Stable snapshots and optimistic writes are
  fundamental, not extensions.
- **Operations should disappear.** Maintenance can happen incrementally in the
  background.
- **The truth is observable.** Metrics and tracing belong in the engine.
- **Compatibility over novelty.** Common SQL should feel familiar.

## Non-goals

- wire compatibility with SQLite
- automatic distributed consensus in the core
- a heavy ORM
- multiple pluggable storage engines
- enterprise-only features

## Architecture sketch

```text
Go API / HTTP / future database/sql driver
                  |
          transactions + MVCC
                  |
       indexes / future SQL executor
                  |
       append-only WAL + snapshots
                  |
               one file
```

The current implementation deliberately begins at the transaction and WAL
layers. Every higher-level feature should rest on a small, testable core.

## Compaction today vs later

Stage 1 exposes an explicit `Compact()` API so reclaiming history and rewriting
the WAL is testable and operator-driven. Automatic background checkpoints and
online maintenance remain a later stage, in line with the principle that
operations should eventually disappear without callers having to schedule them.
