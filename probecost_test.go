package recycler

import (
	"testing"

	"github.com/wow-look-at-my/recycler/internal/diskfree"
)

// BenchmarkFreeProbe times the syscall the daemon polls with, which is what
// decides how often it can afford to look.
func BenchmarkFreeProbe(b *testing.B) {
	dir := b.TempDir()
	b.ResetTimer()
	for range b.N {
		if _, _, err := diskfree.Free(dir); err != nil {
			b.Fatal(err)
		}
	}
}
