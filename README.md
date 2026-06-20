# memfs — an in-memory filesystem

A small, production-shaped Go library implementing a filesystem entirely in
memory: a tree of directories and files exposing the familiar shell operations
(`cd`, `pwd`, `ls`, `mkdir`, `rm`, `mv`, `find`), plus two extensions — a
**streaming file-I/O layer** (the deep one) and a **subtree walk**. Nothing
touches the real disk.

The full design rationale lives in [`docs/PLAN.md`](docs/PLAN.md); this README
is the quick start, the headline tradeoffs, and the future-extension sketches.

## Running and testing

Requires Go 1.26+. Dependencies are vendored, so it builds offline.

```sh
# Run the test suite
go test ./...

# Run it under the race detector (the streaming + walk concurrency tests
# are designed to exercise this)
go test -race ./...

# Static checks
go vet ./...
gofmt -l .        # prints nothing when clean

# Try it interactively: a tiny shell over the library
go run ./cmd/memfsh   # type 'help' for commands, 'exit' to quit
```

### Using it as a library

```go
fs := memfs.New() // a fresh filesystem with just "/", cwd = "/"

fs.Mkdir("school")
fs.Cd("school")
fmt.Println(fs.Pwd()) // /school

fs.CreateFile("notes.txt")
fs.WriteFile("notes.txt", []byte("hello"))
data, _ := fs.ReadFile("notes.txt") // []byte("hello")

// Streaming: many readers, at most one writer; handles survive rename/removal.
w, _ := fs.OpenWriter("notes.txt")
w.WriteAt([]byte("world"), 6)
w.Close()

r, _ := fs.Open("notes.txt")
fs.Move("notes.txt", "renamed.txt") // r keeps working
io.ReadAll(r)
```

Every error is a wrapped sentinel (`memfs.ErrNotFound`, `ErrIsDir`,
`ErrWriterBusy`, …), so callers branch with `errors.Is`.

## What's implemented

| Area | Operations |
|---|---|
| Navigation | `Cd` (child, `.`, `..`), `Pwd`, `Ls` (sorted) |
| Directories | `Mkdir`, `RemoveDir` (empty only), `RemoveAll` (recursive) |
| Files | `CreateFile`, `WriteFile`, `ReadFile`, `Remove`, `Move` (rename in place) |
| Find / walk | `Find` (exact name, recursive), `FindRegex`, `Walk` (visitor + `SkipDir`) |
| Streaming | `Open`/`OpenWriter` → `FileReader`/`FileWriter` implementing `io.Reader`/`Writer`/`ReaderAt`/`WriterAt`/`Seeker`/`Closer` |

The filesystem is safe for concurrent use by multiple goroutines.

## Key design tradeoffs

**Pointer tree, not a path-keyed map or an inode table.** Each directory holds
its children in a `map[string]node` plus a `parent` pointer. This gives every
operation its natural cost (`cd`/`mv` O(1), `pwd` O(depth), `ls` O(children),
`find` O(subtree)). A flat map keyed by full path was rejected because renaming
a directory would be an O(n) rewrite of every descendant's key and couldn't
survive a move on an open handle. A full Unix-style inode table buys nothing
over pointers *unless* we implement hardlinks — a `*node` pointer already
separates identity from name — so we didn't pay for it.

**Identity ≠ name: a file's bytes live behind a shared `*fileContent`.** The
directory entry holds placement (name, parent); the bytes and their lock live in
a separately-allocated `fileContent` the entry points at. An open stream handle
binds to that content, *not* to the path — so reads and writes **continue across
a rename or even a removal** (Unix unlink-while-open semantics, which we get for
free from the garbage collector). This is the single decision that makes the
streaming extension fall out cleanly, and it leaves file hardlinks as a cheap
future step (two entries → one `fileContent`) without an inode rewrite.

**Two lock levels, never nested.** `FS.mu` guards the tree shape; a per-file
`RWMutex` guards each file's bytes. Content operations resolve a name to its
`*fileContent` under `FS.mu`, *release it*, then take the content lock
("resolve-then-release", see `FS.resolveFile`). The payoff: a concurrent `Move`
(which takes `FS.mu`) never blocks an in-flight `Read`. The cost: `FS.mu` is a
single coarse lock for all structural mutations — fine here, but the first thing
to shard if structural write throughput ever became a bottleneck.

**`Walk` snapshots names under the lock, runs the visitor outside it.** The
visitor is arbitrary caller code, so holding a lock across it would invite
re-entrant deadlocks. `Walk` instead copies one directory's entries —
*including their names*, not just node pointers — under a brief read lock, then
calls the visitor. Capturing the name matters: `Move` mutates a node's name
field, so reading it after releasing the lock would be a data race (there's a
regression test, `TestWalkConcurrentWithRename`, that fails under `-race`
without this). The tradeoff is snapshot semantics: a concurrent mutation may or
may not be observed by an in-progress walk, but the walk never crashes.

**Single, global current working directory.** The base spec is written around
one CWD, so the `FS` owns it directly — simplest API, faithful to the prompt.
The tradeoff is that genuine multi-user support needs the CWD (and later, user
identity) to move onto a per-session handle; that's the first future extension
below.

**Three explicit removal verbs.** `Remove` (a single file), `RemoveDir` (a
directory, only if empty → `ErrDirNotEmpty`), and `RemoveAll` (a directory and
its subtree). This mirrors Unix `rm`/`rmdir`/`rm -r` and forces the caller to be
explicit about recursive deletion rather than hiding it behind one ambiguous
call.

**Geometric buffer growth for writes.** `fileContent.writeAt` doubles capacity
when it must grow, so a stream of appends is amortized O(1) per byte instead of
the O(n²) you'd get from reallocating the whole buffer on every write. Spare
capacity is provably zero (writes only touch bytes below the logical length), so
gaps from writing past the end read back as zeros with no extra zero-fill pass.

## Possible future extensions

These are **not implemented** — they're the directions the design was kept open
for, with the changes each would need.

### 1. Permissions and multiple users

*Why:* it's the natural next step once the single-CWD simplification is the main
limitation, and the current "identity vs name" split and resolve-then-release
locking give it a clean home. It also turns the filesystem from a single-tenant
data structure into something that models real access control.

*Changes I'd expect:*
- Introduce a `Session` (or `Shell`) type that owns the current working
  directory and the acting user; the global `FS.cwd` moves onto it, so each user
  gets an independent CWD. Core methods become `(*Session).Cd/Mkdir/...`, with
  `FS` reduced to the shared tree + lock.
- Add `mode` (rwx bits), `uid`, and `gid` fields to `directory`/`file`. A `User`
  has a primary group plus supplementary groups.
- Add a permission check in the resolution path (`resolveFile` and an analogous
  directory resolver) that consults the session's user against the target's
  mode/owner/group, returning a new `ErrPermission` sentinel.
- User/group management (`AddUser`, `AddToGroup`, `SwitchUser`) and an
  ownership-aware `chmod`/`chown`.

### 2. Move/copy with merge, plus absolute and relative paths

*Why:* the base `Move` is deliberately rename-in-place; lifting that restriction
is the most-requested real capability and exercises genuinely interesting
algorithms (recursive copy, directory merge, collision policy).

*Changes I'd expect:*
- A path resolver that splits a string on `/`, walks components (handling `..`
  and a leading `/` for absolute paths), and optionally creates missing
  intermediate directories. Every name-taking method would accept a path, not
  just a single component.
- Generalize `Move` to cross-directory moves: detach the node from its source
  parent, reattach under the destination, fix the `parent` pointer. Because
  identity lives in the node, this stays O(1) for the node itself.
- Add `Copy`, which for a file shares or clones the `fileContent` and for a
  directory recurses (reusing `Walk`).
- A collision policy when a target name exists: error, auto-rename
  (`name (2)`), or merge directories recursively — surfaced as an option so the
  caller chooses.

### 3. In-browser file explorer

*Why:* it demonstrates the library is genuinely API-first — the same operations,
driven over HTTP instead of in-process — and pairs naturally with the streaming
layer for upload/download.

*Changes I'd expect:*
- A thin `net/http` layer (no framework) wrapping the library: `GET` endpoints
  for `ls`/`read`, `POST`/`DELETE` for mutations, streaming bodies wired to
  `FileReader`/`FileWriter`.
- Per-connection session state (building on extension 1) so each browser tab has
  its own CWD/user.
- A static single-page frontend that holds no filesystem state and renders
  whatever the API returns.

## Project layout

```
fs.go        FS type, New, Cd, Pwd, Ls, file resolution
node.go      node interface, directory, file, fileContent, path helpers
errors.go    sentinel errors, SkipDir, validateName
dir.go       Mkdir, RemoveDir, RemoveAll
file.go      CreateFile, WriteFile, ReadFile, Remove, Move
walk.go      Walk, Find, FindRegex
stream.go    Open/OpenWriter, FileReader, FileWriter, buffer growth
*_test.go    one test file per source file, all in package memfs
cmd/memfsh/  an interactive shell over the library
docs/        PLAN.md (full design), requirements.txt (the assignment)
```
