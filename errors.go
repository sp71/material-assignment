package memfs

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned by the filesystem. Callers compare against these
// with errors.Is; operations wrap them with the offending name for context
// (e.g. fmt.Errorf("%s: %w", name, ErrNotFound)).
var (
	ErrNotFound    = errors.New("no such file or directory")
	ErrExists      = errors.New("file or directory already exists")
	ErrNotDir      = errors.New("not a directory")
	ErrIsDir       = errors.New("is a directory")
	ErrDirNotEmpty = errors.New("directory not empty")
	ErrInvalidName = errors.New("invalid name")
	ErrWriterBusy  = errors.New("file already has an active writer")
	ErrInvalidSeek = errors.New("invalid seek offset")
	ErrClosed      = errors.New("handle is closed")
)

// wrap annotates a sentinel error with the name that triggered it while keeping
// the sentinel reachable through errors.Is/errors.Unwrap. Example result:
// "lunch: no such file or directory".
func wrap(name string, err error) error {
	return fmt.Errorf("%s: %w", name, err)
}

// validateName rejects names that cannot be a single path component. A valid
// name is non-empty, contains no '/' separator, and is not one of the reserved
// navigation names "." or "..". Callers that need parent traversal (e.g. Cd)
// handle ".." explicitly before validating.
func validateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return ErrInvalidName
	}
	return nil
}
