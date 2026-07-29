package runneldb

import (
	"sync"
	"sync/atomic"
	"time"
)

// checkpointWorker runs background compaction when thresholds are met.
type checkpointWorker struct {
	db          *DB
	opts        Options
	stop        chan struct{}
	wg          sync.WaitGroup
	running     atomic.Bool
	checkpoints atomic.Uint64
}

func newCheckpointWorker(db *DB, opts Options) *checkpointWorker {
	return &checkpointWorker{db: db, opts: opts, stop: make(chan struct{})}
}

func (w *checkpointWorker) start() {
	w.wg.Add(1)
	go w.loop()
}

func (w *checkpointWorker) close() {
	close(w.stop)
	w.wg.Wait()
}

func (w *checkpointWorker) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastCommits := w.db.commits.Load()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.maybeCheckpoint(&lastCommits)
		}
	}
}

func (w *checkpointWorker) maybeCheckpoint(lastCommits *uint64) {
	if w.running.Load() {
		return
	}
	commits := w.db.commits.Load()
	delta := commits - *lastCommits
	if delta < w.opts.CheckpointEveryCommit {
		w.db.mu.RLock()
		f := w.db.file
		closed := w.db.closed
		w.db.mu.RUnlock()
		if closed || f == nil {
			return
		}
		info, err := f.Stat()
		if err != nil || info.Size() < w.opts.CheckpointMinBytes {
			return
		}
	}
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	if err := w.db.Compact(); err != nil {
		return
	}
	w.checkpoints.Add(1)
	*lastCommits = w.db.commits.Load()
}
