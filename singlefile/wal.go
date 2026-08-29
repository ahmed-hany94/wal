package singlefile

import (
	"encoding/binary"
	"main/common"
	"os"
	"sync"
)

type entryPos struct {
	offset int64
	size   int64
}

const headerSize = 16 // 8 bytes index + 8 bytes data length

type Log struct {
	mu      sync.RWMutex
	path    string
	file    *os.File
	opts    common.Options
	entries []entryPos
	first   uint64
	last    uint64
	closed  bool
}

func Open(path string, opts *common.Options) (*Log, error) {
	if opts == nil {
		opts = &common.Options{}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	// init log
	l := &Log{path: path, file: f, opts: *opts}
	if err := l.load(); err != nil {
		f.Close()
		return nil, err
	}

	return l, nil
}

func (l *Log) load() error {
	stat, err := l.file.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()
	var offset int64
	header := make([]byte, headerSize)
	for offset < size {
		if _, err := l.file.ReadAt(header, offset); err != nil {
			return common.ErrCorrupt
		}

		index := binary.BigEndian.Uint64(header[0:8])
		dataSize := int64(binary.BigEndian.Uint64(header[8:16]))
		entryOffset := offset
		entrySize := headerSize + dataSize
		offset += entrySize

		if offset > size {
			return common.ErrCorrupt
		}

		if len(l.entries) == 0 {
			l.first = index
		}

		l.entries = append(l.entries, entryPos{
			offset: entryOffset,
			size:   entrySize,
		})
	}

	if len(l.entries) > 0 {
		l.last = l.first + uint64(len(l.entries)) - 1
	}

	return nil
}

func (l *Log) Write(index uint64, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeBatch([]common.Entry{{Index: index, Data: data}})
}

func (l *Log) WriteBatch(entries []common.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	if l.closed {
		return common.ErrClosed
	}
	return l.writeBatch(entries)
}

func (l *Log) Read(index uint64) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.closed {
		return nil, common.ErrClosed
	}

	if len(l.entries) == 0 || index < l.first || index > l.last {
		return nil, common.ErrNotFound
	}

	pos := l.entries[index-l.first]
	data := make([]byte, pos.size-headerSize)
	if _, err := l.file.ReadAt(data, pos.offset+headerSize); err != nil {
		return nil, err
	}

	return data, nil
}

func (l *Log) FirstIndex() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return 0, common.ErrClosed
	}
	return l.first, nil
}

func (l *Log) LastIndex() (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return 0, common.ErrClosed
	}
	return l.last, nil
}

func (l *Log) TruncateFront(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	if len(l.entries) == 0 || index < l.first || index > l.last {
		return common.ErrOutOfRange
	}
	i := int(index - l.first)
	if i == 0 {
		return nil
	}
	return l.rewrite(l.entries[i:], index)
}

func (l *Log) TruncateBack(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	if len(l.entries) == 0 || index < l.first || index > l.last {
		return common.ErrOutOfRange
	}
	i := int(index - l.first)
	if i == len(l.entries)-1 {
		return nil
	}
	return l.rewrite(l.entries[:i+1], l.first)
}

func (l *Log) Sync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	return l.file.Sync()
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	l.closed = true
	return l.file.Close()
}

func (l *Log) writeBatch(entries []common.Entry) error {
	next := l.last + 1
	for _, e := range entries {
		if e.Index != next {
			return common.ErrOutOfOrder
		}
		next++
	}

	info, err := l.file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()

	for _, e := range entries {
		buf := appendEntry(e.Index, e.Data)
		if _, err := l.file.WriteAt(buf, offset); err != nil {
			return err
		}
		l.entries = append(l.entries, entryPos{
			offset: offset,
			size:   int64(len(buf)),
		})
		offset += int64(len(buf))
	}

	if l.first == 0 {
		l.first = entries[0].Index
	}
	l.last = entries[len(entries)-1].Index

	if !l.opts.NoSync {
		if err := l.file.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func appendEntry(index uint64, data []byte) []byte {
	buf := make([]byte, headerSize+len(data))
	binary.BigEndian.PutUint64(buf[0:8], index)
	binary.BigEndian.PutUint64(buf[8:16], uint64(len(data)))
	copy(buf[headerSize:], data)
	return buf
}

func (l *Log) rewrite(keep []entryPos, newFirst uint64) error {
	tmpPath := l.path + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	var offset int64
	newEntries := make([]entryPos, 0, len(keep))
	for _, pos := range keep {
		buf := make([]byte, pos.size)
		if _, err := l.file.ReadAt(buf, pos.offset); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		if _, err := tmp.WriteAt(buf, offset); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		newEntries = append(newEntries, entryPos{
			offset: offset,
			size:   pos.size,
		})
		offset += pos.size
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, l.path); err != nil {
		return err
	}

	f, err := os.OpenFile(l.path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	l.file = f
	l.entries = newEntries
	if len(newEntries) > 0 {
		l.first = newFirst
		l.last = newFirst + uint64(len(newEntries)) - 1
	} else {
		l.first = 0
		l.last = 0
	}
	return nil
}
