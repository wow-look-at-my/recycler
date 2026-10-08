package diskfree

// reachable returns the blocks of a filesystem this caller can ever use.
<<<<<<< HEAD
//
// free minus avail is the reserve: another user's, and never arriving here.
=======
>>>>>>> origin/master
func reachable[T int64 | uint64](blocks, free, avail T) uint64 {
	if blocks < 0 || free < 0 || avail < 0 || free <= avail {
		return uint64(max(blocks, 0))
	}
	reserved := uint64(free - avail)
	total := uint64(blocks)
	if reserved >= total {
		return 0
	}
	return total - reserved
}
