# The `.DS_Store` format

Codec for the Finder's `.DS_Store` files. On macOS that is where the Trash keeps the "Put Back" location of every item in it. `ptbL` holds the parent directory an item came from, relative to the volume root. `ptbN` holds the name it carried there.

Apple has never documented the format. It was reverse engineered frequently enough to be reliable. A file is a B-tree of records, stored in blocks managed by a buddy allocator. The header carries a fixed prefix, the magic `Bud1`, and the offset and length of the allocator's bookkeeping block. That offset appears again further along. The Finder rejects a file whose copies disagree.

The tail of the header has no known purpose.

Every offset in the file is relative to the end of the prefix. The offset adjustment in `dsstore.go` accounts for that. The bookkeeping block holds the block address table, where an entry packs an offset together with the block's width in its low bits. It also holds a directory mapping `DSDB` to the block number of the B-tree master block, and a free list per width.

A B-tree node opens with P and a record count. A leaf leaves P empty and its records follow directly. An internal node holds that many child-and-record entries, with P as the rightmost child.

## The writer

`write.go` rewrites a whole file rather than editing one in place. The Finder owns these files and rewrites them when it wants to. An in-place edit therefore races a writer this package cannot see. A whole-file write also keeps every record the file already had. That is what carries the icon positions and window settings the Finder put there.

## Build constraints

`internal/dsstore` and `internal/winbin` deliberately carry no build constraint. Both codecs stay testable on any platform.
