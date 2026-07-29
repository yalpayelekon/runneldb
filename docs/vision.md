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
     SQL Exec/Query (minimal dialect)
                  |
  catalog + B+tree PK + secondary indexes
                  |
           4KiB slotted pages / rows
                  |
          transactions + MVCC
                  |
  append-only WAL + background checkpoints
                  |
               one file
```

## Compaction and background checkpoints

Stage 1 exposed an explicit `Compact()` API. Stage 3 adds a background
checkpoint worker that periodically compacts the WAL when commit count or file
size thresholds are met. The worker runs as a single goroutine per `DB`,
started on `Open` (configurable via `OpenWithOptions`), and stopped cleanly by
`Close`. The checkpoint still rewrites the single WAL file—a separate pagefile
format remains a future stage.

## Secondary indexes

Stage 3 introduces online secondary indexes (`CREATE INDEX` / `DROP INDEX`) on
single non-PK INTEGER or TEXT columns. Indexes are built by scanning the table
under successive read snapshots, then swapped live under the write lock. DML
maintains all secondary trees automatically. The query planner uses a secondary
index for single-column equality when no PK equality is present. Multi-column
and unique secondary indexes are deferred.
