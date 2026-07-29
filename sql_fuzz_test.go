package runneldb

import "testing"

func FuzzSQLParse(f *testing.F) {
	seeds := []string{
		`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO t VALUES (1, 'a')`,
		`SELECT * FROM t WHERE id = 1`,
		`UPDATE t SET name = 'b' WHERE id = 1`,
		`DELETE FROM t WHERE id = ?`,
		`DROP TABLE t`,
		`SELECT`,
		`'`,
		`CREATE TABLE t (id INTEGER PRIMARY KEY`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, _ = parseSQL(src) // must not panic
	})
}
