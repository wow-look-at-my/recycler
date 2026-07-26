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
| `trash_darwin.go` | macOS backend. |
| `putback.go` | Finder's `ptbL`/`ptbN` put back records: reading them out of a trash directory's `.DS_Store` and editing them back in under a lock. Built on every Unix so the test suite covers it on Linux. |
| `dsstore.go`, `dsstore_write.go` | Codec for the `.DS_Store` files those records live in: the B-tree parser, and the writer with its buddy allocator. Deliberately **not** build-constrained, so it is testable on any platform. |
| `trash_windows.go`, `shfileop_windows*.go` | Windows backend, the `SHFILEOPSTRUCTW` declaration, and the guard that fails a 32-bit Windows build. |
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

Only **64-bit Windows** is supported. `SHFILEOPSTRUCTW` is byte-packed on 32-bit
Windows (`shellapi.h` wraps it in `pshpack1.h` under `#ifndef _WIN64`), so the
natural Go layout in `shfileop_windows.go` is right for amd64/arm64 and wrong
for 386/arm. `shfileop_windows_unsupported.go` therefore fails a 32-bit build on
purpose, rather than letting the shell read a delete request from the wrong
offsets.

## Building and testing

Run `go-toolchain` in the repository root - never bare `go` commands. It tidies,
vets, formats, runs the tests with a coverage floor of 80%, and builds.
`go-toolchain matrix` cross-compiles; use it after touching any per-platform
file:

```
go-toolchain matrix --targets linux/amd64,darwin/arm64,windows/amd64,windows/arm64
```

**Do not build this project as a cosmo fat APE.** gosmopolitan compiles with
`GOOS=cosmo`, which matches none of the backends' build constraints, so the APE
falls through to `trash_unsupported.go` and every operation fails with
`ErrUnsupported` - verified by building one and running it. Worse, the default
`--cosmo-slots` mapping copies that APE onto `recycler_windows_amd64.exe`, so a
cosmo build would ship exactly the platform it cannot serve. Recycling is not
portable-libc work: it is three unrelated mechanisms, one of which is only
reachable through the Windows shell API, so the per-GOOS matrix is the honest
build.

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
supported target, so a per-platform file that stops compiling fails the build. No job may be named `all-builds`: that status is
posted by the org's required-builds-manager app, and the go-toolchain action
fails any workflow that shadows it.
