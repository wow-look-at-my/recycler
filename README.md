# recycler

One Go API, and one command, for the recycle bin on Linux, macOS and Windows.

Files go to the platform's own recycle bin — the FreeDesktop trash can, macOS
Trash or the Windows Recycle Bin — so the desktop shows them where you expect.

## Command

```
go install github.com/wow-look-at-my/recycler/cmd/recycler@latest
```

```
recycler trash notes.txt project/   # move to the recycle bin
recycler list                       # what is in there, newest first
recycler restore notes.txt          # put it back where it came from
recycler purge notes.txt            # delete one item for good
recycler empty                      # delete everything for good
```

`restore` and `purge` take the ID from `recycler list`, or a name or original
path when it matches exactly one item. `list --json` prints the same data for
scripts.

## Library

```go
import "github.com/wow-look-at-my/recycler"

recycler.Recycle("notes.txt")            // move to the recycle bin
items, _ := recycler.List()              // newest first
path, _ := recycler.Restore(items[0].ID) // back to where it came from
recycler.Purge(items[0].ID)              // gone for good
recycler.Empty()                         // all of it, gone for good
```

## Platform notes

- **Linux** and the BSDs follow the FreeDesktop trash specification, including
  per-filesystem trash directories, so other trash tools interoperate.
- **macOS** uses `~/.Trash`. Finder's "Put Back" data is private, so this
  package records original locations itself; items trashed by Finder are listed
  but need an explicit destination to restore.
- **Windows** (64-bit) recycles through the shell, exactly like deleting in
  Explorer, and reads `$Recycle.Bin` directly to list, restore and purge.

Details, invariants and the on-disk formats are in [CLAUDE.md](CLAUDE.md).
