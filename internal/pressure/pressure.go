// Package pressure decides whether recycling a path can free any space.
//
// A bin sits on the filesystem it takes from, so recycling frees nothing: the
// bytes move sideways. That is the right trade while there is room to defer a
// deletion into, and the wrong a single when there is not.
package pressure

import (
	"fmt"
	"path/filepath"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// A Decision is what should happen to a single path handed to Recycle.
type Decision struct {
	// Permanent is set when recycling frees nothing, with Reason saying why.
	Permanent bool
	Reason    string

	Avail  uint64 // available bytes on the filesystem holding the path
	Target uint64 // the floor below which recycling stops being deferral
	Size   int64  // the blocks a copy of the path needs, or [bin.SizeUnknown] when no copy is made
}

// Mirrors internal/daemon, which imports the backends calling this. A test pins
// both spellings together.
const (
	freeTargetFraction = 10
	freeTargetCeiling  = 1 << 30
)

// Floor is the available bytes under which recycling stops deferring, and
// where the daemon sweeps.
func Floor(total uint64) uint64 {
	if target := total / freeTargetFraction; target < freeTargetCeiling {
		return target
	}
	return freeTargetCeiling
}

// Check decides what to do with path when the bin directory dest takes it.
//
// Moving into a bin on the same filesystem is a rename: it needs no free space
// whatever the path weighs, so only the floor applies. A bin on another
// filesystem takes a copy, which needs room there for the blocks the path
// occupies. That is its allocated size, not the length its files state,
// which a compressed or sparse filesystem puts far above it.
func Check(path, dest string) Decision {
	avail, total, err := diskfree.Free(filepath.Dir(path))
	if err != nil {
		return Decision{Size: bin.SizeUnknown}
	}
	d := decide(avail, total)
	if d.Permanent {
		return d
	}
	same, err := fsutil.SameFilesystem(filepath.Dir(path), dest)
	if err != nil || same {
		return d
	}
	destAvail, _, err := diskfree.Free(dest)
	if err != nil {
		return d
	}
	return fits(d, destAvail, fsutil.DiskUsage(path))
}

// decide is the floor against an already-read filesystem, so the cases can be
// tested without a filesystem that is full.
func decide(avail, total uint64) Decision {
	d := Decision{Avail: avail, Target: Floor(total), Size: bin.SizeUnknown}
	if avail < d.Target {
		d.Permanent = true
		d.Reason = fmt.Sprintf("only %s free, under the %s the bin needs to defer anything",
			Bytes(avail), Bytes(d.Target))
	}
	return d
}

// fits is the rule for a copy onto another filesystem with destAvail free,
// of a path occupying size bytes.
func fits(d Decision, destAvail uint64, size int64) Decision {
	d.Size = size
	if size == bin.SizeUnknown || size < 0 || uint64(size) <= destAvail {
		return d
	}
	d.Permanent = true
	d.Reason = fmt.Sprintf("%s does not fit in the %s free on the filesystem holding the bin",
		Bytes(uint64(size)), Bytes(destAvail))
	return d
}

// Bytes formats a byte count the way the rest of the CLI prints sizes.
func Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
