package runneldb

import (
	"io"
	"os"
	"sort"
)

// Compact prunes unreachable MVCC versions and rewrites the WAL.
// Versions required by open transactions are retained.
func (db *DB) Compact() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}

	retain := db.oldestReadVersion()
	db.pruneHistory(retain)

	tmpPath := db.path + ".compact"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeWALHeader(tmp); err != nil {
		return err
	}
	if _, err := tmp.Seek(walHeaderSize, 0); err != nil {
		return err
	}

	records := db.compactRecords(retain)
	for _, rec := range records {
		if err := appendRecord(tmp, rec); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	old := db.file
	if err := old.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, db.path); err != nil {
		// Best-effort reopen of the previous file if rename fails.
		reopened, reopenErr := os.OpenFile(db.path, os.O_RDWR, 0o600)
		if reopenErr == nil {
			db.file = reopened
			_, _ = reopened.Seek(0, io.SeekEnd)
		}
		return err
	}
	ok = true

	reopened, err := os.OpenFile(db.path, os.O_RDWR, 0o600)
	if err != nil {
		db.closed = true
		return err
	}
	if _, err := reopened.Seek(0, io.SeekEnd); err != nil {
		_ = reopened.Close()
		db.closed = true
		return err
	}
	db.file = reopened
	db.compactions.Add(1)
	return nil
}

// pruneHistory drops versions not needed for retain or newer snapshots.
// Caller must hold db.mu.
func (db *DB) pruneHistory(retain uint64) {
	for key, versions := range db.history {
		kept := pruneVersions(versions, retain)
		if len(kept) == 0 {
			delete(db.history, key)
		} else {
			db.history[key] = kept
		}
	}
}

func pruneVersions(versions []valueVersion, retain uint64) []valueVersion {
	if len(versions) == 0 {
		return nil
	}
	baseIdx := -1
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].version <= retain {
			baseIdx = i
			break
		}
	}
	var out []valueVersion
	if baseIdx >= 0 {
		if versions[baseIdx].deleted {
			// Keep delete tombstone only if newer versions exist after retain.
			hasNewer := false
			for i := baseIdx + 1; i < len(versions); i++ {
				if versions[i].version > retain {
					hasNewer = true
					break
				}
			}
			if hasNewer {
				out = append(out, versions[baseIdx])
			}
		} else {
			out = append(out, versions[baseIdx])
		}
		for i := baseIdx + 1; i < len(versions); i++ {
			if versions[i].version > retain {
				out = append(out, versions[i])
			}
		}
	} else {
		// All versions are after retain; keep them all.
		out = append(out, versions...)
	}
	if len(out) == 0 {
		return nil
	}
	// Clone slice backing store so later appends to history don't alias.
	return append([]valueVersion(nil), out...)
}

// compactRecords builds WAL records that rebuild retained history.
// Caller must hold db.mu.
func (db *DB) compactRecords(retain uint64) []record {
	if retain == db.version {
		// Single snapshot of live keys at the current version.
		keys := make([]string, 0, len(db.history))
		for key := range db.history {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ops := make([]operation, 0, len(keys))
		for _, key := range keys {
			versions := db.history[key]
			if len(versions) == 0 {
				continue
			}
			last := versions[len(versions)-1]
			if last.deleted {
				continue
			}
			ops = append(ops, operation{Key: key, Value: clone(last.value)})
		}
		if len(ops) == 0 {
			return nil
		}
		return []record{{Version: db.version, Ops: ops}}
	}

	// General case: emit one record per retained version group.
	type verOps struct {
		version uint64
		ops     []operation
	}
	byVersion := map[uint64]*verOps{}
	keys := make([]string, 0, len(db.history))
	for key := range db.history {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, v := range db.history[key] {
			group, ok := byVersion[v.version]
			if !ok {
				group = &verOps{version: v.version}
				byVersion[v.version] = group
			}
			group.ops = append(group.ops, operation{
				Key: key, Value: clone(v.value), Delete: v.deleted,
			})
		}
	}
	versions := make([]uint64, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	out := make([]record, 0, len(versions))
	for _, v := range versions {
		out = append(out, record{Version: v, Ops: byVersion[v].ops})
	}
	return out
}
