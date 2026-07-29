package runneldb

import "errors"

var (
	ErrClosed      = errors.New("runneldb: database is closed")
	ErrConflict    = errors.New("runneldb: transaction conflict")
	ErrReadOnly    = errors.New("runneldb: transaction is read-only")
	ErrTxClosed    = errors.New("runneldb: transaction is closed")
	ErrWALVersion  = errors.New("runneldb: unsupported WAL version")
	ErrWALCorrupt  = errors.New("runneldb: WAL corruption before end of file")
	ErrWALMagic    = errors.New("runneldb: invalid WAL magic")
	ErrReservedKey = errors.New("runneldb: key uses reserved __rdb__/ prefix")
	ErrSQL         = errors.New("runneldb: SQL error")
	ErrRowTooLarge = errors.New("runneldb: row does not fit in a page")
	ErrNotFound    = errors.New("runneldb: not found")
)
