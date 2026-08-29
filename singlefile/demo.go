package singlefile

import (
	"fmt"
	"log"
	"os"

	"main/common"
)

func RunDemo() {
	path := "singlefile.wal"
	defer os.Remove(path)

	// --- Open a fresh log ---
	l, err := Open(path, &common.Options{})
	if err != nil {
		log.Fatalf("open: %v", err)
	}

	// --- Write a few entries ---
	for i := uint64(1); i <= 5; i++ {
		data := []byte(fmt.Sprintf("entry-%d", i))
		if err := l.Write(i, data); err != nil {
			log.Fatalf("write %d: %v", i, err)
		}
	}

	fmt.Println("wrote entries 1..5")

	// --- Write a batch ---
	batch := []common.Entry{
		{Index: 6, Data: []byte("entry-6")},
		{Index: 7, Data: []byte("entry-7")},
		{Index: 8, Data: []byte("entry-8")},
	}
	if err := l.WriteBatch(batch); err != nil {
		log.Fatalf("writebatch: %v", err)
	}
	fmt.Println("wrote batch 6..8")

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
	if err := l.Write(3, []byte("bad")); err != nil {
		fmt.Printf("write(3) correctly rejected: %v\n", err)
	}

	// --- Truncate front: drop entries before 3 ---
	if err := l.TruncateFront(3); err != nil {
		log.Fatalf("truncatefront: %v", err)
	}
	first, _ = l.FirstIndex()
	fmt.Printf("after TruncateFront(3): firstIndex=%d\n", first)

	// --- Truncate back: drop entries after 6 ---
	if err := l.TruncateBack(6); err != nil {
		log.Fatalf("truncateback: %v", err)
	}
	last, _ = l.LastIndex()
	fmt.Printf("after TruncateBack(6): lastIndex=%d\n", last)

	// --- Confirm truncated entries are gone ---
	if _, err := l.Read(2); err != nil {
		fmt.Printf("read(2) correctly missing: %v\n", err)
	}
	if _, err := l.Read(7); err != nil {
		fmt.Printf("read(7) correctly missing: %v\n", err)
	}

	// --- Close, then reopen to confirm persistence ---
	if err := l.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}
	fmt.Println("closed log")

	l2, err := Open(path, &common.Options{})
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
