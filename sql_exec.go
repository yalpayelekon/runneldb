package runneldb

import (
	"fmt"
)

// Result is the outcome of an Exec statement.
type Result struct {
	RowsAffected int64
}

// Rows is the result of a Query.
type Rows struct {
	columns []string
	data    [][]Value
	i       int
	closed  bool
}

// Columns returns result column names.
func (r *Rows) Columns() []string {
	return append([]string(nil), r.columns...)
}

// Next advances to the next result row.
func (r *Rows) Next() bool {
	if r.closed {
		return false
	}
	if r.i+1 >= len(r.data) {
		r.i = len(r.data)
		return false
	}
	r.i++
	return true
}

// Scan copies the current row into dest. dest length must match column count.
// Supported dest element types: *int64, *string, *[]byte, *Value, *any.
func (r *Rows) Scan(dest ...any) error {
	if r.closed || r.i < 0 || r.i >= len(r.data) {
		return fmt.Errorf("%w: Scan called without Next", ErrSQL)
	}
	row := r.data[r.i]
	if len(dest) != len(row) {
		return fmt.Errorf("%w: Scan expected %d dests, got %d", ErrSQL, len(row), len(dest))
	}
	for i, d := range dest {
		if err := assignScan(d, row[i]); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the rows.
func (r *Rows) Close() error {
	r.closed = true
	return nil
}

func assignScan(dest any, v Value) error {
	switch d := dest.(type) {
	case *Value:
		*d = v
	case *any:
		if v.Null {
			*d = nil
		} else {
			switch v.Typ {
			case TypeInteger:
				*d = v.Int
			case TypeText:
				*d = v.Text
			case TypeBlob:
				*d = v.Blob
			default:
				*d = nil
			}
		}
	case *int64:
		if v.Null {
			*d = 0
		} else if v.Typ == TypeInteger {
			*d = v.Int
		} else {
			return fmt.Errorf("%w: cannot scan into *int64", ErrSQL)
		}
	case *string:
		if v.Null {
			*d = ""
		} else if v.Typ == TypeText {
			*d = v.Text
		} else if v.Typ == TypeInteger {
			*d = fmt.Sprintf("%d", v.Int)
		} else {
			return fmt.Errorf("%w: cannot scan into *string", ErrSQL)
		}
	case *[]byte:
		if v.Null {
			*d = nil
		} else if v.Typ == TypeBlob {
			*d = append([]byte(nil), v.Blob...)
		} else if v.Typ == TypeText {
			*d = []byte(v.Text)
		} else {
			return fmt.Errorf("%w: cannot scan into *[]byte", ErrSQL)
		}
	default:
		return fmt.Errorf("%w: unsupported Scan destination %T", ErrSQL, dest)
	}
	return nil
}

// Exec executes a non-query SQL statement.
func (db *DB) Exec(sql string, args ...any) (Result, error) {
	st, err := parseSQL(sql)
	if err != nil {
		return Result{}, err
	}
	bound, err := bindArgs(st, args)
	if err != nil {
		return Result{}, err
	}
	switch s := bound.(type) {
	case createTableStmt:
		return db.execCreate(s)
	case dropTableStmt:
		return db.execDrop(s)
	case insertStmt:
		return db.execInsert(s)
	case updateStmt:
		return db.execUpdate(s)
	case deleteStmt:
		return db.execDelete(s)
	case createIndexStmt:
		return db.execCreateIndex(s)
	case dropIndexStmt:
		return db.execDropIndex(s)
	case selectStmt:
		return Result{}, fmt.Errorf("%w: use Query for SELECT", ErrSQL)
	default:
		return Result{}, fmt.Errorf("%w: unsupported statement", ErrSQL)
	}
}

// Query executes a SELECT statement.
func (db *DB) Query(sql string, args ...any) (*Rows, error) {
	st, err := parseSQL(sql)
	if err != nil {
		return nil, err
	}
	bound, err := bindArgs(st, args)
	if err != nil {
		return nil, err
	}
	s, ok := bound.(selectStmt)
	if !ok {
		return nil, fmt.Errorf("%w: Query requires SELECT", ErrSQL)
	}
	return db.execSelect(s)
}

func bindArgs(st stmt, args []any) (stmt, error) {
	if max := maxParam(st); max >= len(args) {
		return nil, fmt.Errorf("%w: not enough arguments", ErrSQL)
	}
	switch s := st.(type) {
	case insertStmt:
		for i := range s.values {
			if s.values[i].kind == exprParam {
				if err := fillParam(&s.values[i], args); err != nil {
					return nil, err
				}
			}
		}
		return s, nil
	case selectStmt:
		for i := range s.where {
			if s.where[i].value.kind == exprParam {
				if err := fillParam(&s.where[i].value, args); err != nil {
					return nil, err
				}
			}
		}
		return s, nil
	case updateStmt:
		for i := range s.sets {
			if s.sets[i].value.kind == exprParam {
				if err := fillParam(&s.sets[i].value, args); err != nil {
					return nil, err
				}
			}
		}
		for i := range s.where {
			if s.where[i].value.kind == exprParam {
				if err := fillParam(&s.where[i].value, args); err != nil {
					return nil, err
				}
			}
		}
		return s, nil
	case deleteStmt:
		for i := range s.where {
			if s.where[i].value.kind == exprParam {
				if err := fillParam(&s.where[i].value, args); err != nil {
					return nil, err
				}
			}
		}
		return s, nil
	default:
		return st, nil
	}
}

func maxParam(st stmt) int {
	max := -1
	check := func(e expr) {
		if e.kind == exprParam && e.param > max {
			max = e.param
		}
	}
	switch s := st.(type) {
	case insertStmt:
		for _, e := range s.values {
			check(e)
		}
	case selectStmt:
		for _, p := range s.where {
			check(p.value)
		}
	case updateStmt:
		for _, a := range s.sets {
			check(a.value)
		}
		for _, p := range s.where {
			check(p.value)
		}
	case deleteStmt:
		for _, p := range s.where {
			check(p.value)
		}
	}
	return max
}

func fillParam(e *expr, args []any) error {
	if e.param < 0 || e.param >= len(args) {
		return fmt.Errorf("%w: missing argument for ?", ErrSQL)
	}
	v, err := anyToExpr(args[e.param])
	if err != nil {
		return err
	}
	*e = v
	return nil
}

func anyToExpr(a any) (expr, error) {
	if a == nil {
		return expr{kind: exprNull}, nil
	}
	switch v := a.(type) {
	case int:
		return expr{kind: exprInt, i: int64(v)}, nil
	case int64:
		return expr{kind: exprInt, i: v}, nil
	case float64: // JSON numbers
		return expr{kind: exprInt, i: int64(v)}, nil
	case string:
		return expr{kind: exprText, s: v}, nil
	case []byte:
		return expr{kind: exprBlob, blob: append([]byte(nil), v...)}, nil
	default:
		return expr{}, fmt.Errorf("%w: unsupported arg type %T", ErrSQL, a)
	}
}

func exprToValue(e expr, typ ColType) (Value, error) {
	if e.kind == exprNull {
		return Value{Typ: typ, Null: true}, nil
	}
	switch typ {
	case TypeInteger:
		if e.kind != exprInt {
			return Value{}, fmt.Errorf("%w: expected INTEGER", ErrSQL)
		}
		return Value{Typ: TypeInteger, Int: e.i}, nil
	case TypeText:
		if e.kind != exprText {
			return Value{}, fmt.Errorf("%w: expected TEXT", ErrSQL)
		}
		return Value{Typ: TypeText, Text: e.s}, nil
	case TypeBlob:
		if e.kind != exprBlob {
			return Value{}, fmt.Errorf("%w: expected BLOB", ErrSQL)
		}
		return Value{Typ: TypeBlob, Blob: e.blob}, nil
	default:
		return Value{}, fmt.Errorf("%w: unknown column type", ErrSQL)
	}
}

func (db *DB) execCreate(s createTableStmt) (Result, error) {
	name := normalizeName(s.name)
	err := db.Update(func(tx *Tx) error {
		if _, ok := tx.Get(catalogTableKey(name)); ok {
			return fmt.Errorf("%w: table already exists: %s", ErrSQL, name)
		}
		def := TableDef{Name: name, Columns: s.columns}
		for i := range def.Columns {
			def.Columns[i].Name = normalizeName(def.Columns[i].Name)
		}
		return saveTableDef(tx, def)
	})
	if err != nil {
		return Result{}, err
	}
	db.mu.Lock()
	db.tables[name] = &TableDef{Name: name, Columns: append([]ColumnDef(nil), s.columns...)}
	for i := range db.tables[name].Columns {
		db.tables[name].Columns[i].Name = normalizeName(db.tables[name].Columns[i].Name)
	}
	db.indexes[name] = newBTree()
	db.mu.Unlock()
	return Result{}, nil
}

func (db *DB) execDrop(s dropTableStmt) (Result, error) {
	name := normalizeName(s.name)
	var n int64
	err := db.Update(func(tx *Tx) error {
		if _, err := loadTableDef(tx, name); err != nil {
			return err
		}
		pairs := tx.scanPrefix(rowKeyPrefix(name))
		for _, p := range pairs {
			if err := tx.del(p.Key); err != nil {
				return err
			}
			n++
		}
		idxDefs, err := listIndexDefs(tx)
		if err != nil {
			return err
		}
		for _, id := range idxDefs {
			if normalizeName(id.Table) == name {
				if err := dropIndexDef(tx, id.Name); err != nil {
					return err
				}
			}
		}
		return dropTableDef(tx, name)
	})
	if err != nil {
		return Result{}, err
	}
	db.mu.Lock()
	delete(db.tables, name)
	delete(db.indexes, name)
	for k, si := range db.secIndexes {
		if si.def.Table == name {
			delete(db.secIndexes, k)
		}
	}
	db.mu.Unlock()
	return Result{RowsAffected: n}, nil
}

func (db *DB) execInsert(s insertStmt) (Result, error) {
	name := normalizeName(s.table)
	db.mu.RLock()
	def := db.tables[name]
	db.mu.RUnlock()
	if def == nil {
		return Result{}, fmt.Errorf("%w: no such table: %s", ErrSQL, name)
	}
	pkCol, pkIdx, err := def.pkColumn()
	if err != nil {
		return Result{}, err
	}

	colIdxs := make([]int, 0, len(def.Columns))
	if len(s.columns) == 0 {
		if len(s.values) != len(def.Columns) {
			return Result{}, fmt.Errorf("%w: column count mismatch", ErrSQL)
		}
		for i := range def.Columns {
			colIdxs = append(colIdxs, i)
		}
	} else {
		if len(s.columns) != len(s.values) {
			return Result{}, fmt.Errorf("%w: column count mismatch", ErrSQL)
		}
		for _, c := range s.columns {
			i, err := def.columnIndex(normalizeName(c))
			if err != nil {
				return Result{}, err
			}
			colIdxs = append(colIdxs, i)
		}
	}

	values := make([]Value, len(def.Columns))
	for i := range values {
		values[i] = Value{Typ: def.Columns[i].Type, Null: true}
	}
	for j, ci := range colIdxs {
		v, err := exprToValue(s.values[j], def.Columns[ci].Type)
		if err != nil {
			return Result{}, err
		}
		values[ci] = v
	}
	if values[pkIdx].Null {
		return Result{}, fmt.Errorf("%w: PRIMARY KEY must not be NULL", ErrSQL)
	}

	var pk pkKey
	var pkEnc string
	switch pkCol.Type {
	case TypeInteger:
		pk = pkFromInt(values[pkIdx].Int)
		pkEnc = encodePKInt(values[pkIdx].Int)
	case TypeText:
		pk = pkFromString(values[pkIdx].Text)
		pkEnc = encodePKString(values[pkIdx].Text)
	default:
		return Result{}, fmt.Errorf("%w: PRIMARY KEY must be INTEGER or TEXT", ErrSQL)
	}
	rk := rowKey(name, pkEnc)
	page, err := packRow(values)
	if err != nil {
		return Result{}, err
	}

	err = db.Update(func(tx *Tx) error {
		if _, ok := tx.Get(rk); ok {
			return fmt.Errorf("%w: duplicate primary key", ErrSQL)
		}
		return tx.put(rk, page)
	})
	if err != nil {
		return Result{}, err
	}
	db.mu.Lock()
	if db.indexes[name] == nil {
		db.indexes[name] = newBTree()
	}
	db.indexes[name].Put(pk, rk)
	for _, si := range db.secIndexes {
		if si.def.Table == name {
			ci, err := def.columnIndex(si.def.Column)
			if err == nil {
				si.insert(values[ci], pk, rk)
			}
		}
	}
	db.mu.Unlock()
	return Result{RowsAffected: 1}, nil
}

func (db *DB) tableDef(name string) (*TableDef, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	def := db.tables[name]
	if def == nil {
		return nil, fmt.Errorf("%w: no such table: %s", ErrSQL, name)
	}
	return def, nil
}

func (db *DB) execUpdate(s updateStmt) (Result, error) {
	name := normalizeName(s.table)
	def, err := db.tableDef(name)
	if err != nil {
		return Result{}, err
	}
	pkCol, pkIdx, err := def.pkColumn()
	if err != nil {
		return Result{}, err
	}
	for _, a := range s.sets {
		if normalizeName(a.column) == pkCol.Name {
			return Result{}, fmt.Errorf("%w: cannot UPDATE primary key", ErrSQL)
		}
	}

	type updateItem struct {
		pk     pkKey
		oldRow []Value
		newRow []Value
	}
	var affected int64
	var updated []updateItem
	err = db.Update(func(tx *Tx) error {
		rows, err := db.collectRows(tx, def, s.where)
		if err != nil {
			return err
		}
		for _, item := range rows {
			oldVals := append([]Value(nil), item.values...)
			vals := append([]Value(nil), item.values...)
			for _, a := range s.sets {
				ci, err := def.columnIndex(normalizeName(a.column))
				if err != nil {
					return err
				}
				v, err := exprToValue(a.value, def.Columns[ci].Type)
				if err != nil {
					return err
				}
				vals[ci] = v
			}
			page, err := packRow(vals)
			if err != nil {
				return err
			}
			if err := tx.put(item.rowKey, page); err != nil {
				return err
			}
			updated = append(updated, updateItem{pk: item.pk, oldRow: oldVals, newRow: vals})
			affected++
		}
		_ = pkIdx
		return nil
	})
	if err == nil {
		db.mu.Lock()
		for _, si := range db.secIndexes {
			if si.def.Table == name {
				ci, cerr := def.columnIndex(si.def.Column)
				if cerr != nil {
					continue
				}
				for _, u := range updated {
					si.remove(u.oldRow[ci], u.pk)
					si.insert(u.newRow[ci], u.pk, rowKey(name, u.pk.encode()))
				}
			}
		}
		db.mu.Unlock()
	}
	return Result{RowsAffected: affected}, err
}

func (db *DB) execDelete(s deleteStmt) (Result, error) {
	name := normalizeName(s.table)
	def, err := db.tableDef(name)
	if err != nil {
		return Result{}, err
	}
	_, pkIdx, err := def.pkColumn()
	if err != nil {
		return Result{}, err
	}
	var affected int64
	var removed []pkKey
	var removedVals [][]Value
	err = db.Update(func(tx *Tx) error {
		rows, err := db.collectRows(tx, def, s.where)
		if err != nil {
			return err
		}
		for _, item := range rows {
			if err := tx.del(item.rowKey); err != nil {
				return err
			}
			removed = append(removed, item.pk)
			removedVals = append(removedVals, item.values)
			affected++
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	db.mu.Lock()
	if idx := db.indexes[name]; idx != nil {
		for _, k := range removed {
			idx.Delete(k)
		}
	}
	for _, si := range db.secIndexes {
		if si.def.Table == name {
			ci, cerr := def.columnIndex(si.def.Column)
			if cerr != nil {
				continue
			}
			for i, k := range removed {
				si.remove(removedVals[i][ci], k)
			}
		}
	}
	db.mu.Unlock()
	_ = pkIdx
	return Result{RowsAffected: affected}, nil
}

type rowItem struct {
	pk     pkKey
	rowKey string
	values []Value
}

func (db *DB) collectRows(tx *Tx, def *TableDef, where []pred) ([]rowItem, error) {
	pkCol, pkIdx, err := def.pkColumn()
	if err != nil {
		return nil, err
	}
	// PK equality fast path via index.
	if len(where) == 1 && normalizeName(where[0].column) == pkCol.Name {
		v, err := exprToValue(where[0].value, pkCol.Type)
		if err != nil {
			return nil, err
		}
		var k pkKey
		switch pkCol.Type {
		case TypeInteger:
			k = pkFromInt(v.Int)
		case TypeText:
			k = pkFromString(v.Text)
		}
		db.mu.RLock()
		idx := db.indexes[def.Name]
		var rk string
		var ok bool
		if idx != nil {
			rk, ok = idx.Get(k)
		}
		db.mu.RUnlock()
		if !ok {
			return nil, nil
		}
		raw, found := tx.Get(rk)
		if !found {
			return nil, nil
		}
		vals, err := unpackRow(raw)
		if err != nil {
			return nil, err
		}
		return []rowItem{{pk: k, rowKey: rk, values: vals}}, nil
	}

	// Secondary index equality fast path.
	if len(where) == 1 {
		col := normalizeName(where[0].column)
		db.mu.RLock()
		var si *secondaryIdx
		for _, s := range db.secIndexes {
			if s.def.Table == def.Name && s.def.Column == col {
				si = s
				break
			}
		}
		db.mu.RUnlock()
		if si != nil {
			ci, err := def.columnIndex(col)
			if err != nil {
				return nil, err
			}
			val, err := exprToValue(where[0].value, def.Columns[ci].Type)
			if err != nil {
				return nil, err
			}
			db.mu.RLock()
			rks := si.lookup(val)
			db.mu.RUnlock()
			var out []rowItem
			for _, rk := range rks {
				raw, found := tx.Get(rk)
				if !found {
					continue
				}
				vals, err := unpackRow(raw)
				if err != nil {
					return nil, err
				}
				enc, ok := parseRowKey(def.Name, rk)
				if !ok {
					continue
				}
				k, err := pkKeyFromEncoded(enc)
				if err != nil {
					return nil, err
				}
				out = append(out, rowItem{pk: k, rowKey: rk, values: vals})
			}
			return out, nil
		}
	}

	// Table scan.
	pairs := tx.scanPrefix(rowKeyPrefix(def.Name))
	var out []rowItem
	for _, p := range pairs {
		vals, err := unpackRow(p.Value)
		if err != nil {
			return nil, err
		}
		if !matchWhere(def, vals, where) {
			continue
		}
		enc, ok := parseRowKey(def.Name, p.Key)
		if !ok {
			continue
		}
		k, err := pkKeyFromEncoded(enc)
		if err != nil {
			return nil, err
		}
		out = append(out, rowItem{pk: k, rowKey: p.Key, values: vals})
	}
	_ = pkIdx
	return out, nil
}

func matchWhere(def *TableDef, vals []Value, where []pred) bool {
	for _, w := range where {
		ci, err := def.columnIndex(normalizeName(w.column))
		if err != nil {
			return false
		}
		want, err := exprToValue(w.value, def.Columns[ci].Type)
		if err != nil {
			return false
		}
		if !valuesEqual(vals[ci], want) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b Value) bool {
	if a.Null || b.Null {
		return a.Null && b.Null
	}
	if a.Typ != b.Typ {
		return false
	}
	switch a.Typ {
	case TypeInteger:
		return a.Int == b.Int
	case TypeText:
		return a.Text == b.Text
	case TypeBlob:
		return string(a.Blob) == string(b.Blob)
	default:
		return false
	}
}

func (db *DB) execSelect(s selectStmt) (*Rows, error) {
	name := normalizeName(s.table)
	def, err := db.tableDef(name)
	if err != nil {
		return nil, err
	}
	var colIdxs []int
	var colNames []string
	if len(s.columns) == 0 || (len(s.columns) == 1 && s.columns[0] == "*") {
		for i, c := range def.Columns {
			colIdxs = append(colIdxs, i)
			colNames = append(colNames, c.Name)
		}
	} else {
		for _, c := range s.columns {
			i, err := def.columnIndex(normalizeName(c))
			if err != nil {
				return nil, err
			}
			colIdxs = append(colIdxs, i)
			colNames = append(colNames, def.Columns[i].Name)
		}
	}

	var data [][]Value
	err = db.View(func(tx *Tx) error {
		rows, err := db.collectRows(tx, def, s.where)
		if err != nil {
			return err
		}
		for _, item := range rows {
			out := make([]Value, len(colIdxs))
			for j, ci := range colIdxs {
				out[j] = item.values[ci]
			}
			data = append(data, out)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Rows{columns: colNames, data: data, i: -1}, nil
}

func (db *DB) execCreateIndex(s createIndexStmt) (Result, error) {
	idxName := normalizeName(s.index)
	tableName := normalizeName(s.table)
	colName := normalizeName(s.column)

	db.mu.RLock()
	def := db.tables[tableName]
	_, exists := db.secIndexes[idxName]
	db.mu.RUnlock()
	if def == nil {
		return Result{}, fmt.Errorf("%w: no such table: %s", ErrSQL, tableName)
	}
	if exists {
		return Result{}, fmt.Errorf("%w: index already exists: %s", ErrSQL, idxName)
	}
	pkCol, _, err := def.pkColumn()
	if err != nil {
		return Result{}, err
	}
	if colName == pkCol.Name {
		return Result{}, fmt.Errorf("%w: cannot index primary key column", ErrSQL)
	}
	ci, err := def.columnIndex(colName)
	if err != nil {
		return Result{}, err
	}
	ct := def.Columns[ci].Type
	if ct != TypeInteger && ct != TypeText {
		return Result{}, fmt.Errorf("%w: secondary indexes only support INTEGER or TEXT columns", ErrSQL)
	}

	idef := IndexDef{Name: idxName, Table: tableName, Column: colName}

	// Build the shadow index by scanning the table.
	si := newSecondaryIdx(idef)
	if err := db.View(func(tx *Tx) error {
		pairs := tx.scanPrefix(rowKeyPrefix(tableName))
		for _, p := range pairs {
			vals, err := unpackRow(p.Value)
			if err != nil {
				return err
			}
			enc, ok := parseRowKey(tableName, p.Key)
			if !ok {
				continue
			}
			pk, err := pkKeyFromEncoded(enc)
			if err != nil {
				return err
			}
			si.insert(vals[ci], pk, p.Key)
		}
		return nil
	}); err != nil {
		return Result{}, err
	}

	// Persist catalog entry.
	if err := db.Update(func(tx *Tx) error {
		return saveIndexDef(tx, idef)
	}); err != nil {
		return Result{}, err
	}

	db.mu.Lock()
	db.secIndexes[idxName] = si
	db.mu.Unlock()
	return Result{}, nil
}

func (db *DB) execDropIndex(s dropIndexStmt) (Result, error) {
	idxName := normalizeName(s.index)
	db.mu.RLock()
	_, exists := db.secIndexes[idxName]
	db.mu.RUnlock()
	if !exists {
		return Result{}, fmt.Errorf("%w: no such index: %s", ErrSQL, idxName)
	}
	if err := db.Update(func(tx *Tx) error {
		return dropIndexDef(tx, idxName)
	}); err != nil {
		return Result{}, err
	}
	db.mu.Lock()
	delete(db.secIndexes, idxName)
	db.mu.Unlock()
	return Result{}, nil
}

// rebuildSQLState loads catalog and B+tree indexes after WAL replay.
func (db *DB) rebuildSQLState() error {
	db.tables = make(map[string]*TableDef)
	db.indexes = make(map[string]*btree)
	db.secIndexes = make(map[string]*secondaryIdx)
	tx := &Tx{
		db: db, readVersion: db.version, writable: false,
		writes: make(map[string]operation),
	}
	defs, err := listTableDefs(tx)
	if err != nil {
		return err
	}
	// Cache rows per table for PK + secondary index rebuild.
	type tableRows struct {
		pairs []kvPair
	}
	rowCache := make(map[string]*tableRows)
	for i := range defs {
		def := defs[i]
		name := normalizeName(def.Name)
		def.Name = name
		for j := range def.Columns {
			def.Columns[j].Name = normalizeName(def.Columns[j].Name)
		}
		cp := def
		db.tables[name] = &cp
		tree := newBTree()
		pairs := tx.scanPrefix(rowKeyPrefix(name))
		rowCache[name] = &tableRows{pairs: pairs}
		for _, p := range pairs {
			enc, ok := parseRowKey(name, p.Key)
			if !ok {
				continue
			}
			k, err := pkKeyFromEncoded(enc)
			if err != nil {
				return err
			}
			tree.Put(k, p.Key)
		}
		db.indexes[name] = tree
	}

	// Rebuild secondary indexes.
	idxDefs, err := listIndexDefs(tx)
	if err != nil {
		return err
	}
	for _, idef := range idxDefs {
		idef.Name = normalizeName(idef.Name)
		idef.Table = normalizeName(idef.Table)
		idef.Column = normalizeName(idef.Column)
		tdef := db.tables[idef.Table]
		if tdef == nil {
			continue
		}
		ci, err := tdef.columnIndex(idef.Column)
		if err != nil {
			continue
		}
		si := newSecondaryIdx(idef)
		cached := rowCache[idef.Table]
		if cached != nil {
			for _, p := range cached.pairs {
				vals, err := unpackRow(p.Value)
				if err != nil {
					return err
				}
				enc, ok := parseRowKey(idef.Table, p.Key)
				if !ok {
					continue
				}
				pk, err := pkKeyFromEncoded(enc)
				if err != nil {
					return err
				}
				si.insert(vals[ci], pk, p.Key)
			}
		}
		db.secIndexes[idef.Name] = si
	}
	return nil
}
