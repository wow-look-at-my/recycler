package diskfree

// reachable returns the blocks of a filesystem this caller can ever use.
//
// blocks counts the whole device. free counts what is unallocated. avail counts
// what an unprivileged caller may still take. The difference between free and
// avail is the reserve, which belongs to another user and never becomes
// available here, so it is not part of the pool a caller measures itself
// against. The arguments are signed because that is how the platform statfs
// structures spell them, and a filesystem that reports avail above free is
// treated as having no reserve rather than a negative one.
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
