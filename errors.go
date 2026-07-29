package runneldb

import "errors"

var (
	ErrClosed   = errors.New("runneldb: database is closed")
	ErrConflict = errors.New("runneldb: transaction conflict")
	ErrReadOnly = errors.New("runneldb: transaction is read-only")
	ErrTxClosed = errors.New("runneldb: transaction is closed")
)
