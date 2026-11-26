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
recycler daemon -v                  # also log each sweep and each item it keeps
recycler daemon log                 # what the background daemon deleted, and why
recycler config set min-free 20GB   # free space the daemon keeps (or 15%)
recycler empty --older-than 30d     # delete old items, after asking
recycler empty --min-free 50GB --max-size 20GB --dry-run
recycler config folder add /tmp 10GB --delete       # keep /tmp under 10GB
recycler config folder add ~/scratch 20GB           # same, but to the bin
recycler daemon --folder /tmp:10GB:delete           # a folder for one run
```

A capped folder loses its least recently used files first once it is over its size: to the recycle bin, or permanently with `--delete`. Only your own files go, and only those not used in the last hour. No folder is capped until you add one, a cap is at least 1 GiB. A folder cannot hold your home directory.

`recycler empty` is the only command of yours that deletes from the bin. It takes items recycled longer ago than an age, the oldest items until a filesystem has some free space. Otherwise, it takes the oldest items until the bin weighs at most a size. It lists them and asks first. It refuses an age under a day, a bin size under 1 GiB and a free-space goal over half the filesystem. It never takes anything recycled in the last day.

It cannot empty the whole bin. An AI agent session (with `CLAUDECODE`, `GROK_BUILD` or a like variable set) can neither empty the bin, change `min-free`, nor cap a folder. A process running under a known agent is refused the same way, whatever its variables say.

The daemon also deletes, because a bin nobody empties fills the disk and the recycled copy is what holds the space. Recycling something starts a daemon that reads free space every few seconds. It gives back the oldest items when a filesystem drops under a fortieth free (50 GB of a 2 TB disk), or 200 GiB, whichever is smaller. `min-free` raises that, up to half the filesystem, and it logs every item it deletes and the rule that chose it. It uses the size recorded when each item was recycled, so it never walks the bin to measure it. There is one daemon per user.

Recycling onto a filesystem that is already that short works the same way. The bin gives back its oldest items there to make room for the new one, and `recycler trash` names each on standard error. The new item is deleted outright only when the bin holds too little there to make that room.

This holds per system.

Its lock and home bin come from the account database, so a script that exports its own `HOME` or `XDG_*` directories reaches the same daemon. Set `RECYCLER_NO_DAEMON=1` to run it yourself instead, or not at all.

Build output is the disk filler no recycle bin explains. rustc appends to a `target` directory and never removes anything from it, so every branch. Every worktree and every dependency bump leaves its object files, rlibs and fingerprints behind, and cargo's only remedy is `cargo clean`. This throws away the whole directory and rebuilds everything still wanted. `recycler trim` caps each target directory instead. It recycles the oldest regenerable output inside one, incrementals first, and stops as soon as the directory fits so the build. In progress keeps its cache.

The copies under `.claude/worktrees` and `.grok/worktrees` are found too. `--dry-run` says what will go and what it weighs, and `--cap` is the size. It runs when you invoke it.

What a run takes goes to the recycle bin as one item per target directory. The bin keeps build output to the same cap. The bin gives the rest back to the filesystem, oldest first, so a trim returns the space it took. The daemon holds the bin to that cap between runs. Your own recycled files never count against it.

Run on a terminal with no arguments, `recycler` opens the bin in a browser. Redirected, it prints the help instead, and `recycler tui` opens the browser either way.

| Keys | Mouse | Does |
|---|---|---|
| arrows, `pgup`/`pgdown`, `home`/`end` | click a row, wheel, drag the scrollbar | move and scroll |
| `enter` | double-click | restore, after asking |
| `t` | | restore into a directory you type |
| `d` | | delete permanently, after asking |
| `space`, `a`, shift+arrows | ctrl+click, shift+click | mark one, mark all, mark a range |
| `←`/`→` | click the arrow | close or open a directory or a file's versions |
| `v` | | switch between the tree and one row per item |
| `f` | | show one disk at a time, then every disk |
| `s`, `S` | click a heading | change the sort column, reverse it |
| `/` | | filter by name or path, `esc` clears |
| `m` | right-click | a menu of what can be done to the item |

The bin opens as a tree of the directories things came from. Every version of one path shares a row. An action takes every marked item, or everything the selected row holds when nothing is marked. A directory row restores or deletes all of it at once. An AI agent session cannot delete from the browser.

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
