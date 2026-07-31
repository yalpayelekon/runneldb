# RunnelDB

**A modern embedded database kernel for Go.**

RunnelDB is an experimental, pure-Go storage engine exploring a simple idea:
an embedded database can keep SQLite's delightful deployment model while
embracing MVCC, background work, streaming replication, and observable
operations.

> [!WARNING]
> RunnelDB is pre-alpha. The file format and API will change. Do not use it for
> important data yet. On-disk WALs use format version 1 (`RNDB` magic); older
> unheadered files are not readable.

## What works today

- snapshot-isolated reads
- optimistic concurrent transactions
- atomic, checksummed, append-only write-ahead log (format v1)
- recovery after reopen, including truncate of a torn or corrupt tail
- point-in-time read snapshots
- explicit `Compact()` to prune unreachable versions and rewrite the WAL
- background auto-checkpoint (commit-count or WAL-size threshold)
- 4 KiB slotted pages and typed row codec for SQL storage
- per-table in-memory B+tree primary-key indexes (rebuilt on open)
- online secondary indexes (`CREATE INDEX` / `DROP INDEX`) on INTEGER or TEXT columns
- binary `JSON` columns with `json_extract` and path indexes (`CREATE INDEX ... PATH`)
- minimal SQLite-flavored SQL: `CREATE`/`DROP TABLE`, `CREATE`/`DROP INDEX`,
  `INSERT`, `SELECT`, `UPDATE`, `DELETE` with `?` placeholders
- lock-free reads after transaction start
- built-in operation, conflict, compaction, checkpoint, and WAL-size counters
- JSON-over-HTTP (`/v1/kv`, `/v1/sql`, `/v1/metrics`, `/v1/compact`, `/v1/checkpoint`)
- zero third-party runtime dependencies

The key/value API remains the durable kernel. SQL tables are stored under the
reserved `__rdb__/` key prefix (rejected for direct KV writes).

## Quick start

```go
package main

import (
	"fmt"
	"log"

	"github.com/yalpayelekon/runneldb"
)

func main() {
	db, err := runneldb.Open("example.wal")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users (id, name) VALUES (?, ?)`, int64(1), "ada")
	if err != nil {
		log.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM users WHERE id = ?`, int64(1))
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var name string
		_ = rows.Scan(&name)
		fmt.Println(name)
	}
}
```

Or run the server:

```bash
go run ./cmd/runneldb serve --path data.wal --addr :7070
curl -X POST localhost:7070/v1/sql -d '{"sql":"CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"}'
curl -X POST localhost:7070/v1/sql -d '{"sql":"INSERT INTO t VALUES (1, \"hi\")"}'
curl -X POST localhost:7070/v1/sql -d '{"sql":"SELECT * FROM t WHERE id = 1"}'
curl -X POST localhost:7070/v1/compact
curl localhost:7070/v1/metrics
```

## Durability and recovery

Each commit appends a length-prefixed, CRC32-protected JSON record and syncs
the file. On `Open`, RunnelDB validates the 16-byte file header, replays
records, and **truncates a torn or corrupt final record**. Corruption that is
followed by another valid record fails open (`ErrWALCorrupt`). SQL catalog and
B+tree indexes are rebuilt from the recovered key space.

## Compaction and checkpoints

`DB.Compact()` (or `POST /v1/compact`, `POST /v1/checkpoint`) drops MVCC
versions older than the oldest open transaction snapshot and atomically rewrites
the WAL. With no open readers it writes a single snapshot of live keys.

By default `Open` enables a background checkpoint worker that compacts
automatically when the commit count reaches 256 or the WAL exceeds 1 MiB. Use
`OpenWithOptions` to tune thresholds or disable auto-checkpoint:

```go
db, err := runneldb.OpenWithOptions("data.wal", runneldb.Options{
    AutoCheckpoint:        true,
    CheckpointEveryCommit: 128,
    CheckpointMinBytes:    512 << 10,
})
```

## Secondary indexes

Single-column secondary indexes accelerate equality lookups on non-PK columns:

```sql
CREATE INDEX idx_name ON users(name);
SELECT * FROM users WHERE name = 'ada';   -- uses the index
DROP INDEX idx_name;
```

Indexes are built online (concurrent reads continue), persisted in the catalog,
and rebuilt on reopen. DML statements automatically maintain all secondary
indexes on the affected table.

## Binary JSON

`JSON` columns store a compact binary encoding. INSERT/UPDATE accept JSON text;
SELECT returns JSON as text. Use SQLite-flavored `json_extract` and optional
path indexes:

```sql
CREATE TABLE docs (id INTEGER PRIMARY KEY, doc JSON);
INSERT INTO docs VALUES (1, '{"name":"ada","age":36}');
CREATE INDEX idx_name ON docs(doc) PATH '$.name';
SELECT json_extract(doc, '$.name') FROM docs
  WHERE json_extract(doc, '$.name') = 'ada';
```

Paths are `$` + `.ident` segments only (no array indexes or wildcards). Path
indexes cover string and integral leaves; missing or non-indexable paths omit
the row from the index.

## Concurrency model

Each transaction reads from a stable version. Commits are serialized briefly
while RunnelDB checks whether any written key changed after that version.
Transactions touching different keys can be prepared concurrently; conflicting
writes fail with `ErrConflict` and may be retried.

## Direction

The roadmap is intentionally staged:

1. ~~harden the MVCC/WAL kernel, fuzz recovery, add compaction~~
2. ~~pages, indexes, and a minimal SQLite-flavored SQL parser~~
3. ~~background checkpoints and online index construction~~
4. ~~binary JSON with indexed paths~~
5. vector indexes and streaming WAL replication

See [docs/vision.md](docs/vision.md) for principles and non-goals.

## Project status

RunnelDB is a new open-source experiment. Design discussion is encouraged.
The API uses semantic versioning once the first tagged release exists.

## License

[MIT](LICENSE)
