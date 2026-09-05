# recycler

A Go library and CLI that expose the operating system's recycle bin through one
uniform API on Linux (and the BSDs), macOS and Windows.

## Layout

| Path | What lives there |
|---|---|
| `recycler.go` | Public API: `Item`, the errors, `Recycle`/`List`/`Get`/`Restore`/`RestoreTo`, and the unexported `backend` interface every platform implements. |
| `fsutil.go` | Platform-neutral filesystem helpers: `move` (rename with a copy fallback), `copyTree`, `treeSize`, `uniqueName`, `sortItems`, `prepareDest`. |
| `fsutil_unix.go` | `deviceOf`, `topDirOf`, `isStickyDir` - shared by the FreeDesktop and macOS backends. |
| `errno_{unix,windows,other}.go` | `errCrossDevice`, the errno a rename fails with across filesystems. |
| `trash_freedesktop.go`, `mounts_freedesktop.go` | Linux/BSD backend and its mount table scanning. |
| `trash_darwin.go` | macOS backend. |
| `putback.go` | Finder's `ptbL`/`ptbN` put back records: reading them out of a trash directory's `.DS_Store` and editing them back in under a lock. Built on every Unix so the test suite covers it on Linux. |
| `dsstore.go`, `dsstore_write.go` | Codec for the `.DS_Store` files those records live in: the B-tree parser, and the writer with its buddy allocator. Deliberately **not** build-constrained, so it is testable on any platform. |
| `trash_windows.go`, `shfileop_windows*.go` | Windows backend, the `SHFILEOPSTRUCTW` declaration, and the guard that fails a 32-bit Windows build. |
| `recyclebin_meta.go` | Codec for the Windows `$I` metadata files. Deliberately **not** build-constrained, so it is testable on any platform. |
| `trash_unsupported.go` | Every other GOOS: all operations fail with `ErrUnsupported`. |
| `daemon.go` | The disk-pressure daemon: `FreeTarget`, `Sweep` and the poll loop. The only thing here that destroys anything. |
| `daemon_spawn.go`, `daemonlock_*.go`, `detach_*.go` | Starting one daemon per user: the lock that makes it one, and the per-platform detach. |
| `diskfree_{unix,windows,unsupported}.go` | `diskFree`, what the daemon polls - `statfs` and `GetDiskFreeSpaceEx`, the same numbers `df` reports. |
| `cmd/recycler/` | Cobra CLI, one command per file, each registering itself in `init()`. |
| `cmd/recycler/tui.go`, `cmd/recycler/ui/` | The full-screen browser: its Bubble Tea model, and the TML document and theme it renders. |

`platformBackend()` is defined once per platform and is called on **every** API
call rather than cached, so tests can redirect the recycle bin with `HOME` and
`XDG_DATA_HOME` between calls. Keep it that way.

## Invariants

- **Nothing a user can ask for deletes permanently.** No command destroys an
  entry: an item leaves the bin by being restored, and emptying it belongs to
  the desktop environment. Do not add a purge or empty command, a `--force`
  that removes an entry, or a second backend method that unlinks anything.
  `TestNoDestructiveCommands` locks the CLI down, and
  `TestBackendDestroysOnlyUnderDiskPressure` pins the interface to exactly one
  destructive method.
- **That method is `evict`, and only disk pressure calls it.** Recycling defers
  a deletion rather than performing one, which holds only while there is room to
  defer into: a bin nobody empties fills the filesystem, and the recycled copy
  is what holds the space. `daemon.go` gives back the oldest items when a
  filesystem drops under `FreeTarget` - a tenth of itself, capped at 1 GiB.
  Pressure decides what goes, never a user typing a command.
- **A recycled item's size is measured once, when it is recycled.** What sits
  in the bin does not change, so `recycle` records the size and every later
  reader takes that number. The daemon polls, so a `list` that walked each
  item's tree would walk the whole bin every 30 seconds. An entry another
  implementation wrote records no size: it is measured on first sight and the
  number written back, so that walk happens once too. An item whose size is
  still unknown is never evicted, because it cannot be accounted for.
- **An `Item.ID` is a path inside a recycle bin directory, and is validated
  before use.** Every backend's `resolveID` checks that the ID names an existing
  entry inside one of *this user's* trash directories before restoring
  anything. A malformed or hostile ID must never reach a file outside
  the bin; `TestUnknownIDsAreRejected` locks that down.
- **Recycling is a rename, never a copy.** Each backend picks a trash directory
  on the same filesystem as the file, which is what per-filesystem trash
  directories exist for. `move` still falls back to copy-and-delete, which
  matters for restoring to a different filesystem.
- **Nothing is overwritten.** `move` and `prepareDest` fail with `ErrExists`
  rather than clobber a file that took the original location back.
- **Listing is best-effort per location.** An unreadable trash directory (a
  volume mounted by another user, say) is skipped, not fatal - one bad location
  must not hide the rest of the bin.

## The browser

`recycler` with no arguments on a terminal opens the bin on a full screen, and
`recycler tui` opens it anywhere. Routing lives in `execute` in `main.go` rather
than on the root command, which leaves cobra's handling of an unknown command
intact; redirected, the bare invocation prints the help a script asked for.

The view is [TML](https://github.com/wow-look-at-my/tml): `ui/app.tml` declares
it, the model in `tui.go` holds every piece of state, and TML draws what the
properties say. Three things there are load-bearing:

- **The table's rows are `\x1f`-delimited cells.** Every printable delimiter is
  legal in a file name, so a path holding one would arrive as an extra column.
- **`wrap="false"` keeps a row to a line**, which is what lets a click's `y` and
  the scroll offset name a row. A wrapped cell puts every row below it out of
  step with its index.
- **The name leads and the directory trails**, because a cut falls on the end of
  the row: the line below spells the path out in full, and the name is the part
  that has to survive a narrow terminal.

The selected item costs the row it is listed on, not a panel: the table draws a
bar across that row, and what sits under the listing is its full original path.

Restoring is the only thing the browser can do to an item, and it asks first.
`TestTheInterfaceOffersNoWayToDeleteAnything` holds every key against a full bin
and fails if any of them removes anything.

## Platform notes

### Linux and the BSDs - FreeDesktop trash specification v1.0

Home trash is `$XDG_DATA_HOME/Trash` (default `~/.local/share/Trash`) with
`files/` and `info/` subdirectories. Files on another filesystem go to
`$topdir/.Trash/$uid` when that directory exists with the sticky bit set, and
otherwise to `$topdir/.Trash-$uid`.

Each entry is a pair: `files/<name>` holds the data, `info/<name>.trashinfo` the
metadata, in the spec's format (`[Trash Info]`, a percent-encoded `Path`, and a
local-time `DeletionDate`). The `.trashinfo` file is created with `O_EXCL`,
which is what atomically claims a free name. Paths recorded in a per-filesystem
trash are relative to the top directory, so the entry survives a remount
elsewhere.

Mount points come from `/proc/self/mounts` where it exists, plus globs of the
usual removable-media directories. Entries written by other trash
implementations are read and restored normally - `TestReadsForeignTrashEntries`
covers that.

### macOS

Files go to `~/.Trash`, or `<volume>/.Trashes/$uid` for other volumes.

**Original locations come from Finder's own "Put Back" records; this package
keeps no index of its own.** macOS has no API for restoring - `trashItem` and
`NSWorkspace.recycle` only put things in - and the only record of where a
trashed item came from is a pair of records in that trash directory's
`.DS_Store`, keyed by the item's name inside the trash:

| Record | Type | Holds |
|---|---|---|
| `ptbL` | `ustr` | the original parent directory, relative to the volume root (`Users/ada/Documents`) |
| `ptbN` | `ustr` | the original name, which differs from the name in the trash when something was already called that |

`putback.go` reads and writes exactly those, so an item recycled here can be put
back from Finder, and an item Finder trashed restores here. The whole `.DS_Store`
is rewritten under an exclusive `flock`, preserving every record the file
already had - the icon positions and window settings next to the put back
records belong to Finder, and are carried through untouched. A `.DS_Store` that
cannot be parsed is replaced rather than allowed to block recycling: display
settings are worth less than a recorded location. Nothing can lock Finder out of
the same file, and Apple's own APIs lose put back records to that race too.

An item whose records are missing - trashed by a tool that writes none, or a
`.DS_Store` deleted by one of the many programs that delete them - is listed with
an empty `OriginalPath` and needs an explicit destination (`RestoreTo`).

Two smaller consequences of having no index:

- `DeletedAt` is the item's inode change time, which the rename into the trash
  updates. macOS records no deletion time anywhere.
- Finder writes locations through the data volume's own mount point
  (`/System/Volumes/Data/Users/ada/notes.txt`). `dataVolumePath` presents those
  as the firmlinked `/Users/ada/notes.txt` they resolve to, but only when that
  path's directory exists, so an unusual layout keeps what was recorded.

### Windows

Recycling goes through the shell - `SHFileOperationW` with `FOF_ALLOWUNDO` - so
it behaves exactly like deleting from Explorer. That includes the shell's own
failure mode: if the volume has no usable recycle bin, the file is deleted
permanently. This is documented on `Recycle`; do not paper over it by inventing
a pre-check that cannot be made reliable.

Listing and restoring read `<drive>:\$Recycle.Bin\<user SID>\`
directly. Each item is a `$R<id><ext>` data file with a `$I<id><ext>` metadata
file; `recyclebin_meta.go` decodes both metadata versions (1 on Vista..8.1 with
a fixed 260-character path, 2 on Windows 10+ with a length-prefixed one).

Only **64-bit Windows** is supported. `SHFILEOPSTRUCTW` is byte-packed on 32-bit
Windows (`shellapi.h` wraps it in `pshpack1.h` under `#ifndef _WIN64`), so the
natural Go layout in `shfileop_windows.go` is right for amd64/arm64 and wrong
for 386/arm. `shfileop_windows_unsupported.go` therefore fails a 32-bit build on
purpose, rather than letting the shell read a delete request from the wrong
offsets.

## Building and testing

Run `go-toolchain` in the repository root - never bare `go` commands. It tidies,
vets, formats, runs the tests with a coverage floor of 80%, and builds.

**It only compiles the host's files.** A type error in `trash_windows.go` or
`trash_darwin.go` passes a full local `go-toolchain` run with "Build
successful". The `cross-compile` job in `.github/workflows/ci.yml` is what
catches those, by building every GOOS in this package's constraints. Run the
same loop after touching a per-platform file rather than waiting for CI.

**Do not build this project as a cosmo fat APE.** gosmopolitan compiles with
`GOOS=cosmo`, which matches none of the backends' build constraints, so the APE
falls through to `trash_unsupported.go` and every operation fails with
`ErrUnsupported` - verified by building one and running it. That APE is the only
native output `go-toolchain matrix` still emits, which is why CI passes
`autorelease: false`: this package has nothing it can honestly publish through
that path. Recycling is not portable-libc work: it is three unrelated
mechanisms, one of which is only reachable through the Windows shell API.

The test suite runs against a real recycle bin redirected into a temporary
directory (`isolateTrash`), so it exercises the actual FreeDesktop
implementation rather than a mock. Windows and macOS specific behaviour that
cannot run on Linux is covered where possible by platform-neutral unit tests -
that is why the `$I` codec has no build constraint, and why `putback.go` is
built on every Unix rather than only on darwin.

The `.DS_Store` fixtures in `testdata/` were written by an independent
implementation of the format (the `ds_store` Python package behind `dmgbuild`),
which is what keeps the codec honest: `finder_trash.DS_Store` is a single-node
file, `finder_trash_many.DS_Store` a tree with internal nodes, and
`edited_elsewhere.DS_Store` a file this package wrote that was then edited by
that other implementation - the case that comes up every time Finder touches a
trash directory this package has written to. Regenerate them with that package
if the fixtures ever need to change; do not hand-edit them.

CI (`.github/workflows/ci.yml`) runs the same toolchain and builds every
supported target, so a per-platform file that stops compiling fails the build.
The `cross-compile` job builds the library for every GOOS in this package's
constraints, and `./...` for the subset the CLI can run on: the browser needs a
terminal, and Bubble Tea supports neither js/wasm nor solaris. No job may be named `all-builds`: that status is
posted by the org's required-builds-manager app, and the go-toolchain action
fails any workflow that shadows it.
