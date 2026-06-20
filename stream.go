package memfs

import (
	"io"
	"sync"
)

// Compile-time proof that the handles satisfy the standard streaming
// interfaces, so callers can drop them into io.Copy and friends.
var (
	_ io.Reader   = (*FileReader)(nil)
	_ io.ReaderAt = (*FileReader)(nil)
	_ io.Seeker   = (*FileReader)(nil)
	_ io.Closer   = (*FileReader)(nil)

	_ io.Writer   = (*FileWriter)(nil)
	_ io.WriterAt = (*FileWriter)(nil)
	_ io.Seeker   = (*FileWriter)(nil)
	_ io.Closer   = (*FileWriter)(nil)
)

// FileReader is a streaming read handle. It binds to a file's content, not its
// path, so it keeps working after the file is renamed or removed. Multiple
// readers may be open on the same file at once.
//
// Locking: mu guards this handle's own offset and closed flag; the content's
// RWMutex guards the bytes. The two are always acquired in that order (handle
// then content) so concurrent handles can never deadlock.
type FileReader struct {
	c      *fileContent
	mu     sync.Mutex
	off    int64
	closed bool
}

// FileWriter is a streaming write handle. At most one may be open on a file at
// a time (OpenWriter returns ErrWriterBusy otherwise). Like FileReader it binds
// to the content and survives renames/removals.
type FileWriter struct {
	c      *fileContent
	mu     sync.Mutex
	off    int64
	closed bool
}

// Open returns a read handle for a file in the current working directory. Any
// number of readers may be open concurrently. Returns ErrNotFound or ErrIsDir.
func (fs *FS) Open(name string) (*FileReader, error) {
	fs.mu.RLock()
	f, err := fs.lookupFile(name)
	fs.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return &FileReader{c: f.content}, nil
}

// OpenWriter returns the single write handle for a file in the current working
// directory. Returns ErrNotFound, ErrIsDir, or ErrWriterBusy if a writer is
// already open. The handle starts at offset 0 and does not truncate existing
// content (like opening O_WRONLY without O_TRUNC); seek or WriteAt for random
// access, or use WriteFile to replace contents wholesale.
func (fs *FS) OpenWriter(name string) (*FileWriter, error) {
	fs.mu.RLock()
	f, err := fs.lookupFile(name)
	fs.mu.RUnlock()
	if err != nil {
		return nil, err
	}

	c := f.content
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writerOpen {
		return nil, wrap(name, ErrWriterBusy)
	}
	c.writerOpen = true
	return &FileWriter{c: c}, nil
}

// --- FileReader ---

// Read implements io.Reader, reading from the handle's current offset and
// advancing it. Returns io.EOF once the offset reaches the end of the content,
// and ErrClosed after Close.
func (r *FileReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, ErrClosed
	}

	r.c.mu.RLock()
	defer r.c.mu.RUnlock()
	if r.off >= int64(len(r.c.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.c.data[r.off:])
	r.off += int64(n)
	return n, nil
}

// ReadAt implements io.ReaderAt: it reads at an absolute offset without
// touching the handle's offset, so it is safe for concurrent calls. Per the
// io.ReaderAt contract it returns io.EOF when it reads fewer than len(p) bytes.
func (r *FileReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrInvalidSeek
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}

	r.c.mu.RLock()
	defer r.c.mu.RUnlock()
	if off >= int64(len(r.c.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.c.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Seek implements io.Seeker. Seeking to a negative offset is ErrInvalidSeek;
// seeking past the end is allowed (a subsequent Read returns io.EOF).
func (r *FileReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, ErrClosed
	}
	next, err := resolveSeek(r.off, r.contentLen(), offset, whence)
	if err != nil {
		return 0, err
	}
	r.off = next
	return next, nil
}

// Close marks the handle closed; subsequent operations return ErrClosed. It is
// idempotent.
func (r *FileReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *FileReader) contentLen() int64 {
	r.c.mu.RLock()
	defer r.c.mu.RUnlock()
	return int64(len(r.c.data))
}

// --- FileWriter ---

// Write implements io.Writer, writing at the handle's current offset and
// advancing it. Writing past the current end extends the content (any gap is
// zero-filled). Returns ErrClosed after Close.
func (w *FileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}

	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	w.c.writeAt(p, w.off)
	w.off += int64(len(p))
	return len(p), nil
}

// WriteAt implements io.WriterAt: it writes at an absolute offset without
// touching the handle's offset. Writing past the end extends the content.
func (w *FileWriter) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrInvalidSeek
	}
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}

	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	w.c.writeAt(p, off)
	return len(p), nil
}

// Seek implements io.Seeker for the write handle; same rules as FileReader.Seek.
func (w *FileWriter) Seek(offset int64, whence int) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}
	next, err := resolveSeek(w.off, w.contentLen(), offset, whence)
	if err != nil {
		return 0, err
	}
	w.off = next
	return next, nil
}

// Close releases the single-writer slot so the file can be opened for writing
// again, and marks the handle closed. It is idempotent.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true

	w.c.mu.Lock()
	w.c.writerOpen = false
	w.c.mu.Unlock()
	return nil
}

func (w *FileWriter) contentLen() int64 {
	w.c.mu.RLock()
	defer w.c.mu.RUnlock()
	return int64(len(w.c.data))
}

// --- shared helpers ---

// writeAt copies p into the content at off, growing the backing slice (zero-
// filling any gap) when the write extends past the current end. Callers must
// hold c.mu for writing.
func (c *fileContent) writeAt(p []byte, off int64) {
	end := off + int64(len(p))
	if end > int64(len(c.data)) {
		grown := make([]byte, end)
		copy(grown, c.data)
		c.data = grown
	}
	copy(c.data[off:], p)
}

// resolveSeek computes the new absolute offset for a seek from cur against a
// content of the given length. A negative result is ErrInvalidSeek; seeking
// past length is permitted.
func resolveSeek(cur, length, offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = cur
	case io.SeekEnd:
		base = length
	default:
		return 0, ErrInvalidSeek
	}
	next := base + offset
	if next < 0 {
		return 0, ErrInvalidSeek
	}
	return next, nil
}
