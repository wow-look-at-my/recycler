// amount.go holds Amount.

package daemon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// An Amount is a quantity of space: a byte count, or a percentage of a
// filesystem when Percent is set.
type Amount struct {
	Bytes   uint64
	Percent float64
}

// IsZero reports whether the amount was never set.
func (a Amount) IsZero() bool { return a.Bytes == 0 && a.Percent == 0 }

// Of answers the amount in bytes on a filesystem of total bytes.
func (a Amount) Of(total uint64) uint64 {
	if a.Percent > 0 {
		return uint64(float64(total) * a.Percent / 100)
	}
	return a.Bytes
}

func (a Amount) String() string {
	if a.Percent > 0 {
		return strconv.FormatFloat(a.Percent, 'f', -1, 64) + "%"
	}
	return formatBytes(a.Bytes)
}

// units maps each suffix ParseAmount takes to its size.
var units = map[string]uint64{
	"b":   1,
	"k":   1 << 10,
	"kb":  1e3,
	"kib": 1 << 10,
	"m":   1 << 20,
	"mb":  1e6,
	"mib": 1 << 20,
	"g":   1 << 30,
	"gb":  1e9,
	"gib": 1 << 30,
	"t":   1 << 40,
	"tb":  1e12,
	"tib": 1 << 40,
}

// ParseAmount reads "20GB", "1.5 GiB", "500MiB" or "15%". A bare number is
// refused: it is too easy to mean gigabytes and get bytes.
func ParseAmount(s string) (Amount, error) {
	text := strings.TrimSpace(s)
	if strings.HasSuffix(text, "%") {
		pct, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(text, "%")), 64)
		if err != nil || math.IsNaN(pct) || pct <= 0 || pct > 100 {
			return Amount{}, fmt.Errorf("recycler: %q is not a percentage between 0 and 100", s)
		}
		return Amount{Percent: pct}, nil
	}
	split := strings.IndexFunc(text, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	})
	if split <= 0 {
		return Amount{}, fmt.Errorf("recycler: %q needs a number and a unit, such as 20GB, 5GiB or 15%%", s)
	}
	unit, ok := units[strings.ToLower(strings.TrimSpace(text[split:]))]
	if !ok {
		return Amount{}, fmt.Errorf("recycler: %q has an unknown unit; use B, KB, MB, GB, TB, KiB, MiB, GiB, TiB or %%", s)
	}
	n, err := strconv.ParseFloat(text[:split], 64)
	if err != nil || n <= 0 {
		return Amount{}, fmt.Errorf("recycler: %q is not a positive amount", s)
	}
	bytes := n * float64(unit)
	if bytes >= math.MaxUint64 {
		return Amount{}, fmt.Errorf("recycler: %q is too large", s)
	}
	return Amount{Bytes: uint64(bytes)}, nil
}

// formatBytes formats a byte count the way the rest of the CLI prints sizes.
func formatBytes(n uint64) string {
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
