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

// walkEntry is an immutable snapshot of one directory entry. The name and kind
// are captured under the filesystem lock; the visitor then runs against the
// snapshot. We must snapshot the name — not just the node pointer — because a
// concurrent Move mutates a node's name field, so reading name() after the lock
// is released would be a data race (verified by TestWalkConcurrentWithRename).
type walkEntry struct {
	node  node
	name  string
	isDir bool
}

func (fs *FS) walkDir(dir *directory, dirPath string, fn WalkFunc) error {
	for _, e := range fs.snapshotChildren(dir) {
		childPath := joinPath(dirPath, e.name)

		err := fn(childPath, e.isDir)
		if errors.Is(err, SkipDir) {
			continue // prune: do not descend into this entry
		}
		if err != nil {
			return err
		}

		if sub, ok := e.node.(*directory); ok {
			if err := fs.walkDir(sub, childPath, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshotChildren returns dir's children as immutable entries sorted by name,
// captured under a brief read lock so the visitor can run without holding it.
func (fs *FS) snapshotChildren(dir *directory) []walkEntry {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	entries := make([]walkEntry, 0, len(dir.children))
	for _, child := range dir.children {
		entries = append(entries, walkEntry{
			node:  child,
			name:  child.name(),
			isDir: child.isDir(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries
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
