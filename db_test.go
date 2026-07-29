package runneldb

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestWALHeaderOnCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < walHeaderSize || string(data[:4]) != walMagic {
		t.Fatalf("missing WAL magic, got %q", data)
	}
	if v := binary.LittleEndian.Uint32(data[4:8]); v != walVersion {
		t.Fatalf("version %d", v)
	}
}

func TestRejectBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	if err := os.WriteFile(path, []byte("XXXX................"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrWALMagic) {
		t.Fatalf("expected ErrWALMagic, got %v", err)
	}
}

func TestTornTailRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("a", []byte("1")) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("b", []byte("2")) }); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Append a partial frame header (torn write).
	if err := os.WriteFile(path, append(data, 0x01, 0x00, 0x00), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		if got, ok := tx.Get("a"); !ok || string(got) != "1" {
			t.Fatalf("a=%q ok=%v", got, ok)
		}
		if got, ok := tx.Get("b"); !ok || string(got) != "2" {
			t.Fatalf("b=%q ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() != int64(len(data)) {
		t.Fatalf("expected truncate to %d, got %d", len(data), info.Size())
	}
}

func TestCorruptLastRecordCRC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("keep", []byte("yes")) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("lost", []byte("no")) }); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte in the last record payload (after its 8-byte frame header).
	// Find last record start by scanning; easier: corrupt the final payload byte.
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		if got, ok := tx.Get("keep"); !ok || string(got) != "yes" {
			t.Fatalf("keep=%q ok=%v", got, ok)
		}
		if _, ok := tx.Get("lost"); ok {
			t.Fatal("corrupt last commit should be discarded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMidFileCorruptionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("a", []byte("1")) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("b", []byte("2")) }); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the first record's CRC while leaving the second record intact.
	firstFrame := walHeaderSize
	data[firstFrame+4] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	if !errors.Is(err, ErrWALCorrupt) {
		t.Fatalf("expected ErrWALCorrupt, got %v", err)
	}
}

func TestCompactShrinksAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		val := []byte{byte(i)}
		if err := db.Update(func(tx *Tx) error { return tx.Set("k", val) }); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("expected compact to shrink WAL: before=%d after=%d", before.Size(), after.Size())
	}
	if db.Metrics().Compactions != 1 {
		t.Fatalf("compactions=%d", db.Metrics().Compactions)
	}
	if err := db.View(func(tx *Tx) error {
		got, ok := tx.Get("k")
		if !ok || got[0] != 19 {
			t.Fatalf("got %v ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		got, ok := tx.Get("k")
		if !ok || got[0] != 19 {
			t.Fatalf("reopen got %v ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompactRetainsOpenSnapshot(t *testing.T) {
	db := openTestDB(t)
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v1")) }); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v2")) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	if got, ok := snap.Get("k"); !ok || string(got) != "v1" {
		t.Fatalf("snapshot saw %q ok=%v", got, ok)
	}
	snap.Rollback()
	if err := db.View(func(tx *Tx) error {
		got, ok := tx.Get("k")
		if !ok || string(got) != "v2" {
			t.Fatalf("current got %q ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClosedDB(t *testing.T) {
	db := openTestDB(t)
	_ = db.Close()
	if _, err := db.Begin(false); !errors.Is(err, ErrClosed) {
		t.Fatalf("Begin: %v", err)
	}
	if err := db.Compact(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Compact: %v", err)
	}
}

func TestCompactAfterDeleteAndRewrite(t *testing.T) {
	db := openTestDB(t)
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v1")) }); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Delete("k") }); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v3")) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	if got, ok := snap.Get("k"); !ok || string(got) != "v1" {
		t.Fatalf("snap=%q ok=%v", got, ok)
	}
	snap.Rollback()
	if err := db.View(func(tx *Tx) error {
		got, ok := tx.Get("k")
		if !ok || string(got) != "v3" {
			t.Fatalf("current=%q ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTornPayloadRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Set("ok", []byte("1")) }); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Append a frame header claiming a large payload, with no body.
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[:4], 100)
	binary.LittleEndian.PutUint32(hdr[4:], 0)
	if err := os.WriteFile(path, append(data, hdr[:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		got, ok := tx.Get("ok")
		if !ok || string(got) != "1" {
			t.Fatalf("got %q ok=%v", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedWALVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	var hdr [walHeaderSize]byte
	copy(hdr[:4], walMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], 99)
	if err := os.WriteFile(path, hdr[:], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrWALVersion) {
		t.Fatalf("expected ErrWALVersion, got %v", err)
	}
}

func TestShortWALFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	if err := os.WriteFile(path, []byte("RND"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrWALMagic) {
		t.Fatalf("expected ErrWALMagic, got %v", err)
	}
}

func TestCompactEmptyAndDelete(t *testing.T) {
	db := openTestDB(t)
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *Tx) error {
		if err := tx.Set("gone", []byte("x")); err != nil {
			return err
		}
		return tx.Delete("gone")
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	if db.Metrics().Keys != 0 {
		t.Fatalf("keys=%d", db.Metrics().Keys)
	}
}

func TestBackgroundCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ckpt.wal")
	db, err := OpenWithOptions(path, Options{
		AutoCheckpoint:        true,
		CheckpointEveryCommit: 3,
		CheckpointMinBytes:    0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 10; i++ {
		if err := db.Update(func(tx *Tx) error {
			return tx.Set("k", []byte{byte(i)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Give the worker time to fire.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if db.Metrics().Checkpoints > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if db.Metrics().Checkpoints == 0 {
		t.Fatal("expected at least one background checkpoint")
	}
}

func TestWALBytesMetric(t *testing.T) {
	db := openTestDB(t)
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v")) }); err != nil {
		t.Fatal(err)
	}
	m := db.Metrics()
	if m.WALBytes <= 0 {
		t.Fatalf("WALBytes=%d", m.WALBytes)
	}
}

func TestOpenWithDefaultOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opts.wal")
	db, err := OpenWithOptions(path, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(tx *Tx) error { return tx.Set("k", []byte("v")) }); err != nil {
		t.Fatal(err)
	}
}

func TestTxErrors(t *testing.T) {
	db := openTestDB(t)
	tx, err := db.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Set("k", []byte("v")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Set: %v", err)
	}
	if err := tx.Delete("k"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Delete: %v", err)
	}
	tx.Rollback()
	if err := tx.Set("k", []byte("v")); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Set closed: %v", err)
	}
	if err := tx.Commit(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Commit closed: %v", err)
	}
}
