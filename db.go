// Package runneldb provides an experimental MVCC embedded storage engine.
package runneldb

import (
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
	Version   uint64 `json:"version"`
	Reads     uint64 `json:"reads"`
	Commits   uint64 `json:"commits"`
	Conflicts uint64 `json:"conflicts"`
	Keys      int    `json:"keys"`
}

// DB is an embedded database backed by an append-only write-ahead log.
type DB struct {
	mu        sync.RWMutex
	file      *os.File
	history   map[string][]valueVersion
	version   uint64
	closed    bool
	reads     atomic.Uint64
	commits   atomic.Uint64
	conflicts atomic.Uint64
}

// Open opens or creates a database at path and replays its WAL.
func Open(path string) (*DB, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	db := &DB{file: file, history: make(map[string][]valueVersion)}
	if err := replay(file, db.apply); err != nil {
		_ = file.Close()
		return nil, err
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
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	return &Tx{
		db: db, readVersion: db.version, writable: writable,
		writes: make(map[string]operation),
	}, nil
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
	return Metrics{
		Version: db.version, Reads: db.reads.Load(), Commits: db.commits.Load(),
		Conflicts: db.conflicts.Load(), Keys: keys,
	}
}

// Close flushes and closes the database.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	return db.file.Close()
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
