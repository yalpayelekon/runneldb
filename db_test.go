package runneldb

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSetGetDeleteAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error {
		if err := tx.Set("greeting", []byte("hello")); err != nil {
			return err
		}
		return tx.Set("temporary", []byte("gone soon"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Delete("temporary") }); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		if got, ok := tx.Get("greeting"); !ok || string(got) != "hello" {
			t.Fatalf("got %q, %v", got, ok)
		}
		if _, ok := tx.Get("temporary"); ok {
			t.Fatal("deleted key is visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStableSnapshotAndConflict(t *testing.T) {
	db := openTestDB(t)
	if err := db.Update(func(tx *Tx) error { return tx.Set("key", []byte("v1")) }); err != nil {
		t.Fatal(err)
	}
	first, _ := db.Begin(true)
	second, _ := db.Begin(true)
	_ = first.Set("key", []byte("first"))
	_ = second.Set("key", []byte("second"))
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, _ := second.Get("key"); string(got) != "second" {
		t.Fatalf("transaction did not see its own write: %q", got)
	}
	if err := second.Commit(); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestConcurrentDisjointWrites(t *testing.T) {
	db := openTestDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.Begin(true)
			if err != nil {
				errs <- err
				return
			}
			_ = tx.Set(string(rune('a'+i)), []byte{byte(i)})
			errs <- tx.Commit()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := db.Metrics().Keys; got != 32 {
		t.Fatalf("expected 32 keys, got %d", got)
	}
}
