package runneldb

import "sort"

// kvPair is a key/value visible to a transaction snapshot.
type kvPair struct {
	Key   string
	Value []byte
}

// scanPrefix returns snapshot-visible key/value pairs with the given prefix,
// including the transaction's own writes. Results are sorted by key.
func (tx *Tx) scanPrefix(prefix string) []kvPair {
	if tx.closed {
		return nil
	}
	seen := map[string]struct{}{}
	var out []kvPair

	add := func(key string, value []byte, deleted bool) {
		if !stringsHasPrefix(key, prefix) {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if deleted {
			return
		}
		out = append(out, kvPair{Key: key, Value: clone(value)})
	}

	for key, op := range tx.writes {
		add(key, op.Value, op.Delete)
	}

	tx.db.mu.RLock()
	for key, versions := range tx.db.history {
		if _, ok := seen[key]; ok {
			continue
		}
		if !stringsHasPrefix(key, prefix) {
			continue
		}
		for i := len(versions) - 1; i >= 0; i-- {
			if versions[i].version <= tx.readVersion {
				add(key, versions[i].value, versions[i].deleted)
				break
			}
		}
	}
	tx.db.mu.RUnlock()
	tx.db.reads.Add(uint64(len(out)))

	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
