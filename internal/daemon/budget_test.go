package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// cargoTarget writes the markers that make dir a cargo target directory.
func cargoTarget(t *testing.T, dir string) string {
	t.Helper()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, ".rustc_info.json"), []byte("{}"), 0o644))
	return target
}

// builtFrom is an item recycled from origin.
func builtFrom(name, origin string, size int64, age time.Duration) bin.Item {
	return bin.Item{
		ID:           filepath.Join("/trash", "files", name),
		Name:         name,
		OriginalPath: origin,
		Size:         size,
		DeletedAt:    time.Now().Add(-age),
	}
}

// Build output past the budget goes on every sweep, the oldest first, while the
// filesystem is nowhere near its floor. A user's own recycled file is never
// counted against the budget and never goes for it.
func TestTheSweepGivesBackBuildOutputPastItsBudget(t *testing.T) {
	target := cargoTarget(t, t.TempDir())
	newest := builtFrom("recycler-trim-3", filepath.Join(target, "recycler-trim-3"), 40, time.Hour)
	middle := builtFrom("recycler-trim-2", filepath.Join(target, "recycler-trim-2"), 40, 2*time.Hour)
	oldest := builtFrom("recycler-trim-1", filepath.Join(target, "recycler-trim-1"), 40, 3*time.Hour)
	mine := builtFrom("notes.txt", filepath.Join(t.TempDir(), "notes.txt"), 1000, 10*time.Hour)
	b := &fakeBackend{}

	evicted, kept := settleBuildOutput(b, []bin.Item{mine, oldest, newest, middle}, 50)

	assert.Equal(t, []string{middle.ID, oldest.ID}, b.evicted)
	require.Len(t, evicted, 2)
	assert.NoError(t, evicted[0].Error)
	assert.ElementsMatch(t, []bin.Item{mine, newest}, kept, "the pressure sweep still sees what stayed")
}

// An eviction that fails is reported, and the item stays in the listing the
// pressure sweep reads.
func TestABuildOutputEvictionThatFailsIsReported(t *testing.T) {
	target := cargoTarget(t, t.TempDir())
	stuck := builtFrom("recycler-trim-1", filepath.Join(target, "recycler-trim-1"), 40, time.Hour)
	b := &fakeBackend{fail: map[string]error{stuck.ID: errors.New("busy")}}

	evicted, kept := settleBuildOutput(b, []bin.Item{stuck}, 10)

	require.Len(t, evicted, 1)
	assert.ErrorContains(t, evicted[0].Error, "busy")
	assert.Equal(t, []bin.Item{stuck}, kept)
}

// Within the budget nothing goes, and the listing is passed on as it was.
func TestBuildOutputWithinItsBudgetStays(t *testing.T) {
	target := cargoTarget(t, t.TempDir())
	small := builtFrom("recycler-trim-1", filepath.Join(target, "recycler-trim-1"), 40, time.Hour)
	b := &fakeBackend{}

	evicted, kept := settleBuildOutput(b, []bin.Item{small}, 100)

	assert.Empty(t, evicted)
	assert.Empty(t, b.evicted)
	assert.Equal(t, []bin.Item{small}, kept)
}
