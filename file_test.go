package memfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateWriteReadFile(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("notes.txt"))

	got, err := fs.ReadFile("notes.txt")
	require.NoError(t, err)
	assert.Empty(t, got, "a new file starts empty")

	require.NoError(t, fs.WriteFile("notes.txt", []byte("hello")))
	got, err = fs.ReadFile("notes.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)
}

func TestWriteFileCopiesInput(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))

	data := []byte("abc")
	require.NoError(t, fs.WriteFile("f", data))
	data[0] = 'X' // caller mutates its buffer after the call

	got, err := fs.ReadFile("f")
	require.NoError(t, err)
	assert.Equal(t, []byte("abc"), got, "stored bytes must not alias the caller's buffer")
}

func TestFileOpErrors(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.Mkdir("d"))

	assert.ErrorIs(t, fs.CreateFile("f"), ErrExists)

	_, err := fs.ReadFile("missing")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = fs.ReadFile("d")
	assert.ErrorIs(t, err, ErrIsDir)

	assert.ErrorIs(t, fs.WriteFile("d", nil), ErrIsDir)
}

func TestRemoveFile(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.Mkdir("d"))

	assert.ErrorIs(t, fs.Remove("d"), ErrIsDir)
	assert.ErrorIs(t, fs.Remove("missing"), ErrNotFound)

	require.NoError(t, fs.Remove("f"))
	_, err := fs.ReadFile("f")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMove(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("old"))
	require.NoError(t, fs.WriteFile("old", []byte("data")))
	require.NoError(t, fs.CreateFile("taken"))
	require.NoError(t, fs.Mkdir("dir"))

	require.NoError(t, fs.Move("old", "new"))
	got, err := fs.ReadFile("new")
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), got)

	_, err = fs.ReadFile("old")
	assert.ErrorIs(t, err, ErrNotFound, "old name must not resolve after Move")

	assert.ErrorIs(t, fs.Move("missing", "x"), ErrNotFound)
	assert.ErrorIs(t, fs.Move("dir", "x"), ErrIsDir)
	assert.ErrorIs(t, fs.Move("new", "taken"), ErrExists)
	assert.ErrorIs(t, fs.Move("new", "a/b"), ErrInvalidName)
}
