# In-Memory Filesystem — Design

Module `filesys`, package `memfs`. An in-memory filesystem: a tree of
directories and files exposing the familiar shell operations (`cd`, `pwd`,
`ls`, `mkdir`, `rm`, `mv`, `find`), plus two extensions — a **streaming file-I/O
layer** (the deep one) and a **subtree walk** (the light one). Nothing touches
the real disk.

The bar is production-quality, reviewable code: clear naming, explicit error
handling, and data structures that stay efficient under moderately high volume.

## Key decisions (and why)

- **Language: Go.** Explicit `error` returns force deliberate edge-case
  handling; `sync.RWMutex`/goroutines make the streaming extension a real
  showcase rather than a toy; `net/http` (no framework) would keep a future UI
  cheap. Verbosity is an acceptable cost for a readability-graded review.

- **Data structure: pointer tree.** Each directory holds its children in a
  `map[string]node` plus a `parent` pointer. Every core op lands at its natural
  complexity (`cd`/`mv` O(1), `pwd` O(depth), `ls` O(children), `find`
  O(subtree)).
  - A *flat path-keyed map* was rejected: a directory `mv` becomes an O(n) key
    rewrite of every descendant, and an open handle keyed by path can't survive
    the move — "path is identity" is incompatible with the streaming
    requirement.
  - A *full inode table* buys nothing over pointers **unless** we implement
    hardlinks (we don't): a `*Node` pointer already separates identity from
    name. Its only unique capability — N names → one identity — is exactly
    hardlinks.

- **Borrowed from the inode model: identity ≠ name.** A file's directory entry
  points to a shared `*fileContent` (bytes + its own lock). An open stream
  handle holds that `*fileContent` directly, so it **survives rename and
  removal** — Unix unlink-while-open semantics, for free via the GC.

- **Single, global CWD on the `FS`.** Matches the base spec literally and keeps
  the core API simple. The tree is still safe for concurrent use. Per-session /
  per-user CWD is a described future extension.

- **Removal split three ways**, mirroring Unix: `Remove` unlinks a single
  *file*; `RemoveDir` removes a directory *only when empty* (`ErrDirNotEmpty`
  otherwise); `RemoveAll` removes a directory and its whole subtree.

## Data model

All node types are unexported; the public surface is methods on `*FS` plus the
stream handles, `WalkFunc`/`SkipDir`, and the sentinel errors.

```go
// node is implemented by *directory and *file. An interface + concrete types
// (rather than one kind-tagged struct) keeps each type's fields honest — a file
// has no children; a directory has no content.
type node interface {
    name() string
    setName(string)
    parent() *directory
    setParent(*directory)
    isDir() bool
}

type directory struct {
    nodeName string
    par      *directory
    children map[string]node   // name -> file | directory
}

type file struct {
    nodeName string
    par      *directory
    content  *fileContent      // shared identity — handles bind here
}

type fileContent struct {
    mu         sync.RWMutex     // guards data + writerOpen for one file
    data       []byte
    writerOpen bool             // at most one active streaming writer
}

type FS struct {
    mu   sync.RWMutex           // guards tree structure + cwd
    root *directory
    cwd  *directory
}
```

`Pwd` walks `par` pointers to the root, collects names, reverses, and joins with
`/` (root renders as `"/"`). Identity living in the pointer (not the path) is
what makes `mv` an O(1) re-key and lets handles outlive a move.

## Concurrency model

Two independent lock levels, **never held simultaneously**:

1. `FS.mu` (RWMutex) — tree shape (`children` maps, `parent` pointers, `cwd`).
   Structural ops (`Mkdir`, `Remove*`, `Move`, `Cd`) take the write lock; reads
   (`Ls`, `Pwd`, resolution) take the read lock.
2. `fileContent.mu` (RWMutex, one per file) — that file's bytes and its
   single-writer flag.

**Invariant: resolve-then-release.** A content operation resolves the name to
its `*fileContent` under `FS.mu`, releases `FS.mu`, and only then takes the
content lock. The two are never nested, so there is no lock-ordering deadlock —
and a concurrent `Move` (which takes `FS.mu`) does **not** block an in-flight
`Read`. That is the mechanism behind "keep reading after a move."

**Stream handles** add a third, finest lock: a `sync.Mutex` per handle guarding
its own offset + closed flag, always acquired *before* the content lock (handle
→ content), so handles can never deadlock against each other.

**1 writer / N readers:** `fileContent.mu` allows concurrent reads; the
`writerOpen` flag (set under the lock at `OpenWriter`, cleared on `Close`)
enforces a single streaming writer. Individual `Write` calls hold the write lock
only for the byte-slice mutation, so readers aren't blocked for the writer's
lifetime.

`Ls`/`Find`/`ReadFile` return copies so callers hold no references into locked
state. `Walk` snapshots one directory's children at a time under a read lock and
invokes the visitor *outside* the lock — so a `WalkFunc` may safely call back
into the FS, and the walk has clean snapshot semantics.

## Public API & error cases

Sentinel errors (`errors.go`), wrapped with the offending name via
`fmt.Errorf("%s: %w", name, ErrX)` and comparable with `errors.Is`:

```
ErrNotFound, ErrExists, ErrNotDir, ErrIsDir, ErrDirNotEmpty,
ErrInvalidName, ErrWriterBusy, ErrInvalidSeek, ErrClosed
```

`validateName` rejects empty names, names containing `/`, and the reserved `.`
and `..`.

### Core — methods on `*FS`

| Method | Behaviour | Errors |
|---|---|---|
| `New() *FS` | tree with just `/`, cwd = root | — |
| `Cd(name string) error` | single child, or `..` (no-op at root) | `ErrNotFound`, `ErrNotDir` |
| `Pwd() string` | absolute path of cwd | — |
| `Mkdir(name string) error` | new child dir | `ErrInvalidName`, `ErrExists` |
| `Ls() []string` | child names of cwd, **sorted** (deterministic) | — |
| `Remove(name string) error` | unlink a single **file** | `ErrNotFound`, `ErrIsDir` |
| `RemoveDir(name string) error` | remove child dir **only if empty** | `ErrNotFound`, `ErrNotDir`, `ErrDirNotEmpty` |
| `RemoveAll(name string) error` | remove child dir + its subtree | `ErrNotFound`, `ErrNotDir` |
| `CreateFile(name string) error` | empty file in cwd | `ErrInvalidName`, `ErrExists` |
| `WriteFile(name string, data []byte) error` | replace contents (copies input) | `ErrNotFound`, `ErrIsDir` |
| `ReadFile(name string) ([]byte, error)` | copy of contents | `ErrNotFound`, `ErrIsDir` |
| `Move(oldName, newName string) error` | rename a **file** within cwd | `ErrNotFound`, `ErrIsDir`, `ErrExists`, `ErrInvalidName` |
| `Find(name string) []string` | absolute paths of all descendants named exactly `name` | — |

`Move` is rename-in-place per the base spec; cross-directory move/merge is a
future extension. `WriteFile`/`ReadFile` always take the per-file content lock,
so they stay memory-safe even while a streaming writer is open.

### Walk (light extension) — `walk.go`

```go
type WalkFunc func(path string, isDir bool) error
var SkipDir = errors.New("skip directory subtree")

func (fs *FS) Walk(fn WalkFunc) error                  // visits every descendant of cwd
func (fs *FS) FindRegex(pat string) ([]string, error)  // bad pattern -> compile error
```

`Walk` visits each descendant of the cwd depth-first, sorted within each
directory. Returning `SkipDir` from a directory prunes its subtree; any other
error aborts and propagates. Kept to exactly this contract. `Find` and
`FindRegex` are thin passes over `Walk` — one traversal implementation.

### Streaming (deep extension) — `stream.go`

```go
func (fs *FS) Open(name string) (*FileReader, error)        // N readers allowed
func (fs *FS) OpenWriter(name string) (*FileWriter, error)  // ErrWriterBusy if one is open

// *FileReader: io.Reader, io.ReaderAt, io.Seeker, io.Closer
// *FileWriter: io.Writer, io.WriterAt, io.Seeker, io.Closer  (Close frees the writer slot)
```

Each handle holds a `*fileContent` + its own offset + a `closed` flag.
`Open`/`OpenWriter` errors: `ErrNotFound`, `ErrIsDir`, plus `ErrWriterBusy` for
`OpenWriter`. After `Close`, any `Read`/`Write`/`Seek`/`ReadAt`/`WriteAt`
returns `ErrClosed` (`Close` is idempotent). `Seek`/`ReadAt`/`WriteAt` validate
offsets (`ErrInvalidSeek`); reads past end return `io.EOF`; `Write`/`WriteAt`
past end extend the buffer (zero-filling any gap). `OpenWriter` starts at offset
0 and does not truncate. Handles ignore renames/removals by design.

## File layout

```
go.mod                 // module filesys
errors.go              // sentinels, wrap, validateName
node.go                // node interface, directory, file, fileContent, absPath
fs.go                  // FS, New, Cd, Pwd, Ls, file resolution
dir.go                 // Mkdir, RemoveDir, RemoveAll
file.go                // CreateFile, WriteFile, ReadFile, Remove, Move
walk.go                // Walk, SkipDir, Find, FindRegex
stream.go              // Open, OpenWriter, FileReader, FileWriter
*_test.go              // one test file per source file
example_test.go        // ExampleFS mirroring the spec walkthrough (godoc + check)
README.md              // run/test, tradeoffs, 2+ future extensions   (deliverable)
TRANSCRIPT.md          // AI transcript                                (deliverable)
```

## Test strategy

- **Table-driven unit tests** per operation, asserting both success and each
  error path with `errors.Is`.
- **`ExampleFS`** reproduces the prompt's exact walkthrough (school/homework/…)
  and is verified by `go test` via its `// Output:` block.
- **Walk:** visits all descendants in order; `SkipDir` prunes a subtree
  (asserts pruned entries absent); error propagation; `Find` recursion + cwd
  scoping; `FindRegex` match + bad-pattern error.
- **Streaming functional:** write→read, chunked reads, `ReadAt`/`WriteAt`/`Seek`
  random access, read-past-end `io.EOF`, write-past-end extension, `ErrClosed`
  after close, single-writer (`ErrWriterBusy`).
- **Streaming survival:** open a reader, then `Move` / `Remove` / `RemoveAll`
  the parent — keep reading the original bytes.
- **Concurrency:** 1 writer + 8 readers hammering one file; run under
  `go test -race` (`-count=20` for stress).

Status: 33 tests pass, clean under `-race`, `go vet` and `gofmt` clean.

## Future extensions (described in README — NOT built)

1. **Permissions & multiple users** — a `Session`/`User` holding identity; CWD
   moves onto the session; `mode`/`uid`/`gid` on nodes; permission checks in the
   resolve path; groups.
2. **Move & copy with merge + path operations** — a path resolver that splits on
   `/`, handles `..`/absolute paths and optional intermediate-dir creation;
   recursive copy; directory-merge with a collision policy.
3. **In-browser explorer** — `net/http` handlers wrapping the library; the
   browser holds no state and calls read/mutate endpoints.

## How to run

```
go test ./...            # unit + example tests
go test -race ./...      # race detector
go vet ./...
```
