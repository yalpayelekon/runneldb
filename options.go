package runneldb

// Options configures database behaviour.
type Options struct {
	AutoCheckpoint        bool
	CheckpointEveryCommit uint64
	CheckpointMinBytes    int64
}

// DefaultOptions returns production defaults with auto-checkpoint enabled.
func DefaultOptions() Options {
	return Options{
		AutoCheckpoint:        true,
		CheckpointEveryCommit: 256,
		CheckpointMinBytes:    1 << 20,
	}
}
