// Command streamdemo narrates the streaming features of package memfs:
//
//   - io handles bind to a file's *content*, not its path, so a reader keeps
//     working after the file is renamed out from under it; and
//   - one writer can extend a file while many readers read it concurrently,
//     with no data races and no deadlock.
//
// It is a guided tour of what stream_test.go verifies. Run it with:
//
//	go run ./cmd/streamdemo
//	go run -race ./cmd/streamdemo   # prove the concurrent step is race-free
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"memfs"
)

const payload = "the quick brown fox jumps over the lazy dog.\n"

func main() {
	fmt.Println("=== memfs streaming demo ===")
	fs := memfs.New()

	// [1] Write a payload to a fresh file.
	step(1, "write a payload")
	check(fs.CreateFile("log.txt"))
	check(fs.WriteFile("log.txt", []byte(payload)))
	info("created /log.txt and wrote %d bytes: %q", len(payload), payload)

	// [2] Open a streaming reader and peek at the first few bytes so it has a
	//     non-zero offset we can watch survive the rename below.
	step(2, "open a reader and read the first word")
	r, err := fs.Open("log.txt")
	check(err)
	defer r.Close()
	peek := make([]byte, len("the quick"))
	n, err := r.Read(peek)
	check(err)
	info("reader open; read %d bytes: %q (offset is now %d)", n, peek[:n], n)

	// [3] Rename the file while the reader is still open. The handle holds the
	//     file's content directly, so the rename is invisible to it — it resumes
	//     from exactly where it left off.
	step(3, "rename the file out from under the open reader")
	check(fs.Move("log.txt", "log.archived"))
	info("renamed /log.txt -> /log.archived")
	rest, err := io.ReadAll(r)
	check(err)
	info("reader survived the rename; remaining bytes: %q", rest)
	if _, err := fs.ReadFile("log.txt"); err != nil {
		info("...and the old name no longer resolves: %v", err)
	}

	// [4] One writer + eight readers, all on the same file at the same time.
	step(4, "launch 1 writer and 8 readers concurrently")
	finalLen := concurrentReadWrite(fs, "log.archived")

	// [5] Report the final length.
	step(5, "final length")
	info("/log.archived is now %d bytes (%d from the payload + %d appended)",
		finalLen, len(payload), finalLen-len(payload))
}

// concurrentReadWrite runs one writer that appends to the file while eight
// readers read it concurrently, then returns the file's final length. It
// mirrors TestStreamConcurrent — the point is that it stays correct under
// `go run -race`.
func concurrentReadWrite(fs *memfs.FS, name string) int {
	const (
		readers = 8
		appends = 1000
	)

	existing, err := fs.ReadFile(name)
	check(err)
	base := int64(len(existing)) // append past whatever is already there

	w, err := fs.OpenWriter(name)
	check(err)

	var wg sync.WaitGroup
	var reads atomic.Int64

	// One writer: append `appends` bytes, one at a time, past the current end.
	wg.Go(func() {
		for i := range appends {
			if _, err := w.WriteAt([]byte{'.'}, base+int64(i)); err != nil {
				fail(err)
			}
		}
	})

	// Eight readers: each does `appends` absolute reads from offset 0. ReadAt
	// uses no shared offset, so the readers neither block nor interfere with
	// each other or with the writer (beyond the per-file content lock).
	for range readers {
		wg.Go(func() {
			rd, err := fs.Open(name)
			if err != nil {
				fail(err)
			}
			defer rd.Close()
			buf := make([]byte, 64)
			for range appends {
				if _, err := rd.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
					fail(err)
				}
				reads.Add(1)
			}
		})
	}

	wg.Wait()
	check(w.Close())

	info("%d reads across %d readers ran alongside %d writes — no races, no deadlock",
		reads.Load(), readers, appends)

	final, err := fs.ReadFile(name)
	check(err)
	return len(final)
}

// --- narration helpers ---

func step(n int, msg string) { fmt.Printf("\n[%d] %s\n", n, msg) }

func info(format string, a ...any) { fmt.Printf("    "+format+"\n", a...) }

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demo failed:", err)
	os.Exit(1)
}
