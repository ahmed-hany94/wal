package segmented

import (
	"encoding/binary"
	"fmt"
	"io"
	"main/common"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

type entryPos struct {
	offset int64
	size   int64
}

const headerSize = 16 // 8 bytes index + 8 bytes data length
const defaultSegmentSize = 20 * 1024 * 1024

type segment struct {
	path   string
	index  uint64
	epos   []entryPos
	size   int64
	loaded bool
}

type Log struct {
	mu       sync.RWMutex
	path     string
	opts     common.Options
	segments []*segment
	sfile    *os.File
	first    uint64
	last     uint64
	closed   bool
}

func Open(path string, opts *common.Options) (*Log, error) {
	if opts == nil {
		opts = &common.Options{}
	}
	o := *opts
	if o.SegmentSize <= 0 {
		o.SegmentSize = defaultSegmentSize
	}

	if err := os.MkdirAll(path, 0750); err != nil {
		return nil, err
	}

	l := &Log{path: path, opts: o}
	if err := l.load(); err != nil {
		return nil, err
	}

	return l, nil
}

func (l *Log) Write(index uint64, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
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
	if l.first == 0 || index < l.first || index > l.last {
		return nil, common.ErrNotFound
	}

	s := l.segments[l.findSegment(index)]
	if !s.loaded {
		if err := l.loadSegment(s); err != nil {
			return nil, err
		}
	}

	pos := s.epos[index-s.index]
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data := make([]byte, pos.size-headerSize)
	if _, err := f.ReadAt(data, pos.offset+headerSize); err != nil {
		return nil, err
	}
	return data, nil
}

func (l *Log) TruncateFront(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	if l.first == 0 || index < l.first || index > l.last {
		return common.ErrOutOfRange
	}
	if index == l.first {
		return nil
	}

	segIdx := l.findSegment(index)
	s := l.segments[segIdx]
	if !s.loaded {
		if err := l.loadSegment(s); err != nil {
			return err
		}
	}

	if err := l.sfile.Close(); err != nil {
		return err
	}

	for i := 0; i < segIdx; i++ {
		if err := os.Remove(l.segments[i].path); err != nil {
			return err
		}
	}

	keepFrom := int(index - s.index)
	if keepFrom > 0 {
		if err := l.rewriteSegmentFile(s, keepFrom, len(s.epos)); err != nil {
			return err
		}
	}

	l.segments = append([]*segment{}, l.segments[segIdx:]...)
	l.first = index

	return l.reopenTail()
}

func (l *Log) TruncateBack(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	if l.first == 0 || index < l.first || index > l.last {
		return common.ErrOutOfRange
	}
	if index == l.last {
		return nil
	}

	segIdx := l.findSegment(index)
	s := l.segments[segIdx]
	if !s.loaded {
		if err := l.loadSegment(s); err != nil {
			return err
		}
	}

	if err := l.sfile.Close(); err != nil {
		return err
	}

	for i := segIdx + 1; i < len(l.segments); i++ {
		if err := os.Remove(l.segments[i].path); err != nil {
			return err
		}
	}

	keepTo := int(index-s.index) + 1
	if keepTo < len(s.epos) {
		if err := l.rewriteSegmentFile(s, 0, keepTo); err != nil {
			return err
		}
	}

	l.segments = append([]*segment{}, l.segments[:segIdx+1]...)
	l.last = index

	return l.reopenTail()
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
func (l *Log) Sync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	return l.sfile.Sync()
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return common.ErrClosed
	}
	l.closed = true
	if err := l.sfile.Sync(); err != nil {
		return err
	}
	return l.sfile.Close()
}

func (l *Log) load() error {
	dirEntries, err := os.ReadDir(l.path)
	if err != nil {
		return err
	}

	for _, e := range dirEntries {
		if e.IsDir() || len(e.Name()) != 20 {
			continue
		}

		index, err := strconv.ParseUint(e.Name(), 10, 64)
		if err != nil || index == 0 {
			continue
		}
		l.segments = append(l.segments, &segment{
			index: index,
			path:  filepath.Join(l.path, e.Name()),
		})
	}

	sort.Slice(l.segments, func(i, j int) bool {
		return l.segments[i].index < l.segments[j].index
	})

	if len(l.segments) == 0 {
		l.segments = append(l.segments, &segment{
			index: 1,
			path:  filepath.Join(l.path, segmentName(1)),
		})
	}

	tail := l.segments[len(l.segments)-1]

	f, err := os.OpenFile(tail.path, os.O_CREATE|os.O_RDWR, 0640)
	if err != nil {
		return err
	}
	l.sfile = f

	if err := l.loadSegment(tail); err != nil {
		f.Close()
		return err
	}

	if _, err := l.sfile.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	l.first = l.segments[0].index
	l.last = tail.index + uint64(len(tail.epos)) - 1
	if l.last == 0 {
		l.first = 0
	}

	return nil
}

func segmentName(index uint64) string {
	return fmt.Sprintf("%020d", index)
}

func (l *Log) loadSegment(s *segment) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var epos []entryPos
	var offset int64
	for offset < int64(len(data)) {
		if offset+headerSize > int64(len(data)) {
			return common.ErrCorrupt
		}
		dataSize := int64(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
		entrySize := headerSize + dataSize
		if offset+entrySize > int64(len(data)) {
			return common.ErrCorrupt
		}
		epos = append(epos, entryPos{offset: offset, size: entrySize})
		offset += entrySize
	}

	s.epos = epos
	s.size = offset
	s.loaded = true
	return nil
}

func appendEntry(index uint64, data []byte) []byte {
	buf := make([]byte, headerSize+len(data))
	binary.BigEndian.PutUint64(buf[0:8], index)
	binary.BigEndian.PutUint64(buf[8:16], uint64(len(data)))
	copy(buf[headerSize:], data)
	return buf
}

func (l *Log) cycle(nextIndex uint64) error {
	if err := l.sfile.Sync(); err != nil {
		return err
	}
	if err := l.sfile.Close(); err != nil {
		return err
	}

	s := &segment{
		index:  nextIndex,
		path:   filepath.Join(l.path, segmentName(nextIndex)),
		loaded: true,
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	l.sfile = f
	l.segments = append(l.segments, s)
	return nil
}

func (l *Log) writeBatch(entries []common.Entry) error {
	next := l.last + 1
	for _, e := range entries {
		if e.Index != next {
			return common.ErrOutOfOrder
		}
		next++
	}

	tail := l.segments[len(l.segments)-1]
	for _, e := range entries {
		buf := appendEntry(e.Index, e.Data)

		if tail.size > 0 && tail.size+int64(len(buf)) > int64(l.opts.SegmentSize) {
			if err := l.cycle(e.Index); err != nil {
				return err
			}
			tail = l.segments[len(l.segments)-1]
		}

		if _, err := l.sfile.Write(buf); err != nil {
			return err
		}
		tail.epos = append(tail.epos, entryPos{offset: tail.size, size: int64(len(buf))})
		tail.size += int64(len(buf))
	}

	if l.first == 0 {
		l.first = entries[0].Index
	}
	l.last = entries[len(entries)-1].Index

	if !l.opts.NoSync {
		if err := l.sfile.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func (l *Log) findSegment(index uint64) int {
	i, j := 0, len(l.segments)
	for i < j {
		h := i + (j-i)/2
		if index >= l.segments[h].index {
			i = h + 1
		} else {
			j = h
		}
	}
	return i - 1
}

func (l *Log) rewriteSegmentFile(s *segment, keepFrom, keepTo int) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	start := s.epos[keepFrom].offset
	var end int64
	if keepTo == len(s.epos) {
		end = s.size
	} else {
		end = s.epos[keepTo].offset
	}
	kept := data[start:end]

	newIndex := s.index + uint64(keepFrom)
	newPath := filepath.Join(l.path, segmentName(newIndex))
	tmpPath := newPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	if _, err := f.Write(kept); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if s.path != newPath {
		if err := os.Remove(s.path); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, newPath); err != nil {
		return err
	}

	newEpos := make([]entryPos, 0, keepTo-keepFrom)
	var offset int64
	for i := keepFrom; i < keepTo; i++ {
		newEpos = append(newEpos, entryPos{offset: offset, size: s.epos[i].size})
		offset += s.epos[i].size
	}

	s.path = newPath
	s.index = newIndex
	s.epos = newEpos
	s.size = offset
	return nil
}

func (l *Log) reopenTail() error {
	tail := l.segments[len(l.segments)-1]
	f, err := os.OpenFile(tail.path, os.O_RDWR, 0640)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return err
	}
	l.sfile = f
	return nil
}
