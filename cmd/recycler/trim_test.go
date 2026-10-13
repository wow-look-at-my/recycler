package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/recycler/internal/cargotarget"
)

// writeTarget builds a cargo target directory in root, holding one file per name
// at size bytes and dated age old. It is the whole fixture: a couple of
// kilobytes, and cargo's own markers at the top of it.
func writeTarget(t *testing.T, root string, age time.Duration, files map[string]int) string {
	t.Helper()
	target := filepath.Join(root, "target")
	writeFile(t, filepath.Join(target, "CACHEDIR.TAG"), "Signature: 8a477f597d28d172789f06886806bc55\n")
	writeFile(t, filepath.Join(target, ".rustc_info.json"), "{}")
	for name, size := range files {
		path := filepath.Join(target, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644))
		if age > 0 {
			when := time.Now().Add(-age)
			require.NoError(t, os.Chtimes(path, when, when))
		}
	}
	return target
}

// itemsFrom lists what a run put in the bin from one target directory. A machine's
// real recycle bin holds whatever was recycled on it, the other volumes' trash
// included. A test asks about its own paths rather than assuming an empty bin.
func itemsFrom(t *testing.T, prefix string) []recycler.Item {
	t.Helper()
	items, err := recycler.List()
	require.NoError(t, err)
	var mine []recycler.Item
	for _, item := range items {
		if strings.HasPrefix(item.OriginalPath, prefix) {
			mine = append(mine, item)
		}
	}
	return mine
}

// A dry run is the report a user reads before trusting anything with a build
// directory. It has to say what would go and what it weighs, and change
// nothing at all.
func TestTrimDryRunReportsWithoutTouchingTheTree(t *testing.T) {
	work := isolateTrash(t)
	target := writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "incremental", "grok_build-1", "big.o"): 800,
		filepath.Join("debug", "deps", "libgrok-1.rlib"):               800,
	})
	before, err := os.ReadDir(target)
	require.NoError(t, err)

	out, err := run(t, "", "trim", "--dry-run", "--cap", "1k", work)
	require.NoError(t, err)

	assert.Contains(t, out, target)
	assert.Contains(t, out, "would take")
	assert.Contains(t, out, "incremental")
	assert.Contains(t, out, "1.0 KiB cap")
	assert.NotEmpty(t, before, "the fixture is empty, so the assertion above proved nothing")

	// Nothing moved, so nothing of this run's is in the bin and every file is where it was.
	assert.Empty(t, itemsFrom(t, target), "a dry run recycled something")
	assert.FileExists(t, filepath.Join(target, "debug", "deps", "libgrok-1.rlib"))
	assert.FileExists(t, filepath.Join(target, "debug", "incremental", "grok_build-1", "big.o"))
}

// capTaking spells the --cap that leaves a run freeing exactly the named entries.
// It sizes are what the filesystem says a file takes. A test derives its cap from
// the fixture instead of guessing at a block size.
func capTaking(t *testing.T, target string, takes ...string) string {
	t.Helper()
	left := cargotarget.DiskSize(target)
	for _, name := range takes {
		left -= cargotarget.DiskSize(filepath.Join(target, name))
	}
	require.Greater(t, left, int64(0), "the cap would have to free the whole directory")
	return strconv.FormatInt(left, 10)
}

// The same run without --dry-run takes the incremental cache first, and the
// newest artifact is the last thing a cap reaches.
func TestTrimRecyclesTheOldestOutputIntoTheBin(t *testing.T) {
	work := isolateTrash(t)
	target := writeTarget(t, work, 0, map[string]int{
		filepath.Join("debug", "incremental", "old-1", "big.o"): 500,
		filepath.Join("debug", "deps", "lib-old-1.rlib"):        500,
		filepath.Join("debug", "deps", "lib-new-1.rlib"):        500,
	})
	age := time.Now().Add(-48 * time.Hour)
	stale := []string{
		filepath.Join("debug", "incremental", "old-1"),
		filepath.Join("debug", "deps", "lib-old-1.rlib"),
	}
	for _, name := range []string{
		filepath.Join("debug", "incremental", "old-1", "big.o"),
		filepath.Join("debug", "deps", "lib-old-1.rlib"),
	} {
		require.NoError(t, os.Chtimes(filepath.Join(target, name), age, age))
	}

	out, err := run(t, "", "trim", "--cap", capTaking(t, target, stale...), work)
	require.NoError(t, err)
	assert.Contains(t, out, "trimmed")
	assert.NotContains(t, out, "gave back", "build output inside the budget left the bin")

	assert.NoFileExists(t, filepath.Join(target, "debug", "incremental", "old-1", "big.o"))
	assert.NoFileExists(t, filepath.Join(target, "debug", "deps", "lib-old-1.rlib"))
	assert.FileExists(t, filepath.Join(target, "debug", "deps", "lib-new-1.rlib"))
	assert.FileExists(t, filepath.Join(target, "CACHEDIR.TAG"))
	assert.DirExists(t, target)

	mine := itemsFrom(t, target)
	require.Len(t, mine, 1, "what the cap took is not one item in the recycle bin")
	assert.Equal(t, target, filepath.Dir(mine[0].OriginalPath),
		"%s came from somewhere the cap has no business reaching", mine[0].OriginalPath)
	assert.FileExists(t, filepath.Join(mine[0].ID, "debug", "incremental", "old-1", "big.o"))
	assert.FileExists(t, filepath.Join(mine[0].ID, "debug", "deps", "lib-old-1.rlib"))
}

// Past the cap the bin gives the build output back. The run returns the
// space it took rather than moving it beside the target directory.
func TestTrimReturnsTheSpaceItTook(t *testing.T) {
	work := isolateTrash(t)
	target := writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-a-1.rlib"): 64 << 10,
	})

	out, err := run(t, "", "trim", "--cap", "1", work)
	require.NoError(t, err)
	assert.Contains(t, out, "the bin gave back")
	assert.NoFileExists(t, filepath.Join(target, "debug", "deps", "lib-a-1.rlib"))
	assert.Empty(t, itemsFrom(t, target), "the build output is still in the bin, holding its space")
}

func TestTrimLeavesATargetWithRoomAlone(t *testing.T) {
	work := isolateTrash(t)
	target := writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "libgrok-1.rlib"): 800,
	})

	out, err := run(t, "", "trim", "--cap", "1G", work)
	require.NoError(t, err)
	assert.Contains(t, out, "nothing to do")

	assert.Empty(t, itemsFrom(t, target))
	assert.FileExists(t, filepath.Join(target, "debug", "deps", "libgrok-1.rlib"))
}

// A cap the tool cannot reach is said, because the silent alternative is a user
// believing the directory is now small.
func TestTrimSaysWhenTheCapIsOutOfReach(t *testing.T) {
	work := isolateTrash(t)
	writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "unmatched.bin"): 5000,
	})

	out, err := run(t, "", "trim", "--dry-run", "--cap", "100", work)
	require.NoError(t, err)
	assert.Contains(t, out, "nothing left to recycle is eligible here")
}

// The default root is the directory a machine's checkouts live in, and a machine
// without one is told rather than reported clean.
func TestTrimDefaultRootIsTheReposDirectory(t *testing.T) {
	work := isolateTrash(t)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEqual(t, work, home)

	_, err = run(t, "", "trim", "--dry-run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), trimRootsEnv, "the error has to name the way to configure a root")

	repos := filepath.Join(home, reposDirName)
	target := writeTarget(t, repos, 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "libgrok-1.rlib"): 800,
	})
	out, err := run(t, "", "trim", "--dry-run", "--cap", "100")
	require.NoError(t, err)
	assert.Contains(t, out, target)
}

func TestTrimScansEveryRootListed(t *testing.T) {
	work := isolateTrash(t)
	first := writeTarget(t, filepath.Join(work, "one"), 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-a-1.rlib"): 800,
	})
	second := writeTarget(t, filepath.Join(work, "two"), 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-b-1.rlib"): 800,
	})
	// The listed roots are scanned as themselves, so the targets are one level in.
	t.Setenv(trimRootsEnv, filepath.Dir(first)+string(os.PathListSeparator)+filepath.Dir(second))

	out, err := run(t, "", "trim", "--dry-run", "--cap", "100")
	require.NoError(t, err)
	assert.Contains(t, out, first)
	assert.Contains(t, out, second)
	assert.Contains(t, out, "2 target directories over the cap")
}

func TestTrimNamesEveryEntryWhenAsked(t *testing.T) {
	work := isolateTrash(t)
	target := writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-a-1.rlib"): 800,
		filepath.Join("debug", "deps", "lib-b-1.rlib"): 800,
	})

	out, err := run(t, "", "trim", "--dry-run", "--verbose", "--cap", "100", work)
	require.NoError(t, err)
	assert.Contains(t, out, filepath.Join(target, "debug", "deps", "lib-a-1.rlib"))
	assert.Contains(t, out, filepath.Join(target, "debug", "deps", "lib-b-1.rlib"))
}

func TestTrimRejectsACapThatIsNotASize(t *testing.T) {
	work := isolateTrash(t)
	_, err := run(t, "", "trim", "--cap", "banana", work)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "banana")

	_, err = run(t, "", "trim", "--cap", "0", work)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than zero")
}

// cargo-clean is the name a Rust user reaches for; trim is the name the rest of
// this CLI's verbs use. Both have to work.
func TestTheTrimCommandAnswersToCargoClean(t *testing.T) {
	work := isolateTrash(t)
	writeTarget(t, work, 24*time.Hour, map[string]int{
		filepath.Join("debug", "incremental", "grok_build-1", "big.o"): 800,
	})

	out, err := run(t, "", "cargo-clean", "--dry-run", "--cap", "100", work)
	require.NoError(t, err)
	assert.Contains(t, out, "would take")
}

// The cap is a per-target-directory cap, so one small checkout cannot be made
// to pay for another's build.
func TestACapIsChargedToOneTargetDirectory(t *testing.T) {
	work := isolateTrash(t)
	small := writeTarget(t, filepath.Join(work, "small"), 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-a-1.rlib"): 400,
	})
	large := writeTarget(t, filepath.Join(work, "large"), 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-b-1.rlib"): 1 << 20,
	})

	// A cap is charged in allocated blocks, so the small checkout's files weigh a few blocks each.
	out, err := run(t, "", "trim", "--dry-run", "--cap", "256k", work)
	require.NoError(t, err)
	assert.NotContains(t, out, small, "a checkout inside its cap was reported anyway")
	assert.Contains(t, out, large)
}

// A worktree's target directory is reported as one, because the branch it belongs
// to is the thing a reader then goes and checks on.
func TestTrimReportsAWorktreeTarget(t *testing.T) {
	work := isolateTrash(t)
	checkout := filepath.Join(work, ".claude", "worktrees", "agent-7")
	require.NoError(t, os.MkdirAll(checkout, 0o755))
	target := writeTarget(t, checkout, 24*time.Hour, map[string]int{
		filepath.Join("debug", "deps", "lib-a-1.rlib"): 800,
	})

	out, err := run(t, "", "trim", "--dry-run", "--cap", "100", work)
	require.NoError(t, err)
	assert.Contains(t, out, target)
	assert.Contains(t, out, "worktree agent-7")
}
