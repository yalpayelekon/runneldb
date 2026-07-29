package runneldb

import "sort"

// Tx is a read-only or writable transaction with a stable read snapshot.
type Tx struct {
	db          *DB
	readVersion uint64
	writable    bool
	writes      map[string]operation
	closed      bool
}

// Get returns the value visible to this transaction.
func (tx *Tx) Get(key string) ([]byte, bool) {
	if tx.closed {
		return nil, false
	}
	if op, ok := tx.writes[key]; ok {
		tx.db.reads.Add(1)
		if op.Delete {
			return nil, false
		}
		return clone(op.Value), true
	}
	tx.db.mu.RLock()
	defer tx.db.mu.RUnlock()
	tx.db.reads.Add(1)
	versions := tx.db.history[key]
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].version <= tx.readVersion {
			if versions[i].deleted {
				return nil, false
			}
			return clone(versions[i].value), true
		}
	}
	return nil, false
}

// Set records a value to be written on commit.
func (tx *Tx) Set(key string, value []byte) error {
	if tx.closed {
		return ErrTxClosed
	}
	if !tx.writable {
		return ErrReadOnly
	}
	tx.writes[key] = operation{Key: key, Value: clone(value)}
	return nil
}

// Delete records a key deletion to be applied on commit.
func (tx *Tx) Delete(key string) error {
	if tx.closed {
		return ErrTxClosed
	}
	if !tx.writable {
		return ErrReadOnly
	}
	tx.writes[key] = operation{Key: key, Delete: true}
	return nil
}

// Commit atomically persists the transaction if none of its keys changed.
func (tx *Tx) Commit() error {
	if tx.closed {
		return ErrTxClosed
	}
	if !tx.writable {
		tx.closed = true
		return nil
	}
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.db.closed {
		return ErrClosed
	}
	keys := make([]string, 0, len(tx.writes))
	for key := range tx.writes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		versions := tx.db.history[key]
		if len(versions) > 0 && versions[len(versions)-1].version > tx.readVersion {
			tx.db.conflicts.Add(1)
			tx.closed = true
			return ErrConflict
		}
	}
	if len(keys) == 0 {
		tx.closed = true
		return nil
	}
	ops := make([]operation, 0, len(keys))
	for _, key := range keys {
		ops = append(ops, tx.writes[key])
	}
	rec := record{Version: tx.db.version + 1, Ops: ops}
	if err := appendRecord(tx.db.file, rec); err != nil {
		return err
	}
	tx.db.apply(rec)
	tx.db.commits.Add(1)
	tx.closed = true
	return nil
}

// Rollback discards pending writes. It is safe to call more than once.
func (tx *Tx) Rollback() {
	tx.closed = true
	tx.writes = nil
}
