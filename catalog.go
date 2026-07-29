package runneldb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ColType is a SQL column type.
type ColType string

const (
	TypeInteger ColType = "INTEGER"
	TypeText    ColType = "TEXT"
	TypeBlob    ColType = "BLOB"
)

// ColumnDef describes a table column.
type ColumnDef struct {
	Name string  `json:"name"`
	Type ColType `json:"type"`
	PK   bool    `json:"pk,omitempty"`
}

// TableDef is the persisted catalog entry for a table.
type TableDef struct {
	Name    string      `json:"name"`
	Columns []ColumnDef `json:"columns"`
}

func (t *TableDef) pkColumn() (*ColumnDef, int, error) {
	for i := range t.Columns {
		if t.Columns[i].PK {
			return &t.Columns[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("%w: table %q has no primary key", ErrSQL, t.Name)
}

func (t *TableDef) columnIndex(name string) (int, error) {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("%w: unknown column %q", ErrSQL, name)
}

func saveTableDef(tx *Tx, def TableDef) error {
	payload, err := json.Marshal(def)
	if err != nil {
		return err
	}
	return tx.put(catalogTableKey(def.Name), payload)
}

func loadTableDef(tx *Tx, name string) (*TableDef, error) {
	raw, ok := tx.Get(catalogTableKey(name))
	if !ok {
		return nil, fmt.Errorf("%w: no such table: %s", ErrSQL, name)
	}
	var def TableDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

func dropTableDef(tx *Tx, name string) error {
	return tx.del(catalogTableKey(name))
}

func listTableDefs(tx *Tx) ([]TableDef, error) {
	pairs := tx.scanPrefix(catalogTablePrefix())
	out := make([]TableDef, 0, len(pairs))
	for _, p := range pairs {
		var def TableDef
		if err := json.Unmarshal(p.Value, &def); err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}
