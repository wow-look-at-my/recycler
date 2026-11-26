// Package pressure holds the free-space floor a recycle bin defers under.
//
// A bin sits on the filesystem it takes from, so recycling frees nothing: the
// bytes move sideways. That is the right trade while there is room to defer a
// deletion into. Under the floor the bin gives something back first.
package pressure

import "fmt"

const (
	floorFraction = 40
	floorCeiling  = 200 << 30
)

// Floor is the available bytes under which recycling stops deferring, and
// where the daemon sweeps.
func Floor(total uint64) uint64 {
	return min(total/floorFraction, floorCeiling)
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
