package memfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStartsAtRoot(t *testing.T) {
	fs := New()
	assert.Equal(t, "/", fs.Pwd())
	assert.Empty(t, fs.Ls())
}

func TestCdAndPwd(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("a"))
	require.NoError(t, fs.Cd("a"))
	require.NoError(t, fs.Mkdir("b"))
	require.NoError(t, fs.Cd("b"))
	assert.Equal(t, "/a/b", fs.Pwd())

	require.NoError(t, fs.Cd(".."))
	assert.Equal(t, "/a", fs.Pwd())
}

func TestCdParentAtRootIsNoOp(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Cd("..")) // should neither error nor move above root
	assert.Equal(t, "/", fs.Pwd())
}

func TestCdErrors(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("afile"))

	assert.ErrorIs(t, fs.Cd("missing"), ErrNotFound)
	assert.ErrorIs(t, fs.Cd("afile"), ErrNotDir)
}

func TestLsIsSorted(t *testing.T) {
	fs := New()
	for _, name := range []string{"zebra", "apple", "mango"} {
		require.NoError(t, fs.Mkdir(name))
	}
	assert.Equal(t, []string{"apple", "mango", "zebra"}, fs.Ls())
}

func TestLsReturnsCopy(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("a"))

	got := fs.Ls()
	got[0] = "mutated"
	assert.Equal(t, []string{"a"}, fs.Ls(), "Ls must not return a slice aliased into state")
}
