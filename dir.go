package memfs

// Mkdir creates a new empty directory as a child of the current working
// directory. Returns ErrInvalidName for an unusable name and ErrExists if a
// file or directory of that name already exists.
func (fs *FS) Mkdir(name string) error {
	if err := validateName(name); err != nil {
		return wrap(name, err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, exists := fs.cwd.children[name]; exists {
		return wrap(name, ErrExists)
	}
	fs.cwd.children[name] = newDir(name, fs.cwd)
	return nil
}

// RemoveDir removes a child directory of the current working directory, but only
// when it is empty. Returns ErrNotFound if there is no such child, ErrNotDir if
// the child is a file, and ErrDirNotEmpty if the directory still has children.
// Use RemoveAll to remove a non-empty directory and its subtree.
func (fs *FS) RemoveDir(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir, err := fs.cwd.lookupChild(name)
	if err != nil {
		return err
	}
	if len(dir.children) > 0 {
		return wrap(name, ErrDirNotEmpty)
	}
	delete(fs.cwd.children, name)
	return nil
}

// RemoveAll removes a child directory of the current working directory together
// with its entire subtree. Returns ErrNotFound if there is no such child and
// ErrNotDir if the child is a file.
//
// Detaching the subtree's root is all that is required: the descendant nodes
// become unreachable and are reclaimed by the garbage collector — except for
// any file whose content is still held by an open stream handle, which stays
// alive until that handle closes (Unix unlink-while-open semantics).
func (fs *FS) RemoveAll(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, err := fs.cwd.lookupChild(name); err != nil {
		return err
	}
	delete(fs.cwd.children, name)
	return nil
}
