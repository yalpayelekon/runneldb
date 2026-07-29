package runneldb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IndexDef is the persisted catalog entry for a secondary index.
type IndexDef struct {
	Name   string `json:"name"`
	Table  string `json:"table"`
	Column string `json:"column"`
}

func catalogIndexKey(name string) string {
	return reservedPrefix + "meta/indexes/" + name
}

func catalogIndexPrefix() string {
	return reservedPrefix + "meta/indexes/"
}

func saveIndexDef(tx *Tx, def IndexDef) error {
	payload, err := json.Marshal(def)
	if err != nil {
		return err
	}
	return tx.put(catalogIndexKey(def.Name), payload)
}

func dropIndexDef(tx *Tx, name string) error {
	return tx.del(catalogIndexKey(name))
}

func listIndexDefs(tx *Tx) ([]IndexDef, error) {
	pairs := tx.scanPrefix(catalogIndexPrefix())
	out := make([]IndexDef, 0, len(pairs))
	for _, p := range pairs {
		var def IndexDef
		if err := json.Unmarshal(p.Value, &def); err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}

// secondaryIdx is an in-memory B+tree secondary index.
// The composite key is (indexedValue, pk) so multiple rows can share a value.
type secondaryIdx struct {
	def  IndexDef
	tree *btree
}

func newSecondaryIdx(def IndexDef) *secondaryIdx {
	return &secondaryIdx{def: def, tree: newBTree()}
}

// compositeKey builds a B+tree key from the indexed column value and the PK.
func compositeKey(val Value, pk pkKey) pkKey {
	var prefix string
	switch val.Typ {
	case TypeInteger:
		prefix = fmt.Sprintf("I:%d", val.Int)
	case TypeText:
		prefix = "T:" + val.Text
	default:
		prefix = "N:"
	}
	return pkFromString(prefix + "\x00" + formatPK(pk))
}

// parseCompositeKey extracts the pk portion from a composite key string.
func parseCompositeKey(key pkKey) (pkKey, error) {
	parts := strings.SplitN(key.s, "\x00", 2)
	if len(parts) < 2 {
		return pkKey{}, fmt.Errorf("%w: bad composite key", ErrSQL)
	}
	return pkFromString(parts[1]), nil
}

// compositePrefix returns a prefix-seekable key for exact value match.
func compositePrefix(val Value) pkKey {
	var prefix string
	switch val.Typ {
	case TypeInteger:
		prefix = fmt.Sprintf("I:%d", val.Int)
	case TypeText:
		prefix = "T:" + val.Text
	default:
		prefix = "N:"
	}
	return pkFromString(prefix + "\x00")
}

func (si *secondaryIdx) insert(val Value, pk pkKey, rk string) {
	ck := compositeKey(val, pk)
	si.tree.Put(ck, rk)
}

func (si *secondaryIdx) remove(val Value, pk pkKey) {
	ck := compositeKey(val, pk)
	si.tree.Delete(ck)
}

// lookup returns all row keys matching the exact indexed value.
func (si *secondaryIdx) lookup(val Value) []string {
	prefix := compositePrefix(val)
	it := si.tree.Seek(prefix)
	var out []string
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if !strings.HasPrefix(k.s, prefix.s) {
			break
		}
		out = append(out, v)
	}
	return out
}
