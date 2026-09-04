# recycler

One Go API, and one command, for the recycle bin on Linux, macOS and Windows.

Files go to the platform's own recycle bin — the FreeDesktop trash can, macOS
Trash or the Windows Recycle Bin — so the desktop shows them where you expect.

## Command

```
go install github.com/wow-look-at-my/recycler/cmd/recycler@latest
```

```
recycler                            # browse the bin on a full screen
recycler trash notes.txt project/   # move to the recycle bin
recycler list                       # what is in there, newest first
recycler restore notes.txt          # put it back where it came from
```

That is the whole command. There is no way to delete anything permanently:
an item leaves the bin by being restored, and emptying the bin is left to the
desktop environment.

Run on a terminal with no arguments, `recycler` opens the bin in a browser:
arrows move, `/` filters, `enter` restores what is selected. Redirected, it
prints the help instead, and `recycler tui` opens the browser either way.

`restore` takes the ID from `recycler list`, or a name or original path when it
matches exactly one item. `list --json` prints the same data for scripts.

## Library

```go
import "github.com/wow-look-at-my/recycler"

recycler.Recycle("notes.txt")            // move to the recycle bin
items, _ := recycler.List()              // newest first
path, _ := recycler.Restore(items[0].ID) // back to where it came from
```

The API is those three operations and nothing else. Recycling is reversible,
and the package offers no operation that makes it permanent.

## Platform notes

- **Linux** and the BSDs follow the FreeDesktop trash specification, including
  per-filesystem trash directories, so other trash tools interoperate.
- **macOS** uses `~/.Trash`, reading and writing Finder's own "Put Back" records
  rather than an index of its own, so what this restores and what Finder puts
  back are the same thing.
- **Windows** (64-bit) recycles through the shell, exactly like deleting in
  Explorer, and reads `$Recycle.Bin` directly to list and restore.

Details, invariants and the on-disk formats are in [CLAUDE.md](CLAUDE.md).
