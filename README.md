# recycler

One Go API, and one command, for the recycle bin on Linux, macOS and Windows.

Files go to the platform's own recycle bin: the FreeDesktop trash can, macOS Trash or the Windows Recycle Bin. The desktop then shows them where you expect.

## Command

```
go install github.com/wow-look-at-my/recycler/cmd/recycler@latest
```

```
recycler                            # browse the bin on a full screen
recycler trash notes.txt project/   # move to the recycle bin
recycler list                       # what is in there, newest first
recycler restore notes.txt          # put it back where it came from
recycler daemon                     # watch free space (started for you)
recycler trim --dry-run             # cargo build output over its size cap
```

No command of yours deletes anything: an item leaves the bin by being restored, and emptying the bin is left to the desktop environment.

Disk pressure is the exception, because a bin nobody empties fills the disk and the recycled copy is what holds the space. Recycling something starts a daemon that reads free space every few seconds. It gives back the oldest items when a filesystem drops under a tenth free, or 1 GiB, whichever is smaller. It uses the size recorded when each item was recycled, so it never walks the bin to measure it. There is one daemon per user per system: its lock and home bin come from the account database, so a script that exports its own `HOME` or `XDG_*` directories reaches the same daemon. Set `RECYCLER_NO_DAEMON=1` to run it yourself instead, or not at all.

Build output is the disk filler no recycle bin explains. rustc appends to a `target` directory and never removes anything from it, so every branch. Every worktree and every dependency bump leaves its object files, rlibs and fingerprints behind, and cargo's only remedy is `cargo clean`. This throws away the whole directory and rebuilds everything still wanted. `recycler trim` caps each target directory instead. It recycles the oldest regenerable output inside one, incrementals first, and stops as soon as the directory fits so the build. In progress keeps its cache.

The copies under `.claude/worktrees` and `.grok/worktrees` are found too. `--dry-run` says what will go and what it weighs, and `--cap` is the size. It runs when you invoke it.

What a run takes goes to the recycle bin as one item per target directory. The bin keeps build output to the same cap. The bin gives the rest back to the filesystem, oldest first, so a trim returns the space it took. The daemon holds the bin to that cap between runs. Your own recycled files never count against it.

Run on a terminal with no arguments, `recycler` opens the bin in a browser: arrows move, `/` filters, `enter` restores what is selected. Redirected, it prints the help instead, and `recycler tui` opens the browser either way.

`restore` takes the ID from `recycler list`, or a name or original path when it matches exactly one item. `list --json` prints the same data for scripts.

## Library

```go
import "github.com/wow-look-at-my/recycler"

recycler.Recycle("notes.txt")            // move to the recycle bin
items, _ := recycler.List()              // newest first
path, _ := recycler.Restore(items[0].ID) // back to where it came from
```

The API is those operations and nothing else. Recycling is reversible, and the package offers no operation that makes it permanent.

## Platform notes

- **Linux** and the BSDs follow the FreeDesktop trash specification, including per-filesystem trash directories, so other trash tools interoperate.
- **macOS** uses `~/.Trash`. It reads and writes Finder's own "Put Back" records rather than an index of its own. What this restores and what Finder puts back are the same thing.
- **Windows** (64-bit) recycles through the shell, exactly like deleting in Explorer, and reads `$Recycle.Bin` directly to list and restore.

Details, invariants and the on-disk formats are in [CLAUDE.md](CLAUDE.md).
