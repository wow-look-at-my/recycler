package recycler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
)

// BenchmarkFreeProbe times the syscall the daemon polls with, which is what
// decides how often it can afford to look.
func BenchmarkFreeProbe(b *testing.B) {
	dir := b.TempDir()
	b.ResetTimer()
	for range b.N {
		_, _, err := diskfree.Free(dir)
		require.Nil(b, err)

	}
}
