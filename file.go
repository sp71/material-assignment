package memfs

// CreateFile creates a new empty file in the current working directory. Returns
// ErrInvalidName for an unusable name and ErrExists if a file or directory of
// that name already exists.
func (fs *FS) CreateFile(name string) error {
	if err := validateName(name); err != nil {
		return wrap(name, err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, exists := fs.cwd.children[name]; exists {
		return wrap(name, ErrExists)
	}
	fs.cwd.children[name] = newFile(name, fs.cwd)
	return nil
}

// WriteFile replaces the contents of a file in the current working directory.
// Returns ErrNotFound if there is no such file and ErrIsDir if the name refers
// to a directory.
//
// It resolves the file under FS.mu, releases it, then takes the per-file
// content lock for the copy — so it is memory-safe even while a streaming
// writer is open on the same file (the two serialize on the content lock).
func (fs *FS) WriteFile(name string, data []byte) error {
	f, err := fs.resolveFile(name)
	if err != nil {
		return err
	}

	c := f.content
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy defensively so the caller cannot mutate stored bytes after the call.
	c.data = append([]byte(nil), data...)
	return nil
}

// ReadFile returns a copy of a file's contents from the current working
// directory. Returns ErrNotFound if there is no such file and ErrIsDir if the
// name refers to a directory. The returned slice is a copy and safe to retain.
func (fs *FS) ReadFile(name string) ([]byte, error) {
	f, err := fs.resolveFile(name)
	if err != nil {
		return nil, err
	}

	c := f.content
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]byte(nil), c.data...), nil
}

// Remove unlinks a single file from the current working directory. Returns
// ErrNotFound if there is no such entry and ErrIsDir if the name refers to a
// directory (use RemoveDir/RemoveAll for those). As with RemoveAll, an open
// stream handle keeps the unlinked file's content alive until it closes.
func (fs *FS) Remove(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	child, ok := fs.cwd.children[name]
	if !ok {
		return wrap(name, ErrNotFound)
	}
	if child.isDir() {
		return wrap(name, ErrIsDir)
	}
	delete(fs.cwd.children, name)
	return nil
}

// Move renames a file within the current working directory. Per the base spec
// it operates on files only and keeps them in the same directory; cross-
// directory moves and merging are a planned future extension.
//
// Returns ErrNotFound if oldName does not exist, ErrIsDir if it is a directory,
// ErrInvalidName if newName is unusable, and ErrExists if newName is already
// taken. Because identity lives in the file node (not its path), the rename is
// an O(1) re-key and any open stream handle is unaffected.
func (fs *FS) Move(oldName, newName string) error {
	if err := validateName(newName); err != nil {
		return wrap(newName, err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	child, ok := fs.cwd.children[oldName]
	if !ok {
		return wrap(oldName, ErrNotFound)
	}
	if child.isDir() {
		return wrap(oldName, ErrIsDir)
	}
	if oldName == newName {
		return nil
	}
	if _, exists := fs.cwd.children[newName]; exists {
		return wrap(newName, ErrExists)
	}

	delete(fs.cwd.children, oldName)
	child.setName(newName)
	fs.cwd.children[newName] = child
	return nil
}
