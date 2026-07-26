# recycler

A Go library and CLI that expose the operating system's recycle bin through one
uniform API on Linux (and the BSDs), macOS and Windows.

## Layout

| Path | What lives there |
|---|---|
| `recycler.go` | Public API: `Item`, the errors, `Recycle`/`List`/`Get`/`Restore`/`RestoreTo`/`Purge`/`Empty`, and the unexported `backend` interface every platform implements. |
| `fsutil.go` | Platform-neutral filesystem helpers: `move` (rename with a copy fallback), `copyTree`, `treeSize`, `uniqueName`, `sortItems`, `prepareDest`. |
| `fsutil_unix.go` | `deviceOf`, `topDirOf`, `isStickyDir` - shared by the FreeDesktop and macOS backends. |
| `errno_{unix,windows,other}.go` | `errCrossDevice`, the errno a rename fails with across filesystems. |
| `trash_freedesktop.go`, `mounts_freedesktop.go` | Linux/BSD backend and its mount table scanning. |
| `trash_darwin.go` | macOS backend, including the index of original locations. |
| `trash_windows.go`, `shfileop_win{64,32}.go` | Windows backend and the two `SHFILEOPSTRUCTW` layouts. |
| `recyclebin_meta.go` | Codec for the Windows `$I` metadata files. Deliberately **not** build-constrained, so it is testable on any platform. |
| `trash_unsupported.go` | Every other GOOS: all operations fail with `ErrUnsupported`. |
| `cmd/recycler/` | Cobra CLI, one command per file, each registering itself in `init()`. |

`platformBackend()` is defined once per platform and is called on **every** API
call rather than cached, so tests can redirect the recycle bin with `HOME` and
`XDG_DATA_HOME` between calls. Keep it that way.

## Invariants

- **An `Item.ID` is a path inside a recycle bin directory, and is validated
  before use.** Every backend's `resolveID` checks that the ID names an existing
  entry inside one of *this user's* trash directories before restoring or
  deleting anything. A malformed or hostile ID must never reach a file outside
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

Files go to `~/.Trash`, or `<volume>/.Trashes/$uid` for other volumes. Finder
stores its "Put Back" location in private metadata that is neither documented
nor writable, so this package keeps its own index at
`~/Library/Application Support/recycler/index.json`, mapping the path in the
trash to the original location and deletion time. The index is updated under an
exclusive `flock` and pruned of entries whose files are gone - but only when
their trash directory is reachable, so an unmounted volume does not cost its
items their original locations.

Items trashed by Finder or another tool are listed with an empty `OriginalPath`
and can only be restored with an explicit destination (`RestoreTo`).

### Windows

Recycling and emptying go through the shell - `SHFileOperationW` with
`FOF_ALLOWUNDO`, and `SHEmptyRecycleBinW` - so they behave exactly like deleting
from Explorer. That includes the shell's own failure mode: if the volume has no
usable recycle bin, the file is deleted permanently. This is documented on
`Recycle`; do not paper over it by inventing a pre-check that cannot be made
reliable.

Listing, restoring and purging read `<drive>:\$Recycle.Bin\<user SID>\`
directly. Each item is a `$R<id><ext>` data file with a `$I<id><ext>` metadata
file; `recyclebin_meta.go` decodes both metadata versions (1 on Vista..8.1 with
a fixed 260-character path, 2 on Windows 10+ with a length-prefixed one).

`SHFILEOPSTRUCTW` is **byte-packed on 32-bit Windows only** (`shellapi.h` wraps
it in `pshpack1.h` under `#ifndef _WIN64`). Hence two declarations:
`shfileop_win64.go` uses natural Go layout for amd64/arm64, and
`shfileop_win32.go` keeps everything after `fFlags` as a raw byte tail so the
386/arm offsets match. If you touch either, keep both in sync.

## Building and testing

Run `go-toolchain` in the repository root - never bare `go` commands. It tidies,
vets, formats, runs the tests with a coverage floor of 80%, and builds.
`go-toolchain matrix` cross-compiles; use it after touching any per-platform
file:

```
go-toolchain matrix --targets linux/amd64,darwin/arm64,windows/amd64,windows/386
```

The test suite runs against a real recycle bin redirected into a temporary
directory (`isolateTrash`), so it exercises the actual FreeDesktop
implementation rather than a mock. Windows and macOS specific behaviour that
cannot run on Linux is covered where possible by platform-neutral unit tests -
that is why the `$I` codec has no build constraint.

CI (`.github/workflows/ci.yml`) runs the same toolchain and builds every
supported target, windows/386 included, so a per-platform file that stops
compiling fails the build. No job may be named `all-builds`: that status is
posted by the org's required-builds-manager app, and the go-toolchain action
fails any workflow that shadows it.
