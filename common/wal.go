package common

import "errors"

var (
	ErrOutOfOrder = errors.New("wal: index out of order")
	ErrOutOfRange = errors.New("wal: index out of range")
	ErrNotFound   = errors.New("wal: entry not found")
	ErrClosed     = errors.New("wal: log is closed")
	ErrCorrupt    = errors.New("wal: log is corrupt")
)

type Options struct {
	NoSync           bool
	SegmentSize      int
	SegmentCacheSize int
}

type Entry struct {
	Index uint64
	Data  []byte
}

type WAL interface {
	Write(index uint64, data []byte) error

	WriteBatch(entries []Entry) error

	Read(index uint64) ([]byte, error)

	FirstIndex() (uint64, error)

	LastIndex() (uint64, error)

	TruncateFront(index uint64) error

	TruncateBack(index uint64) error

	Sync() error

	Close() error
}
