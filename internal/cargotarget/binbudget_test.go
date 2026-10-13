package cargotarget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A run that gathers nothing leaves no empty directory behind in the target.
func TestARunThatGathersNothingLeavesNoBundle(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	plan, err := Inspect(Target{Path: target}, 1)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(target, deps("lib-a-000000.rlib"))))
	b := newBin(t)

	recycled, _ := apply(b, &plan, roomy, &Outcome{})
	assert.Zero(t, recycled)
	assert.Empty(t, b.taken)
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), bundlePrefix), "an empty bundle was left at %s", e.Name())
	}
}

// A whole profile directory goes after entries an earlier tier already took out
// of it, so it is merged into what is already gathered.
func TestStageMergesIntoWhatIsAlreadyGathered(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "debug")
	dst := filepath.Join(root, "dst", "debug")
	writeFile(t, filepath.Join(src, "a"), "a", 0)
	writeFile(t, filepath.Join(src, "deps", "c"), "c", 0)
	writeFile(t, filepath.Join(dst, "deps", "b"), "b", 0)

	require.NoError(t, stage(src, dst))
	assert.FileExists(t, filepath.Join(dst, "a"))
	assert.FileExists(t, filepath.Join(dst, "deps", "b"))
	assert.FileExists(t, filepath.Join(dst, "deps", "c"))
	assert.NoDirExists(t, src)

	// A file where a directory is to go is never overwritten.
	writeFile(t, filepath.Join(src, "a"), "new", 0)
	err := stage(filepath.Join(src, "a"), filepath.Join(dst, "a"))
	require.ErrorIs(t, err, bin.ErrExists)
	got, err := os.ReadFile(filepath.Join(dst, "a"))
	require.NoError(t, err)
	assert.Equal(t, "a", string(got))
}

// A trim returns the space it took: build output the bin holds past the cap
// goes back to the filesystem. A user's own item in the bin is not build
// output, and stays however large it is.
func TestATrimGivesBackBuildOutputPastTheBudget(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		deps("lib-a-000000.rlib"): {size: 300, age: old},
		deps("lib-b-000000.rlib"): {size: 300, age: old},
	})
	b := newBin(t)
	mine := filepath.Join(b.dir, "notes.txt")
	writeFile(t, mine, strings.Repeat("y", 4096), 0)
	b.items = append(b.items, bin.Item{
		ID: mine, OriginalPath: filepath.Join(root, "notes.txt"), Size: 4096, DeletedAt: time.Now().Add(-old),
	})

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: roomy})
	require.NoError(t, err)
	assert.Empty(t, outcome.Errors)
	assert.Equal(t, 2, outcome.Recycled)
	require.Len(t, outcome.Returned, 1)
	require.Len(t, b.taken, 1)
	assert.Equal(t, []string{b.binned(b.taken[0])}, b.evicted)
	assert.NoDirExists(t, b.binned(b.taken[0]), "the build output is still holding its space")
	assert.Positive(t, outcome.Freed())
	assert.NoFileExists(t, filepath.Join(target, deps("lib-a-000000.rlib")))
	assert.FileExists(t, mine, "a user's own item went for the build output's budget")
}

// The bin keeps the newest build output up to the budget, in each bin
// directory, and gives back the rest. What it keeps is never chosen for being
// small: an older item goes once a newer one has filled the budget.
func TestOverflowKeepsTheNewestWithinTheBudget(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, nil)
	notCargo := filepath.Join(root, "other", targetName)
	require.NoError(t, os.MkdirAll(notCargo, 0o755))
	now := time.Now()
	in := func(dir, name string, size int64, age time.Duration) bin.Item {
		return bin.Item{
			ID: filepath.Join(dir, name), OriginalPath: filepath.Join(target, name), Size: size, DeletedAt: now.Add(-age),
		}
	}
	newest := in("/bin-a", "trim-3", 60, 1*time.Hour)
	middle := in("/bin-a", "trim-2", 60, 2*time.Hour)
	oldest := in("/bin-a", "trim-1", 1, 3*time.Hour)
	elsewhere := in("/bin-b", "trim-4", 60, 4*time.Hour)
	unknown := in("/bin-a", "trim-0", bin.SizeUnknown, 5*time.Hour)
	maven := bin.Item{ID: "/bin-a/classes", OriginalPath: filepath.Join(notCargo, "classes"), Size: 1000, DeletedAt: now}
	theTarget := bin.Item{ID: "/bin-a/target", OriginalPath: target, Size: 1000, DeletedAt: now}

	over := Overflow([]bin.Item{oldest, maven, newest, elsewhere, unknown, middle, theTarget}, 100)
	assert.Equal(t, []bin.Item{middle, oldest}, over)
}

