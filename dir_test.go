package memfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMkdirErrors(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("a"))

	assert.ErrorIs(t, fs.Mkdir("a"), ErrExists)
	for _, bad := range []string{"", ".", "..", "a/b"} {
		assert.ErrorIs(t, fs.Mkdir(bad), ErrInvalidName, "Mkdir(%q)", bad)
	}
}

func TestRemoveDirEmptyOnly(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("empty"))
	require.NoError(t, fs.Mkdir("parent"))
	require.NoError(t, fs.Cd("parent"))
	require.NoError(t, fs.Mkdir("child"))
	require.NoError(t, fs.Cd(".."))

	require.NoError(t, fs.RemoveDir("empty"))
	assert.ErrorIs(t, fs.RemoveDir("parent"), ErrDirNotEmpty)
}

func TestRemoveDirErrors(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("file"))

	assert.ErrorIs(t, fs.RemoveDir("missing"), ErrNotFound)
	assert.ErrorIs(t, fs.RemoveDir("file"), ErrNotDir)
}

func TestRemoveAll(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("tree"))
	require.NoError(t, fs.Cd("tree"))
	require.NoError(t, fs.Mkdir("sub"))
	require.NoError(t, fs.CreateFile("leaf"))
	require.NoError(t, fs.Cd(".."))

	require.NoError(t, fs.RemoveAll("tree"))
	assert.Empty(t, fs.Ls())
	assert.ErrorIs(t, fs.RemoveAll("missing"), ErrNotFound)
}
