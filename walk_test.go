package memfs

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkVisitsAllDescendants(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("a"))
	require.NoError(t, fs.Cd("a"))
	require.NoError(t, fs.Mkdir("b"))
	require.NoError(t, fs.CreateFile("f"))
	require.NoError(t, fs.Cd(".."))

	var visited []string
	require.NoError(t, fs.Walk(func(path string, _ bool) error {
		visited = append(visited, path)
		return nil
	}))
	assert.Equal(t, []string{"/a", "/a/b", "/a/f"}, visited)
}

func TestWalkSkipDirPrunes(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("keep"))
	require.NoError(t, fs.Mkdir("prune"))
	require.NoError(t, fs.Cd("prune"))
	require.NoError(t, fs.CreateFile("hidden"))
	require.NoError(t, fs.Cd(".."))
	require.NoError(t, fs.Cd("keep"))
	require.NoError(t, fs.CreateFile("shown"))
	require.NoError(t, fs.Cd(".."))

	var visited []string
	require.NoError(t, fs.Walk(func(path string, _ bool) error {
		visited = append(visited, path)
		if path == "/prune" {
			return SkipDir
		}
		return nil
	}))
	// /prune is visited but its child /prune/hidden is pruned.
	assert.Equal(t, []string{"/keep", "/keep/shown", "/prune"}, visited)
}

func TestWalkPropagatesError(t *testing.T) {
	fs := New()
	require.NoError(t, fs.Mkdir("a"))
	require.NoError(t, fs.Mkdir("b"))

	sentinel := errors.New("stop")
	err := fs.Walk(func(string, bool) error { return sentinel })
	assert.ErrorIs(t, err, sentinel)
}

func TestFindRecursesAndScopesToCwd(t *testing.T) {
	fs := buildTreeForFind(t)

	// From the root, "target" matches the file and dir at every depth.
	assert.Equal(t, []string{"/a/b/target", "/a/target", "/target"}, fs.Find("target"))

	// Scoped to /a, the root-level /target is out of scope.
	require.NoError(t, fs.Cd("a"))
	assert.Equal(t, []string{"/a/b/target", "/a/target"}, fs.Find("target"))

	assert.Empty(t, fs.Find("nonexistent"))
}

func TestFindRegex(t *testing.T) {
	fs := New()
	require.NoError(t, fs.CreateFile("notes.txt"))
	require.NoError(t, fs.CreateFile("report.md"))
	require.NoError(t, fs.Mkdir("sub"))
	require.NoError(t, fs.Cd("sub"))
	require.NoError(t, fs.CreateFile("draft.txt"))
	require.NoError(t, fs.Cd(".."))

	got, err := fs.FindRegex(`\.txt$`)
	require.NoError(t, err)
	assert.Equal(t, []string{"/notes.txt", "/sub/draft.txt"}, got)

	_, err = fs.FindRegex("[")
	assert.Error(t, err, "an invalid pattern should return a compile error")
}

// TestWalkConcurrentWithRename runs Walk concurrently with a goroutine that
// repeatedly renames a file the walk observes. Walk reads each entry's name,
// and Move mutates that same node's name field; this test is meaningful only
// under `go test -race`, which flags the read/write if Walk captures the name
// outside the filesystem lock.
func TestWalkConcurrentWithRename(t *testing.T) {
	const iters = 2000

	fs := New()
	require.NoError(t, fs.CreateFile("a")) // the file being renamed back and forth
	require.NoError(t, fs.Mkdir("sub"))    // extra entries so the walk does real work
	require.NoError(t, fs.Cd("sub"))
	require.NoError(t, fs.CreateFile("x"))
	require.NoError(t, fs.Cd(".."))

	var wg sync.WaitGroup
	wg.Add(2)

	// Renamer: flip the file's name under FS.mu via Move (which calls setName).
	go func() {
		defer wg.Done()
		cur, next := "a", "b"
		for i := 0; i < iters; i++ {
			if err := fs.Move(cur, next); err != nil {
				t.Errorf("Move(%q,%q): %v", cur, next, err)
				return
			}
			cur, next = next, cur
		}
	}()

	// Walker: traverse repeatedly, touching every entry's name via path.
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = fs.Walk(func(string, bool) error { return nil })
		}
	}()

	wg.Wait()
}

// buildTreeForFind builds a fixture with the name "target" appearing as a file
// and a directory at several depths, with the cwd left at the root:
//
//	/a  /a/b  /a/b/target(file)  /a/target(dir)  /c  /target(file)
func buildTreeForFind(t *testing.T) *FS {
	t.Helper()
	fs := New()
	require.NoError(t, fs.Mkdir("a"))
	require.NoError(t, fs.Mkdir("c"))
	require.NoError(t, fs.CreateFile("target"))
	require.NoError(t, fs.Cd("a"))
	require.NoError(t, fs.Mkdir("target"))
	require.NoError(t, fs.Mkdir("b"))
	require.NoError(t, fs.Cd("b"))
	require.NoError(t, fs.CreateFile("target"))
	require.NoError(t, fs.Cd(".."))
	require.NoError(t, fs.Cd(".."))
	return fs
}
