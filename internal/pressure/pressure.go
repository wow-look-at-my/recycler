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
)

// A Decision is what should happen to a single path handed to Recycle.
type Decision struct {
	// Permanent is set when recycling frees nothing, with Reason saying why.
	Permanent bool
	Reason    string

	Avail  uint64 // available bytes on the filesystem holding the path
	Target uint64 // the floor below which recycling stops being deferral
	Size   int64  // the path's measured size, or [bin.SizeUnknown]
}

// Mirrors internal/daemon, which imports the backends calling this. A test pins
// both spellings together.
const (
	freeTargetFraction = 10
	freeTargetCeiling  = 1 << 30
)

// Floor is the available bytes under which recycling stops deferring, and where
// the daemon sweeps. A fraction of the filesystem, capped: small media keep the
// fraction, because a flat cap there makes every deletion permanent.
func Floor(total uint64) uint64 {
	if target := total / freeTargetFraction; target < freeTargetCeiling {
		return target
	}
	return freeTargetCeiling
}

// Check decides what to do with path, whose measured size is size. An unreadable
// filesystem reads as ordinary recycling: guessing "permanent" off a failed
// probe destroys a file over a missing answer.
func Check(path string, size int64) Decision {
	avail, total, err := diskfree.Free(filepath.Dir(path))
	if err != nil {
		return Decision{Size: size}
	}
	return decide(avail, total, size)
}

// decide is Check's rules against an already-read filesystem, so the cases
// can be tested without a single that is full.
func decide(avail, total uint64, size int64) Decision {
	d := Decision{Avail: avail, Target: Floor(total), Size: size}
	switch {
	case avail < d.Target:
		d.Permanent = true
		d.Reason = fmt.Sprintf("only %s free, under the %s the bin needs to defer anything",
			Bytes(avail), Bytes(d.Target))
	case size != bin.SizeUnknown && size >= 0 && uint64(size) > avail:
		d.Permanent = true
		d.Reason = fmt.Sprintf("%s does not fit in the %s free", Bytes(uint64(size)), Bytes(avail))
	}
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
