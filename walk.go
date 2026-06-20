package memfs

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

// WalkFunc is invoked by Walk for each descendant of the current working
// directory. path is the absolute path of the entry and isDir reports whether
// it is a directory. Returning SkipDir from a directory prunes its subtree;
// returning any other non-nil error aborts the walk and is returned by Walk.
type WalkFunc func(path string, isDir bool) error

// SkipDir, when returned by a WalkFunc for a directory, tells Walk not to
// descend into that directory. Returned for a file it simply skips nothing
// further (files have no subtree).
var SkipDir = errors.New("skip directory subtree")

// Walk visits every descendant of the current working directory, depth-first
// and in sorted order within each directory (so results are deterministic),
// invoking fn for each.
//
// The visitor runs outside the filesystem lock: Walk snapshots one directory's
// children at a time under a read lock, releases it, then calls fn. That means
// (a) a WalkFunc may safely call back into the FS without deadlocking, and
// (b) Walk has snapshot semantics — concurrent mutations may or may not be
// observed, but the traversal never crashes on them.
func (fs *FS) Walk(fn WalkFunc) error {
	fs.mu.RLock()
	start := fs.cwd
	startPath := absPath(start)
	fs.mu.RUnlock()
	return fs.walkDir(start, startPath, fn)
}

func (fs *FS) walkDir(dir *directory, dirPath string, fn WalkFunc) error {
	for _, child := range fs.snapshotChildren(dir) {
		childPath := joinPath(dirPath, child.name())

		err := fn(childPath, child.isDir())
		if errors.Is(err, SkipDir) {
			continue // prune: do not descend into this entry
		}
		if err != nil {
			return err
		}

		if sub, ok := child.(*directory); ok {
			if err := fs.walkDir(sub, childPath, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshotChildren returns dir's children sorted by name, copied under a brief
// read lock so the visitor can run without holding the lock.
func (fs *FS) snapshotChildren(dir *directory) []node {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	children := make([]node, 0, len(dir.children))
	for _, child := range dir.children {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].name() < children[j].name()
	})
	return children
}

// Find returns the absolute paths of every descendant of the current working
// directory whose name is exactly name, in deterministic (sorted-walk) order.
// It is a thin pass over Walk so there is a single traversal implementation.
func (fs *FS) Find(name string) []string {
	var matches []string
	// The visitor never errors, so Walk cannot fail here.
	_ = fs.Walk(func(path string, _ bool) error {
		if baseName(path) == name {
			matches = append(matches, path)
		}
		return nil
	})
	return matches
}

// FindRegex returns the absolute paths of every descendant of the current
// working directory whose name matches pattern (RE2 syntax, matched against the
// final path component). It returns the compilation error for an invalid
// pattern.
func (fs *FS) FindRegex(pattern string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var matches []string
	_ = fs.Walk(func(path string, _ bool) error {
		if re.MatchString(baseName(path)) {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, nil
}

// joinPath appends a single component to a directory path, treating the root
// "/" specially so the result is "/name" rather than "//name".
func joinPath(dirPath, name string) string {
	if dirPath == "/" {
		return "/" + name
	}
	return dirPath + "/" + name
}

// baseName returns the final component of an absolute path ("/a/b" -> "b").
func baseName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
