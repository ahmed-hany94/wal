package segmented

import (
	"fmt"
	"log"
	"main/common"
	"os"
	"sort"
)

func RunDemo() {
	dir := "segmented.wal"
	defer os.RemoveAll(dir)

	opts := &common.Options{SegmentSize: 64}

	// --- Open a fresh log ---
	l, err := Open(dir, opts)
	if err != nil {
		log.Fatalf("open: %v", err)
	}

	// --- Write a few entries ---
	for i := uint64(1); i <= 12; i++ {
		data := []byte(fmt.Sprintf("entry-%d", i))
		if err := l.Write(i, data); err != nil {
			log.Fatalf("write %d: %v", i, err)
		}
	}
	fmt.Println("wrote entries 1..12")
	printSegments(dir)

	// --- Write a batch ---
	batch := []common.Entry{
		{Index: 13, Data: []byte("entry-13")},
		{Index: 14, Data: []byte("entry-14")},
	}
	if err := l.WriteBatch(batch); err != nil {
		log.Fatalf("writebatch: %v", err)
	}
	fmt.Println("wrote batch 13..14")
	printSegments(dir)

	// --- Read them back ---
	first, _ := l.FirstIndex()
	last, _ := l.LastIndex()
	fmt.Printf("firstIndex=%d lastIndex=%d\n", first, last)

	for i := first; i <= last; i++ {
		data, err := l.Read(i)
		if err != nil {
			log.Fatalf("read %d: %v", i, err)
		}
		fmt.Printf("read(%d) = %q\n", i, data)
	}

	// --- Reject out-of-order write ---
	if err := l.Write(5, []byte("bad")); err != nil {
		fmt.Printf("write(5) correctly rejected: %v\n", err)
	}

	// --- Truncate front: drop entries before 3 ---
	if err := l.TruncateFront(7); err != nil {
		log.Fatalf("truncatefront: %v", err)
	}
	first, _ = l.FirstIndex()
	fmt.Printf("after TruncateFront(7): firstIndex=%d\n", first)
	printSegments(dir)

	// --- Truncate back: drop entries after 6 ---
	if err := l.TruncateBack(11); err != nil {
		log.Fatalf("truncateback: %v", err)
	}
	last, _ = l.LastIndex()
	fmt.Printf("after TruncateBack(11): lastIndex=%d\n", last)
	printSegments(dir)

	// --- Confirm truncated entries are gone ---
	if _, err := l.Read(6); err != nil {
		fmt.Printf("read(6) correctly missing: %v\n", err)
	}
	if _, err := l.Read(12); err != nil {
		fmt.Printf("read(12) correctly missing: %v\n", err)
	}

	// --- Close, then reopen to confirm persistence ---
	if err := l.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}
	fmt.Println("closed log")

	l2, err := Open(dir, opts)
	if err != nil {
		log.Fatalf("reopen: %v", err)
	}
	defer l2.Close()

	first, _ = l2.FirstIndex()
	last, _ = l2.LastIndex()
	fmt.Printf("reopened: firstIndex=%d lastIndex=%d\n", first, last)
	for i := first; i <= last; i++ {
		data, err := l2.Read(i)
		if err != nil {
			log.Fatalf("read %d after reopen: %v", i, err)
		}
		fmt.Printf("read(%d) after reopen = %q\n", i, data)
	}
}

func printSegments(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Fatalf("readdir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	fmt.Printf("  segment files: %v\n", names)
}
