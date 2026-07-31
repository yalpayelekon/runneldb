package runneldb

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPagePackUnpack(t *testing.T) {
	vals := []Value{
		{Typ: TypeInteger, Int: 42},
		{Typ: TypeText, Text: "hello"},
		{Typ: TypeBlob, Blob: []byte{1, 2, 3}},
	}
	raw, err := packRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != pageSize {
		t.Fatalf("page size %d", len(raw))
	}
	got, err := unpackRow(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Int != 42 || got[1].Text != "hello" || string(got[2].Blob) != "\x01\x02\x03" {
		t.Fatalf("got %#v", got)
	}
}

func TestPageTooLarge(t *testing.T) {
	big := make([]byte, pageSize)
	_, err := packRow([]Value{{Typ: TypeBlob, Blob: big}})
	if !errors.Is(err, ErrRowTooLarge) {
		t.Fatalf("expected ErrRowTooLarge, got %v", err)
	}
}

func TestReservedKeyRejected(t *testing.T) {
	db := openTestDB(t)
	err := db.Update(func(tx *Tx) error {
		return tx.Set("__rdb__/nope", []byte("x"))
	})
	if !errors.Is(err, ErrReservedKey) {
		t.Fatalf("got %v", err)
	}
}

func TestSQLCRUDAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sql.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, name) VALUES (?, ?)`, int64(1), "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users VALUES (2, 'grace')`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM users WHERE id = ?`, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("expected row")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "ada" {
		t.Fatalf("got %q", name)
	}
	_ = rows.Close()

	res, err := db.Exec(`UPDATE users SET name = ? WHERE id = ?`, "Ada", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("affected %d", res.RowsAffected)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err = db.Query(`SELECT id, name FROM users WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing row after reopen")
	}
	var id int64
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if id != 1 || name != "Ada" {
		t.Fatalf("got %d %q", id, name)
	}

	res, err = db.Exec(`DELETE FROM users WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("delete affected %d", res.RowsAffected)
	}
	rows, err = db.Query(`SELECT * FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n != 1 {
		t.Fatalf("rows=%d", n)
	}
}

func TestSQLPKIndexLookup(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ {
		if _, err := db.Exec(`INSERT INTO t VALUES (?, ?)`, int64(i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	db.mu.RLock()
	idx := db.indexes["t"]
	rk, ok := idx.Get(pkFromInt(42))
	db.mu.RUnlock()
	if !ok || rk == "" {
		t.Fatal("index miss for pk 42")
	}
	rows, err := db.Query(`SELECT id FROM t WHERE id = 42`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected row")
	}
}

func TestSQLDropTable(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE t`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(`SELECT * FROM t`); err == nil {
		t.Fatal("expected error for missing table")
	}
}

func TestSQLParseRejects(t *testing.T) {
	cases := []string{
		`SELECT * FROM t ORDER BY id`,
		`SELECT * FROM a JOIN b`,
		`CREATE TABLE t (id INTEGER)`,
		`CREATE TABLE t (id INTEGER PRIMARY KEY, name INTEGER PRIMARY KEY)`,
	}
	for _, c := range cases {
		if _, err := parseSQL(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestSecondaryIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secidx.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 5; i++ {
		if _, err := db.Exec(`INSERT INTO users VALUES (?, ?)`, i, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users VALUES (6, 'bob')`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`CREATE INDEX idx_name ON users(name)`); err != nil {
		t.Fatal(err)
	}

	// Query using secondary index.
	rows, err := db.Query(`SELECT id FROM users WHERE name = ?`, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n != 5 {
		t.Fatalf("expected 5 rows, got %d", n)
	}

	// Update a row and verify index maintenance.
	if _, err := db.Exec(`UPDATE users SET name = 'carol' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT id FROM users WHERE name = ?`, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n = 0
	for rows.Next() {
		n++
	}
	if n != 4 {
		t.Fatalf("after update expected 4, got %d", n)
	}

	// Delete a row.
	if _, err := db.Exec(`DELETE FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT id FROM users WHERE name = ?`, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n = 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("after delete expected 3, got %d", n)
	}

	// Survive reopen.
	_ = db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err = db.Query(`SELECT id FROM users WHERE name = ?`, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n = 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("after reopen expected 3, got %d", n)
	}

	// Drop index.
	if _, err := db.Exec(`DROP INDEX idx_name`); err != nil {
		t.Fatal(err)
	}
	// Should still work (table scan fallback).
	rows, err = db.Query(`SELECT id FROM users WHERE name = ?`, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n = 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("after drop index expected 3, got %d", n)
	}
}

func TestSecondaryIndexPKColumnRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`CREATE INDEX bad ON t(id)`)
	if err == nil {
		t.Fatal("expected error indexing PK column")
	}
}

func TestSecondaryIndexSurvivesCompact(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO t VALUES (?, 'x')`, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX idx_v ON t(v)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id FROM t WHERE v = 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("after compact expected 3, got %d", n)
	}
}

func TestDropTableCleansIndexes(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1, 'a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_v ON t(v)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE t`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`DROP INDEX idx_v`)
	if err == nil {
		t.Fatal("expected error dropping index for dropped table")
	}
}

func TestJSONColumnCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "json.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE docs (id INTEGER PRIMARY KEY, doc JSON)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO docs VALUES (1, ?)`, `{"name":"ada","age":36}`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT json_extract(doc, '$.name') FROM docs WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("expected row")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if name != "ada" {
		t.Fatalf("got %q", name)
	}

	rows, err = db.Query(`SELECT json_extract(doc, '$.name') FROM docs WHERE json_extract(doc, '$.name') = 'ada'`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("expected extract where row")
	}
	_ = rows.Close()

	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err = db.Query(`SELECT doc FROM docs WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing after reopen")
	}
	var doc string
	if err := rows.Scan(&doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "ada") {
		t.Fatalf("doc=%q", doc)
	}
}

func TestJSONPathIndex(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE docs (id INTEGER PRIMARY KEY, doc JSON)`); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ada", "grace", "ada", "bob"} {
		if _, err := db.Exec(`INSERT INTO docs VALUES (?, ?)`, int64(i+1), `{"name":"`+name+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX idx_name ON docs(doc) PATH '$.name'`); err != nil {
		t.Fatal(err)
	}

	db.mu.RLock()
	si := db.secIndexes["idx_name"]
	rks := si.lookup(Value{Typ: TypeText, Text: "ada"})
	db.mu.RUnlock()
	if len(rks) != 2 {
		t.Fatalf("index lookup got %d keys", len(rks))
	}

	rows, err := db.Query(`SELECT id FROM docs WHERE json_extract(doc, '$.name') = 'ada'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n != 2 {
		t.Fatalf("rows=%d", n)
	}

	if _, err := db.Exec(`UPDATE docs SET doc = '{"name":"carol"}' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT id FROM docs WHERE json_extract(doc, '$.name') = 'ada'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n = 0
	for rows.Next() {
		n++
	}
	if n != 1 {
		t.Fatalf("after update rows=%d", n)
	}
}

func TestJSONPathIndexConcurrentRead(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE docs (id INTEGER PRIMARY KEY, doc JSON)`); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 20; i++ {
		if _, err := db.Exec(`INSERT INTO docs VALUES (?, '{"name":"x"}')`, i); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		rows, err := db.Query(`SELECT id FROM docs WHERE id = 1`)
		if err != nil {
			done <- err
			return
		}
		_ = rows.Close()
		done <- nil
	}()
	if _, err := db.Exec(`CREATE INDEX idx_n ON docs(doc) PATH '$.name'`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestJSONPathIndexRejects(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT, doc JSON)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX bad ON t(v) PATH '$.name'`); err == nil {
		t.Fatal("expected PATH on TEXT to fail")
	}
	if _, err := parseSQL(`CREATE INDEX bad ON t(doc) PATH '$.a[0]'`); err == nil {
		t.Fatal("expected bad path to fail")
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1, 'x', '{bad')`); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}
}

func TestCreateIndexParseRejects(t *testing.T) {
	cases := []string{
		`CREATE INDEX idx ON t(a, b)`,
	}
	for _, c := range cases {
		if _, err := parseSQL(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestBTreeSeekScan(t *testing.T) {
	tree := newBTree()
	for _, v := range []int64{5, 1, 9, 3, 7} {
		tree.Put(pkFromInt(v), encodePKInt(v))
	}
	it := tree.Seek(pkFromInt(3))
	var got []int64
	for {
		k, _, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, k.i)
	}
	if len(got) < 3 || got[0] != 3 {
		t.Fatalf("seek got %v", got)
	}
	tree.Delete(pkFromInt(3))
	if _, ok := tree.Get(pkFromInt(3)); ok {
		t.Fatal("delete failed")
	}
}
