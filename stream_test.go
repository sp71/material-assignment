package memfs

import (
	"errors"
	"io"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamWriteThenRead(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	_, err = w.Write([]byte("hello world"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello world"), got)
}

func TestStreamChunkedRead(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.WriteFile("f", []byte("abcdefg")))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	var got []byte
	buf := make([]byte, 3) // smaller than the content: forces multiple reads
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, []byte("abcdefg"), got)
}

func TestStreamRandomAccess(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	_, err = w.Write([]byte("AAAA")) // -> AAAA
	require.NoError(t, err)
	_, err = w.WriteAt([]byte("BB"), 1) // overwrite middle -> ABBA
	require.NoError(t, err)
	require.NoError(t, w.Close())

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	// ReadAt does not move the offset.
	buf := make([]byte, 2)
	_, err = r.ReadAt(buf, 1)
	require.NoError(t, err)
	assert.Equal(t, []byte("BB"), buf)

	// Seek to end-1 and read the last byte.
	_, err = r.Seek(-1, io.SeekEnd)
	require.NoError(t, err)
	last := make([]byte, 1)
	_, err = r.Read(last)
	require.NoError(t, err)
	assert.Equal(t, byte('A'), last[0], "content should be ABBA")
}

func TestStreamWritePastEndExtends(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	_, err = w.WriteAt([]byte("Z"), 4) // gap [0,4) zero-filled
	require.NoError(t, err)
	require.NoError(t, w.Close())

	got, err := fs.ReadFile("f")
	require.NoError(t, err)
	assert.Equal(t, []byte{0, 0, 0, 0, 'Z'}, got)
}

func TestStreamReadPastEndIsEOF(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.WriteFile("f", []byte("ab")))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	_, err = r.Seek(10, io.SeekStart)
	require.NoError(t, err)
	_, err = r.Read(make([]byte, 4))
	assert.ErrorIs(t, err, io.EOF)
}

func TestStreamClosedHandle(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	r, err := fs.Open("f")
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.NoError(t, r.Close(), "Close is idempotent")

	_, err = r.Read(make([]byte, 1))
	assert.ErrorIs(t, err, ErrClosed)
	_, err = r.Seek(0, io.SeekStart)
	assert.ErrorIs(t, err, ErrClosed)

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = w.Write([]byte("x"))
	assert.ErrorIs(t, err, ErrClosed)
}

func TestOpenWriterSingleWriter(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w1, err := fs.OpenWriter("f")
	require.NoError(t, err)

	_, err = fs.OpenWriter("f")
	assert.ErrorIs(t, err, ErrWriterBusy)

	// Closing the first writer frees the slot.
	require.NoError(t, w1.Close())
	w2, err := fs.OpenWriter("f")
	require.NoError(t, err)
	require.NoError(t, w2.Close())
}

func TestStreamOpenErrors(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("d"))

	_, err := fs.Open("missing")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = fs.Open("d")
	assert.ErrorIs(t, err, ErrIsDir)

	_, err = fs.OpenWriter("d")
	assert.ErrorIs(t, err, ErrIsDir)
}

// TestStreamSequentialWriteGrowsGeometrically writes a file one byte at a time
// and asserts the backing array is reallocated only logarithmically (geometric
// growth), not once per write. Against the old reallocate-the-whole-buffer
// implementation this fails with reallocs == n.
func TestStreamSequentialWriteGrowsGeometrically(t *testing.T) {
	const n = 4096

	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	defer w.Close()

	reallocs, prevCap := 0, 0
	for i := range n {
		_, err := w.Write([]byte{byte(i)})
		require.NoError(t, err)
		if c := cap(w.c.data); c != prevCap { // single goroutine: direct read is safe
			reallocs++
			prevCap = c
		}
	}

	require.Len(t, w.c.data, n)
	// Doubling from empty to 4096 is ~13 reallocations; allow generous headroom
	// while still being far below the per-write count (4096) of the old code.
	assert.Less(t, reallocs, 20, "expected geometric growth, got near-per-write reallocations")

	// Content is still correct end to end.
	got, err := fs.ReadFile("f")
	require.NoError(t, err)
	want := make([]byte, n)
	for i := range want {
		want[i] = byte(i)
	}
	assert.Equal(t, want, got)
}

func TestSeekInvalid(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.WriteFile("f", []byte("hello")))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	defer w.Close()

	// A bad whence and a seek that resolves to a negative offset are rejected,
	// on both the read and write handles (they share resolveSeek).
	const badWhence = 99
	_, err = r.Seek(0, badWhence)
	assert.ErrorIs(t, err, ErrInvalidSeek)
	_, err = r.Seek(-1, io.SeekStart)
	assert.ErrorIs(t, err, ErrInvalidSeek)
	_, err = r.Seek(-100, io.SeekCurrent)
	assert.ErrorIs(t, err, ErrInvalidSeek)

	_, err = w.Seek(0, badWhence)
	assert.ErrorIs(t, err, ErrInvalidSeek)
	_, err = w.Seek(-1, io.SeekStart)
	assert.ErrorIs(t, err, ErrInvalidSeek)
}

func TestReadAtWriteAtNegativeOffset(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()
	_, err = r.ReadAt(make([]byte, 1), -1)
	assert.ErrorIs(t, err, ErrInvalidSeek)

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	defer w.Close()
	_, err = w.WriteAt([]byte("x"), -1)
	assert.ErrorIs(t, err, ErrInvalidSeek)
}

// TestWriteOffsetOverflow checks that an offset so large that off+len(p)
// overflows int64 is rejected with ErrInvalidSeek rather than panicking in a
// huge make/slice — through both WriteAt and the offset-advancing Write path.
func TestWriteOffsetOverflow(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)
	defer w.Close()

	_, err = w.WriteAt([]byte("data"), math.MaxInt64-1)
	assert.ErrorIs(t, err, ErrInvalidSeek)

	// Seeking to a huge offset is allowed; the overflow is caught at Write time.
	_, err = w.Seek(math.MaxInt64-1, io.SeekStart)
	require.NoError(t, err)
	_, err = w.Write([]byte("data"))
	assert.ErrorIs(t, err, ErrInvalidSeek)
}

// TestStreamSurvivesMove opens a reader, renames the file out from under it,
// and confirms the read still returns the original bytes — the handle binds to
// content, not path.
func TestStreamSurvivesMove(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("old"))
	require.NoError(t, fs.WriteFile("old", []byte("payload")))

	r, err := fs.Open("old")
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, fs.Move("old", "new"))

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)

	_, err = fs.ReadFile("new")
	assert.NoError(t, err, "file should be reachable under its new name")
}

// TestStreamSurvivesRemove opens a reader, removes the file, and confirms the
// read still succeeds (Unix unlink-while-open semantics).
func TestStreamSurvivesRemove(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.WriteFile("f", []byte("still here")))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, fs.Remove("f"))
	_, err = fs.ReadFile("f")
	assert.ErrorIs(t, err, ErrNotFound)

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("still here"), got)
}

// TestStreamSurvivesRemoveAllParent removes the entire parent subtree while a
// reader is open on a file inside it.
func TestStreamSurvivesRemoveAllParent(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("dir"))
	require.NoError(t, fs.Cd("dir"))
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.WriteFile("f", []byte("deep")))

	r, err := fs.Open("f")
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, fs.Cd(".."))
	require.NoError(t, fs.RemoveAll("dir"))

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("deep"), got)
}

// TestStreamConcurrent runs one writer and several readers against the same
// file simultaneously. Its real purpose is to be run under `go test -race`:
// it asserts no panic/deadlock and a consistent final length.
func TestStreamConcurrent(t *testing.T) {
	const writes = 200
	const readers = 8

	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	w, err := fs.OpenWriter("f")
	require.NoError(t, err)

	var wg sync.WaitGroup

	// Single writer: extend the file one byte at a time.
	wg.Go(func() {
		for i := range writes {
			if _, err := w.WriteAt([]byte{'x'}, int64(i)); err != nil {
				t.Errorf("WriteAt: %v", err)
				return
			}
		}
	})

	// Many readers hammering ReadAt + whole-file ReadFile concurrently.
	for range readers {
		wg.Go(func() {
			r, err := fs.Open("f")
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer r.Close()
			buf := make([]byte, 16)
			for range writes {
				if _, err := r.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
					t.Errorf("ReadAt: %v", err)
					return
				}
				if _, err := fs.ReadFile("f"); err != nil {
					t.Errorf("ReadFile: %v", err)
					return
				}
			}
		})
	}

	wg.Wait()
	require.NoError(t, w.Close())

	got, err := fs.ReadFile("f")
	require.NoError(t, err)
	assert.Len(t, got, writes)
}
