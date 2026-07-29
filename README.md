# RunnelDB

**A modern embedded database kernel for Go.**

RunnelDB is an experimental, pure-Go storage engine exploring a simple idea:
an embedded database can keep SQLite's delightful deployment model while
embracing MVCC, background work, streaming replication, and observable
operations.

> [!WARNING]
> RunnelDB is pre-alpha. The file format and API will change. Do not use it for
> important data yet.

## What works today

- snapshot-isolated reads
- optimistic concurrent transactions
- atomic, checksummed, append-only write-ahead log
- recovery after reopen
- point-in-time read snapshots
- lock-free reads after transaction start
- built-in operation and conflict counters
- a small JSON-over-HTTP server
- zero third-party runtime dependencies

SQL is not implemented yet. The current key/value API is the storage kernel on
which the SQL layer will be built.

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

	err = db.Update(func(tx *runneldb.Tx) error {
		tx.Set("hello", []byte("world"))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	_ = db.View(func(tx *runneldb.Tx) error {
		value, _ := tx.Get("hello")
		fmt.Println(string(value))
		return nil
	})
}
```

Or run the server:

```bash
go run ./cmd/runneldb serve --path data.wal --addr :7070
curl -X PUT localhost:7070/v1/kv/hello -d world
curl localhost:7070/v1/kv/hello
curl localhost:7070/v1/metrics
```

## Concurrency model

Each transaction reads from a stable version. Commits are serialized briefly
while RunnelDB checks whether any written key changed after that version.
Transactions touching different keys can be prepared concurrently; conflicting
writes fail with `ErrConflict` and may be retried.

## Direction

The roadmap is intentionally staged:

1. harden the MVCC/WAL kernel, fuzz recovery, add compaction
2. pages, indexes, and a minimal SQLite-flavored SQL parser
3. background checkpoints and online index construction
4. binary JSON with indexed paths
5. vector indexes and streaming WAL replication

See [docs/vision.md](docs/vision.md) for principles and non-goals.

## Project status

RunnelDB is a new open-source experiment. Design discussion is encouraged.
The API uses semantic versioning once the first tagged release exists.

## License

[MIT](LICENSE)
