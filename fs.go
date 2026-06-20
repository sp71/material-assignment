// Package memfs implements an in-memory filesystem: a tree of directories and
// files that supports the familiar shell operations (cd, pwd, ls, mkdir, rm,
// mv, find) plus a streaming file-I/O layer and a subtree walk.
//
// Nothing touches the real disk. The tree is a pointer graph: each directory
// holds its children in a map keyed by name and a pointer back to its parent,
// so navigation and rename are O(1) and pwd is O(depth).
//
// Concurrency: an *FS is safe for use by multiple goroutines. Tree structure is
// guarded by FS.mu; each file's bytes are guarded by a per-file lock reached
// only after FS.mu is released (see fileContent). Stream handles bind to a
// file's content rather than its path, so reads and writes continue across a
// rename or removal.
package memfs

import (
	"sort"
	"sync"
)

// FS is an in-memory filesystem with a single current working directory. The
// zero value is not usable; construct one with New.
type FS struct {
	mu   sync.RWMutex
	root *directory
	cwd  *directory
}

// New returns a filesystem containing only the root directory "/", with the
// current working directory set to the root.
func New() *FS {
	root := newDir("", nil) // root has an empty name and no parent
	return &FS{root: root, cwd: root}
}

// Pwd returns the absolute path of the current working directory, e.g.
// "/school/homework". The root is "/".
func (fs *FS) Pwd() string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return absPath(fs.cwd)
}

// Cd changes the current working directory. name must be a single child
// directory name or "..", which moves to the parent (a no-op at the root, like
// a shell). Returns ErrNotFound if no such child exists and ErrNotDir if the
// child is a file.
func (fs *FS) Cd(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if name == ".." {
		if fs.cwd.par != nil {
			fs.cwd = fs.cwd.par
		}
		return nil
	}

	child, ok := fs.cwd.children[name]
	if !ok {
		return wrap(name, ErrNotFound)
	}
	dir, ok := child.(*directory)
	if !ok {
		return wrap(name, ErrNotDir)
	}
	fs.cwd = dir
	return nil
}

// Ls returns the names of the current working directory's children, sorted for
// deterministic output (matching the behaviour callers expect from `ls`). The
// returned slice is a fresh copy and safe to retain or mutate.
func (fs *FS) Ls() []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	names := make([]string, 0, len(fs.cwd.children))
	for name := range fs.cwd.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookupFile resolves name to a *file within the current working directory. It
// must be called with at least FS.mu held for reading. It is the shared
// resolution step for the file content operations.
func (fs *FS) lookupFile(name string) (*file, error) {
	if err := validateName(name); err != nil {
		return nil, wrap(name, err)
	}
	child, ok := fs.cwd.children[name]
	if !ok {
		return nil, wrap(name, ErrNotFound)
	}
	f, ok := child.(*file)
	if !ok {
		return nil, wrap(name, ErrIsDir)
	}
	return f, nil
}
