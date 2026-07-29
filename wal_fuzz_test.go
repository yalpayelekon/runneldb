package runneldb

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzWALReplay(f *testing.F) {
	// Seed: empty (create), minimal header, and a valid small DB WAL.
	f.Add([]byte{})
	var hdr [walHeaderSize]byte
	copy(hdr[:4], walMagic)
	// version 1 little-endian
	hdr[4] = 1
	f.Add(hdr[:])
	f.Add([]byte("not a wal file!!!!"))

	path := filepath.Join(f.TempDir(), "seed.wal")
	db, err := Open(path)
	if err != nil {
		f.Fatal(err)
	}
	_ = db.Update(func(tx *Tx) error { return tx.Set("x", []byte("y")) })
	_ = db.Close()
	seed, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	if len(seed) > 0 {
		torn := append([]byte(nil), seed...)
		torn = torn[:len(torn)/2]
		f.Add(torn)
		flipped := append([]byte(nil), seed...)
		flipped[len(flipped)-1] ^= 0xff
		f.Add(flipped)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		p := filepath.Join(dir, "fuzz.wal")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := Open(p)
		if err != nil {
			return
		}
		defer db.Close()
		_ = db.View(func(tx *Tx) error {
			_, _ = tx.Get("x")
			return nil
		})
		_ = db.Update(func(tx *Tx) error {
			return tx.Set("fuzz", []byte("ok"))
		})
	})
}
