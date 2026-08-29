package bench

import (
	"main/common"
	"main/segmented"
	"main/singlefile"
	"path/filepath"
	"testing"
)

type variant struct {
	name string
	open func(path string, opts *common.Options) (common.WAL, error)
}

var variants = []variant{
	{
		name: "singlefile",
		open: func(path string, opts *common.Options) (common.WAL, error) {
			return singlefile.Open(filepath.Join(path, "wal.log"), opts)
		},
	},
	{
		name: "segmented",
		open: func(path string, opts *common.Options) (common.WAL, error) {
			return segmented.Open(path, opts)
		},
	},
}

func payload(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// pseudoRandomIndexes generates n indexes in [1, max]
// using a fixed-seed xorshift generator so every variant
// is benchmarked against the identical access pattern.
func pseudoRandomIndexes(n, max int) []uint64 {
	out := make([]uint64, n)
	state := uint64(88172645463325252)
	for i := 0; i < n; i++ {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[i] = (state % uint64(max)) + 1
	}
	return out
}

// BenchmarkSequentialWrite measures single-entry Write() throughput,
// one fsync per call by default.
func BenchmarkSequentialWrite(b *testing.B) {
	data := payload(128)
	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			dir := b.TempDir()
			w, err := v.open(dir, &common.Options{})
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			defer w.Close()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := w.Write(uint64(i+1), data); err != nil {
					b.Fatalf("write: %v", err)
				}
			}
		})
	}
}

// BenchmarkBatchWrite measures WriteBatch()
// throughput for a fixed batch size,
// isolating the effect of one fsync per batch instead of per entry.
func BenchmarkBatchWrite(b *testing.B) {
	const batchSize = 100
	data := payload(128)

	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			dir := b.TempDir()
			w, err := v.open(dir, &common.Options{})
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			defer w.Close()

			b.ReportAllocs()
			b.ResetTimer()
			index := uint64(1)
			for i := 0; i < b.N; i++ {
				entries := make([]common.Entry, batchSize)
				for j := range entries {
					entries[j] = common.Entry{Index: index, Data: data}
					index++
				}
				if err := w.WriteBatch(entries); err != nil {
					b.Fatalf("writebatch: %v", err)
				}
			}
		})
	}
}

// BenchmarkRandomRead measures Read() throughput
// after pre-loading a fixed number of entries,
// reading in random order.
func BenchmarkRandomRead(b *testing.B) {
	const preload = 10000
	data := payload(128)

	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			dir := b.TempDir()
			w, err := v.open(dir, &common.Options{})
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			defer w.Close()

			for i := 0; i < preload; i++ {
				if err := w.Write(uint64(i+1), data); err != nil {
					b.Fatalf("preload write: %v", err)
				}
			}
			indexes := pseudoRandomIndexes(b.N, preload)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := w.Read(indexes[i]); err != nil {
					b.Fatalf("read: %v", err)
				}
			}
		})
	}
}

// BenchmarkTruncateFront measures the cost of dropping
// the first half of a preloaded log — this is the one
// where segmented's "delete whole files" approach should
// visibly beat singlefile's "rewrite everything" approach.
func BenchmarkTruncateFront(b *testing.B) {
	const preload = 2000
	data := payload(128)

	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				dir := b.TempDir()
				w, err := v.open(dir, &common.Options{SegmentSize: 64 * 1024})
				if err != nil {
					b.Fatalf("open: %v", err)
				}
				for j := 1; j <= preload; j++ {
					if err := w.Write(uint64(j), data); err != nil {
						b.Fatalf("preload write: %v", err)
					}
				}
				b.StartTimer()

				if err := w.TruncateFront(preload / 2); err != nil {
					b.Fatalf("truncatefront: %v", err)
				}

				b.StopTimer()
				w.Close()
			}
		})
	}
}
