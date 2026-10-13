package cargotarget

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// A fixture is a fake cargo tree written in code, sized and dated per file.
type fixture struct {
	size int
	age  time.Duration
}

// old and fresh date a fixture entry.
const (
	old   = 100 * time.Hour
	fresh = 1 * time.Hour
)

// newTarget writes a cargo target directory in root and returns its path. Each key
// of files is a path inside it, and gets the size and age named.
func newTarget(t *testing.T, root string, files map[string]fixture) string {
	t.Helper()
	target := filepath.Join(root, targetName)
	writeFile(t, filepath.Join(target, cacheDirTag), "Signature: 8a477f597d28d172789f06886806bc55\n", 0)
	writeFile(t, filepath.Join(target, rustcInfo), "{}", 0)
	for name, f := range files {
		writeFile(t, filepath.Join(target, name), strings.Repeat("x", f.size), f.age)
	}
	return target
}

// newWorktreeTarget writes the target directory of an agent worktree, which is
// where cargo's refusal to collect any garbage first becomes visible.
func newWorktreeTarget(t *testing.T, root, owner, branch string, files map[string]fixture) string {
	t.Helper()
	checkout := filepath.Join(root, owner, "worktrees", branch)
	require.NoError(t, os.MkdirAll(checkout, 0o755))
	return newTarget(t, checkout, files)
}

func writeFile(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	if age > 0 {
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, when, when))
	}
}

func deps(name string) string { return filepath.Join("debug", depsDir, name) }

// A cap is a cap on space. A test says how much of the tree it wants freed
// and lets the filesystem's own numbers set the limit.
func totalOf(t *testing.T, target string) int64 {
	t.Helper()
	plan, err := Inspect(Target{Path: target}, 1<<62)
	require.NoError(t, err)
	require.Empty(t, plan.Recycle, "a cap nobody can exceed still chose entries")
	return plan.Size
}

// unit is what one fixture file costs on this filesystem.
func unit(t *testing.T, path string) int64 {
	t.Helper()
	size := DiskSize(path)
	require.Greater(t, size, int64(0), "%s takes no space, so no cap can be derived from it", path)
	return size
}

// capAfterFreeing is the cap that leaves a plan freeing bytes and no more.
func capAfterFreeing(total, bytes int64) int64 { return total - bytes }

// markers is what both files identifying a cargo target directory take: they are
// never recyclable, so they are the floor a plan cannot get below.
func markers(t *testing.T, target string) int64 {
	t.Helper()
	return DiskSize(filepath.Join(target, cacheDirTag)) + DiskSize(filepath.Join(target, rustcInfo))
}

// A recordingBin stands in for the platform bin. It moves what it is given into a
// directory of its own and remembers the order it was asked in.
type recordingBin struct {
	dir   string
	taken []string
	err   error

	// perm answers with a permanent disposal, which is what a bin with nowhere to defer a deletion does.
	perm bool
}

func newBin(t *testing.T) *recordingBin {
	t.Helper()
	return &recordingBin{dir: t.TempDir()}
}

func (b *recordingBin) Recycle(paths []string) ([]bin.Disposal, error) {
	if b.err != nil {
		return nil, b.err
	}
	var disposals []bin.Disposal
	for _, path := range paths {
		if err := os.Rename(path, filepath.Join(b.dir, filepath.Base(path))); err != nil {
			return nil, err
		}
		b.taken = append(b.taken, path)
		disposals = append(disposals, bin.Disposal{Path: path, Permanent: b.perm})
	}
	return disposals, nil
}

func (b *recordingBin) List() ([]bin.Item, error)              { return nil, nil }
func (b *recordingBin) Restore(string, string) (string, error) { return "", nil }
func (b *recordingBin) Evict(string) error                     { return nil }
func (b *recordingBin) Dirs() []string                         { return []string{b.dir} }

// roomy is a filesystem with somewhere to defer a recycle into.
func roomy(string) (uint64, uint64, error) { return 100 << 30, 1000 << 30, nil }

// tiers lists the kind of each entry of a plan, in the order it would go.
func tiers(plan Plan) []Tier {
	out := make([]Tier, 0, len(plan.Recycle))
	for _, c := range plan.Recycle {
		out = append(out, c.Tier)
	}
	return out
}

func bases(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, filepath.Base(c.Path))
	}
	return out
}

func rel(t *testing.T, base, path string) string {
	t.Helper()
	out, err := filepath.Rel(base, path)
	require.NoError(t, err)
	return out
}

// treeOf records every path, kind and size under root, so a test can prove a run
// changed nothing.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		where, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		tree[where] = fmt.Sprintf("%s %d", info.Mode(), info.Size())
		return nil
	})
	require.NoError(t, err)
	return tree
}

func TestATargetInsideTheCapIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "big.o"): {size: 400, age: old},
		deps("libcrate-1-000000.rlib"):                             {size: 400, age: fresh},
	})
	b := newBin(t)

	outcome, err := Trim(root, Options{Cap: 2 * totalOf(t, target), Bin: b, Free: roomy})
	require.NoError(t, err)
	assert.Empty(t, outcome.Plans, "a directory already inside the cap produced a plan")
	assert.Zero(t, outcome.Recycled)
	assert.Empty(t, b.taken, "a target with room had something recycled anyway")
	assert.FileExists(t, filepath.Join(target, deps("libcrate-1-000000.rlib")))
}

// The incremental cache is the part of a target directory that puts no bound on its
// own growth. It goes before any artifact the build is reading.
func TestTheIncrementalCacheGoesBeforeAnythingNewer(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "stale-1", "big.o"): {size: 400, age: old},
		deps("libcrate-1-000000.rlib"):                             {size: 400, age: fresh},
	})
	total, one := totalOf(t, target), unit(t, filepath.Join(target, deps("libcrate-1-000000.rlib")))

	// One entry's worth of room is all the cap gives back, so the first entry chosen is the whole answer.
	plan, err := Inspect(Target{Path: target}, capAfterFreeing(total, one))
	require.NoError(t, err)
	require.False(t, plan.Room())
	require.Len(t, plan.Recycle, 1, "the incremental cache alone reaches the cap")
	assert.Equal(t, Incremental, plan.Recycle[0].Tier)
	assert.Equal(t, filepath.Join("debug", incrementalDir, "stale-1"), rel(t, target, plan.Recycle[0].Path))
	assert.Equal(t, one, plan.Reclaimed)
	assert.Zero(t, plan.Shortfall())
}

// Inside one deps directory the oldest artifacts go first, which is what leaves a
// build in progress its own inputs.
func TestDepsGoLeastRecentlyUsedFirst(t *testing.T) {
	root := t.TempDir()
	names := []string{"lib-a-000000.rlib", "lib-b-000000.rlib", "lib-c-000000.rlib", "lib-d-000000.rlib"}
	target := newTarget(t, root, map[string]fixture{
		deps(names[0]): {size: 300, age: 300 * time.Hour},
		deps(names[1]): {size: 300, age: 200 * time.Hour},
		deps(names[2]): {size: 300, age: 100 * time.Hour},
		deps(names[3]): {size: 300, age: fresh},
	})
	total, one := totalOf(t, target), unit(t, filepath.Join(target, deps(names[0])))

	plan, err := Inspect(Target{Path: target}, capAfterFreeing(total, 2*one))
	require.NoError(t, err)
	assert.Equal(t, names[:2], bases(plan.Recycle),
		"the two oldest artifacts are what a cap reaches first")
	assert.Zero(t, plan.Shortfall())
}

// An entry a tier has already chosen is not counted twice when a later tier takes
// the whole profile directory around it.
func TestAByteIsCountedOnceAcrossTiers(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "big.o"): {size: 400, age: old},
		filepath.Join("debug", "build-script.bin"):                 {size: 600, age: fresh},
	})
	total, rest := totalOf(t, target), markers(t, target)

	// The cache alone cannot reach the cap, so the profile directory holding it goes too.
	plan, err := Inspect(Target{Path: target, Worktree: "branch"}, rest)
	require.NoError(t, err)
	assert.Equal(t, []Tier{Incremental, Profiles}, tiers(plan))
	assert.Equal(t, total-rest, plan.Reclaimed)
	assert.Zero(t, plan.Shortfall())
}

// The tiers are a priority order and not a set: the safest kind is emptied before
// the next one is touched at all.
func TestTheTiersAreOrderedSafestFirst(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "a.o"):            {size: 100, age: old},
		deps("libcrate-1-000000.rlib"):                                      {size: 100, age: old},
		filepath.Join("debug", fingerprintDir, "crate-1-000000", "dep-bin"): {size: 100, age: old},
		filepath.Join("debug", "unmatched.bin"):                             {size: 100, age: old},
	})

	plan, err := Inspect(Target{Path: target, Worktree: "branch"}, 0)
	require.NoError(t, err)
	got := tiers(plan)
	assert.True(t, slices.IsSortedFunc(got, func(a, b Tier) int { return int(a) - int(b) }),
		"the plan takes %v, which is not safest first", got)
	for _, want := range []Tier{Incremental, Deps, Fingerprints, Profiles} {
		assert.Contains(t, got, want)
	}
}

// A whole build profile directory is only ever taken from a worktree. A merged or
// deleted branch's target is pure junk, and a main checkout's is somebody's next
// build.
func TestAWholeProfileDirIsTakenFromAWorktreeOnly(t *testing.T) {
	files := map[string]fixture{filepath.Join("debug", "old.bin"): {size: 500, age: old}}

	worktree := newWorktreeTarget(t, t.TempDir(), ".claude", "branchy", files)
	plan, err := Inspect(Target{Path: worktree, Worktree: "branchy"}, 1)
	require.NoError(t, err)
	assert.Equal(t, []Tier{Profiles}, tiers(plan),
		"a worktree's profile directory is the last resort available")

	main := t.TempDir()
	newTarget(t, main, files)
	mainPlan, err := Inspect(Target{Path: filepath.Join(main, targetName)}, 1)
	require.NoError(t, err)
	assert.Empty(t, mainPlan.Recycle, "a main checkout's profile directory is never taken whole")
	assert.Greater(t, mainPlan.Shortfall(), int64(0), "and the cap saying it cannot get there is the honest report")
}

// The worktree copies are where the growth comes from, so finding them is most of
// the mechanism.
func TestWorktreeTargetsAreFound(t *testing.T) {
	root := t.TempDir()
	small := map[string]fixture{deps("libcrate-1-000000.rlib"): {size: 10, age: old}}
	newWorktreeTarget(t, root, ".claude", "one", small)
	newWorktreeTarget(t, root, ".grok", "two", small)
	newTarget(t, root, small)

	got, err := Find(root)
	require.NoError(t, err)
	require.Len(t, got, 3, "a worktree's target directory was missed")
	for _, target := range got {
		assert.Equal(t, targetName, filepath.Base(target.Path))
	}
	// Path order puts both worktrees before the main checkout.
	assert.Equal(t, []string{"one", "two", ""}, []string{got[0].Worktree, got[1].Worktree, got[2].Worktree})
	assert.Equal(t, filepath.Join(root, targetName), got[2].Path)
}

// A directory called target is not cargo's: Maven builds into one. A Makefile names
// a phony target's directory one, and a checkout's own source may hold one.
func TestADirectoryNamedTargetThatCargoNeverWroteIsNeverTouched(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"maven/target", "notes/target", "src/target"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		writeFile(t, filepath.Join(full, depsDir, "thing.o"), "not build output", old)
		writeFile(t, filepath.Join(full, "classes", "Thing.class"), "not build output", old)
	}

	got, err := Find(root)
	require.NoError(t, err)
	assert.Empty(t, got, "a directory named target with nothing cargo writes in it was taken for one")

	_, err = Inspect(Target{Path: filepath.Join(root, "maven", targetName)}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a cargo target directory")
	assert.FileExists(t, filepath.Join(root, "maven", targetName, depsDir, "thing.o"),
		"refusing a directory still disturbed it")
}

// What a bin holds is already recycled. Recycling it again puts it in the bin twice
// and loses where it came from, so a scan does not enter one.
func TestARecycleBinIsNotScannedForBuildOutput(t *testing.T) {
	root := t.TempDir()
	small := map[string]fixture{deps("libcrate-1-000000.rlib"): {size: 10, age: old}}
	for _, dir := range []string{".Trash-501", ".Trashes", ".Trash"} {
		newTarget(t, filepath.Join(root, filepath.FromSlash(dir)), small)
	}

	got, err := Find(root)
	require.NoError(t, err)
	assert.Empty(t, got, "a scan reached into the recycle bin")
}

// A dry run is a measurement, not the rehearsal of a change.
func TestADryRunChangesNothing(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "big.o"): {size: 400, age: old},
		deps("libcrate-1-000000.rlib"):                             {size: 400, age: fresh},
	})
	before := treeOf(t, root)
	b := newBin(t)

	outcome, err := Trim(root, Options{Cap: 1, DryRun: true, Bin: b, Free: roomy})
	require.NoError(t, err)
	require.Len(t, outcome.Plans, 1)
	assert.NotEmpty(t, outcome.Plans[0].Recycle, "the dry run found nothing to report")
	assert.Greater(t, outcome.Planned(), int64(0))
	assert.Zero(t, outcome.Recycled, "a dry run reported having recycled something")
	assert.Zero(t, outcome.Reclaimed)
	assert.Empty(t, b.taken, "a dry run went to the bin")
	assert.Equal(t, before, treeOf(t, root), "the dry run changed the tree")
	assert.FileExists(t, filepath.Join(target, cacheDirTag))
}

// The real path: each entry reaches the bin, the newest last, and the directory
// itself and the markers that identify it stay where they are.
func TestTrimRecyclesThroughTheBin(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "a.o"): {size: 300, age: old},
		deps("lib-a-000000.rlib"):                                {size: 300, age: old},
		deps("lib-b-000000.rlib"):                                {size: 300, age: fresh},
	})
	total, one := totalOf(t, target), unit(t, filepath.Join(target, deps("lib-a-000000.rlib")))
	b := newBin(t)

	outcome, err := Trim(root, Options{Cap: capAfterFreeing(total, 2*one), Bin: b, Free: roomy})
	require.NoError(t, err)
	require.Len(t, outcome.Plans, 1)
	assert.Equal(t, 2, outcome.Recycled)
	assert.Equal(t, 2*one, outcome.Reclaimed)
	assert.Empty(t, outcome.Errors)
	assert.Empty(t, outcome.Refusals)
	assert.Equal(t, []string{
		filepath.Join(target, "debug", incrementalDir, "crate-1"),
		filepath.Join(target, deps("lib-a-000000.rlib")),
	}, b.taken, "the bin got the oldest of each kind")

	assert.NoFileExists(t, filepath.Join(target, deps("lib-a-000000.rlib")))
	assert.FileExists(t, filepath.Join(target, deps("lib-b-000000.rlib")),
		"the newest artifact was taken as well as the oldest")
	assert.FileExists(t, filepath.Join(target, cacheDirTag), "the marker that says this tree is a cache was taken")
	assert.FileExists(t, filepath.Join(target, rustcInfo))
	assert.DirExists(t, target, "the target directory itself was removed")
}

// An entry bigger than what the filesystem has left cannot be deferred into a bin:
// the backends would delete it. This package declines instead.
func TestAnEntryTooBigForTheBinIsRefused(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		deps("small-000000.rlib"): {size: 100, age: old},
		deps("huge-000000.rlib"):  {size: 600_000, age: fresh},
	})
	b := newBin(t)
	// Enough room to defer a small file and nowhere near enough for the large one.
	tight := func(string) (uint64, uint64, error) { return (1 << 19) + 1, 1 << 20, nil }

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: tight})
	require.NoError(t, err)
	assert.Equal(t, 1, outcome.Recycled, "the entry that fits was not recycled")
	require.Len(t, outcome.Refusals, 1)
	assert.Contains(t, outcome.Refusals[0].Path, "huge-000000.rlib")
	assert.Contains(t, outcome.Refusals[0].Reason, "does not fit")
	assert.FileExists(t, filepath.Join(target, deps("huge-000000.rlib")), "a refused entry was moved anyway")
	assert.NoFileExists(t, filepath.Join(target, deps("small-000000.rlib")))
}

// Under the floor a bin needs, every recycle the backends make is a deletion, so
// the whole target directory is declined.
func TestAFilesystemUnderTheBinFloorIsDeclined(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	b := newBin(t)
	full := func(string) (uint64, uint64, error) { return 1 << 20, 1000 << 30, nil }

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: full})
	require.NoError(t, err)
	require.Len(t, outcome.Plans, 1)
	assert.Zero(t, outcome.Recycled)
	assert.Empty(t, b.taken)
	require.Len(t, outcome.Refusals, 1)
	assert.Equal(t, target, outcome.Refusals[0].Path)
	assert.Contains(t, outcome.Refusals[0].Reason, "under the")
	assert.FileExists(t, filepath.Join(target, deps("lib-a-000000.rlib")))
}

// A filesystem that cannot be measured is not a licence either.
func TestAnUnmeasurableFilesystemStopsThePlan(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	b := newBin(t)
	blind := func(string) (uint64, uint64, error) { return 0, 0, errors.New("no such filesystem") }

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: blind})
	require.NoError(t, err)
	assert.Zero(t, outcome.Recycled)
	assert.Empty(t, b.taken)
	require.Len(t, outcome.Errors, 1)
	assert.Contains(t, outcome.Errors[0].Error(), target)
	assert.FileExists(t, filepath.Join(target, deps("lib-a-000000.rlib")))
}

// A bin that reports a permanent disposal has deleted the entry. That is said out
// loud rather than counted as a successful recycle.
func TestAPermanentDisposalIsReportedNotCounted(t *testing.T) {
	root := t.TempDir()
	newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	b := newBin(t)
	b.perm = true

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: roomy})
	require.NoError(t, err)
	assert.Zero(t, outcome.Recycled)
	assert.Zero(t, outcome.Reclaimed)
	require.Len(t, outcome.Refusals, 1)
	assert.Contains(t, outcome.Refusals[0].Reason, "permanently")
}

func TestABinFailureIsCarriedWithoutStoppingTheRun(t *testing.T) {
	root := t.TempDir()
	newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	b := newBin(t)
	b.err = errors.New("read-only volume")

	outcome, err := Trim(root, Options{Cap: 1, Bin: b, Free: roomy})
	require.NoError(t, err)
	require.Len(t, outcome.Errors, 1)
	assert.Contains(t, outcome.Errors[0].Error(), "read-only volume")
	assert.Zero(t, outcome.Recycled)
}

// An entry taken by something else between the walk and the recycle is not a
// failure to report, and is not counted as reclaimed.
func TestAnEntryAlreadyGoneIsSkipped(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", incrementalDir, "crate-1", "a.o"): {size: 300, age: old},
		deps("lib-a-000000.rlib"):                                {size: 300, age: old},
	})
	plan, err := Inspect(Target{Path: target}, 1)
	require.NoError(t, err)
	require.Len(t, plan.Recycle, 2)
	one := unit(t, filepath.Join(target, deps("lib-a-000000.rlib")))

	require.NoError(t, os.Remove(filepath.Join(target, deps("lib-a-000000.rlib"))))
	b := newBin(t)
	out := &Outcome{}
	recycled, bytes := apply(b, &plan, roomy, out)

	assert.Equal(t, 1, recycled)
	assert.Equal(t, one, bytes)
	assert.Empty(t, out.Errors)
	assert.Equal(t, []string{filepath.Join(target, "debug", incrementalDir, "crate-1")}, b.taken)
}

// A symlink inside a target directory stands for bytes that are not its own, and
// for something outside it that cargo did not write.
func TestSymlinksAreNeitherCountedNorRecycled(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{deps("lib-a-000000.rlib"): {size: 300, age: old}})
	outsider := filepath.Join(root, "outsider.rlib")
	writeFile(t, outsider, strings.Repeat("y", 4000), old)
	link := filepath.Join(target, deps("libfoo.dylib"))
	require.NoError(t, os.Symlink(outsider, link))

	plan, err := Inspect(Target{Path: target}, 1)
	require.NoError(t, err)
	assert.NotContains(t, bases(plan.Recycle), "libfoo.dylib")
	// The same walk without the symlink weighs the same.
	require.NoError(t, os.Remove(link))
	assert.Equal(t, totalOf(t, target), plan.Size)

	// A symlinked directory is not descended into, either.
	other := t.TempDir()
	newTarget(t, other, map[string]fixture{deps("x.rlib"): {size: 1, age: old}})
	require.NoError(t, os.Symlink(other, filepath.Join(root, "link")))
	got, err := Find(root)
	require.NoError(t, err)
	require.Len(t, got, 1, "a symlinked directory was scanned as a second tree")
	assert.Equal(t, target, got[0].Path)
}

func TestIsCargoTargetNeedsCargoAndNotMerelyTheName(t *testing.T) {
	root := t.TempDir()

	cargo := filepath.Join(root, targetName)
	require.NoError(t, os.MkdirAll(filepath.Join(cargo, "release"), 0o755))
	assert.False(t, IsCargoTarget(cargo), "an empty directory called target is not cargo's")

	writeFile(t, filepath.Join(cargo, cacheDirTag), "Signature: 8a477f597d28d172789f06886806bc55\n", 0)
	assert.False(t, IsCargoTarget(cargo), "CACHEDIR.TAG is every backup tool's format, not cargo's alone")

	writeFile(t, filepath.Join(cargo, "release", depsDir, "libcrate-1.rlib"), "x", old)
	assert.True(t, IsCargoTarget(cargo))

	writeFile(t, filepath.Join(root, "elsewhere.json"), "{}", old)
	assert.False(t, IsCargoTarget(filepath.Join(root, "elsewhere.json")), "a file is not a directory")

	// A build directory renamed to something else is not a place cargo writes its markers.
	renamed := filepath.Join(root, "build")
	writeFile(t, filepath.Join(renamed, rustcInfo), "{}", old)
	assert.False(t, IsCargoTarget(renamed))
}

func TestWorktreeOfNamesTheBranchAndOnlyAnAgentWorktree(t *testing.T) {
	for _, want := range []struct {
		path, worktree string
	}{
		{"/repo/.claude/worktrees/fix-bug/target", "fix-bug"},
		{"/repo/.grok/worktrees/agent-7/target", "agent-7"},
		{"/repo/target", ""},
		{"/repo/ci/worktrees/not-an-agent/target", ""},
		{"/repo/.claude/target", ""},
	} {
		assert.Equal(t, want.worktree, worktreeOf(want.path), want.path)
	}
}

// Entries with the same timestamp still produce the same plan twice, which is what
// makes a run reproducible and a test of one meaningful.
func TestAPlanIsTheSamePlanTwice(t *testing.T) {
	root := t.TempDir()
	names := []string{"lib-z-000000.rlib", "lib-a-000000.rlib", "lib-m-000000.rlib"}
	files := map[string]fixture{}
	for _, name := range names {
		files[deps(name)] = fixture{size: 100, age: old}
	}
	target := newTarget(t, root, files)

	// The tie-break is only reached by entries whose timestamps are truly equal.
	when := time.Now().Add(-old).Truncate(time.Second)
	for _, name := range names {
		path := filepath.Join(target, deps(name))
		require.NoError(t, os.Chtimes(path, when, when))
	}
	total, one := totalOf(t, target), unit(t, filepath.Join(target, deps(names[0])))

	lookedAt := Target{Path: target}
	first, err := Inspect(lookedAt, capAfterFreeing(total, 2*one))
	require.NoError(t, err)
	second, err := Inspect(lookedAt, capAfterFreeing(total, 2*one))
	require.NoError(t, err)
	require.Equal(t, first.Recycle, second.Recycle)
	assert.Equal(t, []string{"lib-a-000000.rlib", "lib-m-000000.rlib"}, bases(first.Recycle),
		"a shared timestamp has to break toward a path, not toward directory order")
}

func TestFindRefusesAFileAndReportsAMissingDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "notes.txt")
	writeFile(t, file, "notes", old)

	_, err := Find(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")

	_, err = Find(filepath.Join(root, "absent"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist), "a missing root is not the error a caller can act on: %v", err)
}

func TestParseCapAcceptsEverySpellingOfASize(t *testing.T) {
	for _, want := range []struct {
		raw  string
		want int64
	}{
		{"4294967296", 4 << 30},
		{"4G", 4 << 30},
		{"4g", 4 << 30},
		{"4GB", 4 << 30},
		{"4GiB", 4 << 30},
		{"4 GiB", 4 << 30},
		{"512M", 512 << 20},
		{"1.5G", 1<<30 + 1<<29},
		{"1T", 1 << 40},
		{"2048B", 2048},
		{"0", 0},
	} {
		got, err := ParseCap(want.raw)
		require.NoError(t, err, want.raw)
		assert.Equal(t, want.want, got, want.raw)
	}

	for _, bad := range []string{"", "   ", "G", "abc", "4X", "-1G", "10000000T", "4 G B"} {
		_, err := ParseCap(bad)
		assert.Error(t, err, "ParseCap(%q) took it", bad)
	}
}

func TestCapFromEnvAnswersToTheVariable(t *testing.T) {
	assert.Equal(t, DefaultCap, CapFromEnv(DefaultCap), "an unset variable is the default")

	t.Setenv(capEnv, "1G")
	assert.Equal(t, int64(1<<30), CapFromEnv(DefaultCap))

	t.Setenv(capEnv, "not a size")
	assert.Equal(t, DefaultCap, CapFromEnv(DefaultCap), "a broken value falls back to the cap that still works")
}

// A plan has to be able to say the cap is out of reach. Reporting a directory as
// trimmed while it sits over the cap would be a lie about the disk.
func TestAPlanSaysWhenTheCapIsOutOfReach(t *testing.T) {
	root := t.TempDir()
	target := newTarget(t, root, map[string]fixture{
		filepath.Join("debug", "keepme.bin"): {size: 900, age: fresh},
	})

	plan, err := Inspect(Target{Path: target}, 1)
	require.NoError(t, err)
	assert.Empty(t, plan.Recycle, "a file outside every cargo accumulation is not eligible")
	assert.False(t, plan.Room())
	assert.Equal(t, plan.Size-1, plan.Shortfall())
	assert.Greater(t, plan.Shortfall(), markers(t, target))
}

func TestDiskSizeCountsAFilesSpaceAndNotItsLength(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small")
	tiny := filepath.Join(root, "tiny")
	writeFile(t, small, strings.Repeat("x", 100_000), 0)
	writeFile(t, tiny, "x", 0)
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(small, link))

	// Every file takes at least a block, and a larger one takes at least as much of them.
	big, little := DiskSize(small), DiskSize(tiny)
	assert.Greater(t, little, int64(0), "a file with bytes in it takes no space")
	assert.GreaterOrEqual(t, big, little)
	assert.Zero(t, DiskSize(link), "a symlink was weighed as what it stands for")
	assert.Zero(t, DiskSize(filepath.Join(root, "absent")), "a path that is not there has nothing to reclaim")

	dir := filepath.Join(root, "tree")
	writeFile(t, filepath.Join(dir, "a"), "aaaa", 0)
	writeFile(t, filepath.Join(dir, "deeper", "b"), "bbbb", 0)
	assert.Equal(t, DiskSize(filepath.Join(dir, "a"))+DiskSize(filepath.Join(dir, "deeper", "b")), DiskSize(dir),
		"a directory was weighed as something other than the sum of its files")
}
