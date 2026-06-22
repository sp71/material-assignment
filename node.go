package memfs

import (
	"strings"
	"sync"
)

// node is the common behaviour shared by the two entry kinds that can live in a
// directory: *directory and *file. Keeping it unexported means the tree shape
// is an implementation detail — callers only ever see the *FS API.
//
// We model "file or directory" with an interface + concrete types rather than a
// single kind-tagged struct. Go has no sum types, and the interface keeps each
// type's fields honest (a file has no children; a directory has no content).
type node interface {
	name() string
	setName(string)
	parent() *directory
	// isDir reports whether the node is a directory, so traversal code can
	// branch without a type switch at every call site.
	isDir() bool
}

// directory is an interior node of the tree. children maps a single-component
// name to the child node; par points at the enclosing directory (nil only for
// the root, which is its own conceptual parent).
type directory struct {
	nodeName string
	par      *directory
	children map[string]node
}

func newDir(name string, parent *directory) *directory {
	return &directory{
		nodeName: name,
		par:      parent,
		children: make(map[string]node),
	}
}

func (d *directory) name() string       { return d.nodeName }
func (d *directory) setName(n string)   { d.nodeName = n }
func (d *directory) parent() *directory { return d.par }
func (d *directory) isDir() bool        { return true }

// file is a leaf node. Its directory entry holds only naming/placement; the
// bytes live in a separately-allocated fileContent that the entry points at.
// This split is the crux of the streaming design: an open handle binds to the
// content, not to the entry, so it survives rename and removal.
type file struct {
	nodeName string
	par      *directory
	content  *fileContent
}

func newFile(name string, parent *directory) *file {
	return &file{
		nodeName: name,
		par:      parent,
		content:  &fileContent{},
	}
}

func (f *file) name() string       { return f.nodeName }
func (f *file) setName(n string)   { f.nodeName = n }
func (f *file) parent() *directory { return f.par }
func (f *file) isDir() bool        { return false }

// fileContent is the shared, lockable identity of a file's bytes. One exists
// per file; open stream handles keep a pointer to it. Its mutex guards both the
// byte slice and the single-writer flag.
//
// Locking note: this lock is independent of FS.mu. Content operations resolve a
// name to its *fileContent under FS.mu, release FS.mu, then take this lock — the
// two are never held at once, which both avoids deadlock and lets a concurrent
// Move (FS.mu) proceed without blocking an in-flight Read.
type fileContent struct {
	mu         sync.RWMutex
	data       []byte
	writerOpen bool
}

// absPath walks parent pointers from n up to the root and renders the absolute
// path. The root renders as "/"; every other node renders as "/a/b/c".
func absPath(n node) string {
	// Collect component names from the node up to (but not including) the root.
	var parts []string
	for cur := n; cur != nil && cur.parent() != nil; cur = cur.parent() {
		parts = append(parts, cur.name())
	}
	if len(parts) == 0 {
		return "/"
	}
	// parts is leaf-to-root; reverse into root-to-leaf order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}
