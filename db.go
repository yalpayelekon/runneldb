// Package runneldb provides an experimental MVCC embedded storage engine.
package runneldb

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
)

type valueVersion struct {
	version uint64
	value   []byte
	deleted bool
}

// Metrics is a point-in-time view of database activity.
type Metrics struct {
	Version     uint64 `json:"version"`
	Reads       uint64 `json:"reads"`
	Commits     uint64 `json:"commits"`
	Conflicts   uint64 `json:"conflicts"`
	Compactions uint64 `json:"compactions"`
	Checkpoints uint64 `json:"checkpoints"`
	WALBytes    int64  `json:"wal_bytes"`
	Keys        int    `json:"keys"`
}

// DB is an embedded database backed by an append-only write-ahead log.
type DB struct {
	mu          sync.RWMutex
	path        string
	file        *os.File
	history     map[string][]valueVersion
	version     uint64
	closed      bool
	active      map[*Tx]uint64
	tables      map[string]*TableDef
	indexes     map[string]*btree
	secIndexes  map[string]*secondaryIdx
	ckpt        *checkpointWorker
	reads       atomic.Uint64
	commits     atomic.Uint64
	conflicts   atomic.Uint64
	compactions atomic.Uint64
}

// Open opens or creates a database at path with default options.
func Open(path string) (*DB, error) {
	return OpenWithOptions(path, DefaultOptions())
}

// OpenWithOptions opens or creates a database at path with explicit options.
func OpenWithOptions(path string, opts Options) (*DB, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	db := &DB{
		path:       path,
		file:       file,
		history:    make(map[string][]valueVersion),
		active:     make(map[*Tx]uint64),
		tables:     make(map[string]*TableDef),
		indexes:    make(map[string]*btree),
		secIndexes: make(map[string]*secondaryIdx),
	}
	lastGood, err := initWAL(file, db.apply)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if info.Size() != lastGood {
		if err := file.Truncate(lastGood); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := db.rebuildSQLState(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if opts.AutoCheckpoint {
		w := newCheckpointWorker(db, opts)
		db.ckpt = w
		w.start()
	}
	return db, nil
}

func (db *DB) apply(rec record) {
	for _, op := range rec.Ops {
		db.history[op.Key] = append(db.history[op.Key], valueVersion{
			version: rec.Version, value: clone(op.Value), deleted: op.Delete,
		})
	}
	if rec.Version > db.version {
		db.version = rec.Version
	}
}

// Begin starts a transaction at the current stable version.
func (db *DB) Begin(writable bool) (*Tx, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, ErrClosed
	}
	tx := &Tx{
		db: db, readVersion: db.version, writable: writable,
		writes: make(map[string]operation),
	}
	db.active[tx] = tx.readVersion
	return tx, nil
}

func (db *DB) unregister(tx *Tx) {
	delete(db.active, tx)
}

// oldestReadVersion returns the oldest open snapshot, or db.version when none.
// Caller must hold db.mu.
func (db *DB) oldestReadVersion() uint64 {
	if len(db.active) == 0 {
		return db.version
	}
	var oldest uint64
	first := true
	for _, v := range db.active {
		if first || v < oldest {
			oldest = v
			first = false
		}
	}
	return oldest
}

// View runs fn in a read-only transaction.
func (db *DB) View(fn func(*Tx) error) error {
	tx, err := db.Begin(false)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// Update runs fn in a writable transaction and commits it.
func (db *DB) Update(fn func(*Tx) error) error {
	tx, err := db.Begin(true)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Metrics returns current activity counters.
func (db *DB) Metrics() Metrics {
	db.mu.RLock()
	defer db.mu.RUnlock()
	keys := 0
	for _, versions := range db.history {
		if len(versions) > 0 && !versions[len(versions)-1].deleted {
			keys++
		}
	}
	var walBytes int64
	if db.file != nil {
		if info, err := db.file.Stat(); err == nil {
			walBytes = info.Size()
		}
	}
	var ckpts uint64
	if db.ckpt != nil {
		ckpts = db.ckpt.checkpoints.Load()
	}
	return Metrics{
		Version: db.version, Reads: db.reads.Load(), Commits: db.commits.Load(),
		Conflicts: db.conflicts.Load(), Compactions: db.compactions.Load(),
		Checkpoints: ckpts, WALBytes: walBytes, Keys: keys,
	}
}

// Close flushes and closes the database.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	ckpt := db.ckpt
	db.ckpt = nil
	db.mu.Unlock()
	if ckpt != nil {
		ckpt.close()
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.file.Close()
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
