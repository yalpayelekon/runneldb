package runneldb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Handler exposes a small HTTP API for a DB.
func Handler(db *DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, db.Metrics())
	})
	mux.HandleFunc("/v1/compact", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := db.Compact(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/kv/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var value []byte
			var found bool
			err := db.View(func(tx *Tx) error {
				value, found = tx.Get(key)
				return nil
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			} else if !found {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(value)
			}
		case http.MethodPut:
			value, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			err = db.Update(func(tx *Tx) error { return tx.Set(key, value) })
			if errors.Is(err, ErrConflict) {
				http.Error(w, err.Error(), http.StatusConflict)
			} else if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case http.MethodDelete:
			err := db.Update(func(tx *Tx) error { return tx.Delete(key) })
			if errors.Is(err, ErrConflict) {
				http.Error(w, err.Error(), http.StatusConflict)
			} else if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
