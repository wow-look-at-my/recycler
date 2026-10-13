// Package cargotarget keeps a cargo build directory inside a size cap.
//
// rustc only ever adds to a target directory. Every branch, worktree, dependency
// bump and feature or profile permutation leaves its object files, rlibs,
// fingerprints and incremental cache. Behind, and nothing takes any of it back:
// cargo has no garbage collection at all. Its one remedy is "cargo clean", which
// throws away the whole directory and costs a full rebuild of everything still
// wanted. A machine where several agents each build in their own worktree
// reaches tens of gigabytes with nobody asking for them.
//
// This package takes the middle ground: it recycles the oldest regenerable
// output inside a target directory until the directory is back under a cap. The
// build in progress keeps its cache and the rest stays recoverable from the
// recycle bin. Cargo rebuilds everything it takes on the next build.
package cargotarget

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/pressure"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

const (
	// DefaultCap is what one target directory is allowed to hold.
	DefaultCap int64 = 4 << 30

	// capEnv overrides [DefaultCap] for a machine whose disks disagree with it, spelled as [ParseCap] takes it.
	capEnv = "RECYCLER_CARGO_TARGET_CAP"

	// targetName is the directory cargo builds into.
	targetName = "target"

	// cacheDirTag is the file marking a whole tree as disposable to a backup tool.
	cacheDirTag = "CACHEDIR.TAG"

	// rustcInfo records the compiler probe cargo runs once per target directory. Nothing but cargo writes that name.
	rustcInfo = ".rustc_info.json"

	depsDir        = "deps"
	fingerprintDir = ".fingerprint"
	incrementalDir = "incremental"
)

// A Tier ranks what may leave a target directory, safest first.
type Tier int

const (
	// Incremental is cargo's incremental compilation cache.
	Incremental Tier = iota

	// Deps holds the object files, rlibs, metadata, linkable executables and their .d files.
	Deps

	// Fingerprints holds the per-crate records that decide whether cargo rebuilds something.
	Fingerprints

	// Profiles is a whole build profile directory.
	Profiles
)

func (t Tier) String() string {
	switch t {
	case Incremental:
		return "incremental"
	case Deps:
		return "deps"
	case Fingerprints:
		return "fingerprints"
	case Profiles:
		return "profiles"
	}
	return "unknown"
}

// A Target is one cargo build directory.
type Target struct {
	Path string

	// Worktree names the agent worktree the checkout lives in, empty for a main checkout.
	Worktree string
}

// IsCargoTarget reports whether dir is cargo's build directory rather than a
// directory that happens to be called target. Maven, Make and a checkout's own
// source all use that name. The name is necessary and not enough: the
// directory has to hold something only cargo writes.
func IsCargoTarget(dir string) bool {
	if filepath.Base(filepath.Clean(dir)) != targetName {
		return false
	}
	if _, err := os.Lstat(filepath.Join(dir, rustcInfo)); err == nil {
		return true
	}
	if !exists(filepath.Join(dir, cacheDirTag)) {
		return false
	}
	// CACHEDIR.TAG is another tool's format as much as cargo's.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		profile := filepath.Join(dir, e.Name())
		if exists(filepath.Join(profile, fingerprintDir)) ||
			exists(filepath.Join(profile, depsDir)) ||
			exists(filepath.Join(profile, incrementalDir)) {
			return true
		}
	}
	return false
}

// Find returns every cargo target directory under root, path order first.
//
// Symlinks are never followed, and a directory on another filesystem than root
// is not entered. A scan of a source tree must not wander into a mounted volume,
// and what it does not descend into it cannot recycle.
func Find(root string) ([]Target, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("recycler: %s is not a directory", abs)
	}
	rootDev, bounded := deviceOf(rootInfo)

	var found []Target
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// One unreadable directory hides nothing else: keep scanning.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == abs {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fs.SkipDir
		}
		if bounded {
			if dev, ok := deviceOf(info); !ok || dev != rootDev {
				return fs.SkipDir
			}
		}
		if skipTrees(d.Name()) {
			return fs.SkipDir
		}
		if d.Name() != targetName || !IsCargoTarget(path) {
			return nil
		}
		found = append(found, Target{Path: path, Worktree: worktreeOf(path)})
		// Everything below a target directory is that target directory's.
		return fs.SkipDir
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b Target) int { return strings.Compare(a.Path, b.Path) })
	return found, nil
}

// skipTrees names directories a scan of source trees never enters.
func skipTrees(name string) bool {
	switch {
	case strings.HasPrefix(name, ".Trash"):
		return true
	case name == ".git", name == ".hg", name == ".svn", name == "node_modules":
		return true
	}
	return false
}

// worktreeOf returns the agent worktree a target directory belongs to, or the
// empty string for a main checkout. An agent's worktree sits at
// <repo>/<owner>/worktrees/<branch>, where the owner is the tool that made it.
func worktreeOf(path string) string {
	branch := filepath.Dir(path)
	worktrees := filepath.Dir(branch)
	owner := filepath.Dir(worktrees)
	if filepath.Base(worktrees) != "worktrees" {
		return ""
	}
	switch filepath.Base(owner) {
	case ".claude", ".grok":
		return filepath.Base(branch)
	}
	return ""
}

// A Candidate is one entry that may leave a target directory.
type Candidate struct {
	Path string
	Size int64
	Tier Tier

	// Last is the newest modification time of anything inside it.
	Last time.Time
}

// A Plan is one target directory's account against the cap. Building it reads
// and decides; it changes nothing.
type Plan struct {
	Target Target

	// Size is every byte under the target directory, and Cap what it is allowed to hold.
	Size int64
	Cap  int64

	// Recycle holds what would go, in the order it would go in.
	Recycle   []Candidate
	Reclaimed int64

	// Recycled and Applied are what reached the bin, and their bytes measured on the way out.
	Recycled int
	Applied  int64
}

// Room reports whether the directory already fits inside the cap.
func (p Plan) Room() bool { return p.Size <= p.Cap }

// Shortfall is how far the directory will still sit over the cap once the
// plan has run: what the cap cannot reach.
func (p Plan) Shortfall() int64 {
	if left := p.Size - p.Reclaimed - p.Cap; left > 0 {
		return left
	}
	return 0
}

// Inspect measures a target directory and chooses what would have to go to bring
// it back inside cap. It writes nothing anywhere.
func Inspect(target Target, cap int64) (Plan, error) {
	if !IsCargoTarget(target.Path) {
		return Plan{}, fmt.Errorf("recycler: refusing to trim %s: it is not a cargo target directory", target.Path)
	}
	plan := Plan{Target: target, Cap: cap}

	var accumulators []*accumulator
	byPath := map[string]*accumulator{}
	add := func(path string, tier Tier, size int64, when time.Time) {
		acc := byPath[path]
		if acc == nil {
			acc = &accumulator{path: path, tier: tier}
			byPath[path] = acc
			accumulators = append(accumulators, acc)
		}
		acc.size += size
		if when.After(acc.last) {
			acc.last = when
		}
	}

	err := filepath.WalkDir(target.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An entry that cannot be read is a hole in the measurement, not a reason
			// to give up on the directory.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == target.Path {
			return nil
		}
		// A symlink counts for nothing and is never a candidate: the bytes are not
		// its own, and what it points at is not necessarily regenerable.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		size := sizeOf(info)
		plan.Size += size
		rel, err := filepath.Rel(target.Path, path)
		if err != nil {
			return nil
		}
		for _, c := range candidatesFor(target, rel) {
			add(c.Path, c.Tier, size, info.ModTime())
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	if plan.Room() {
		return plan, nil
	}

	ordered := make([]Candidate, 0, len(accumulators))
	for _, acc := range accumulators {
		ordered = append(ordered, Candidate{Path: acc.path, Size: acc.size, Tier: acc.tier, Last: acc.last})
	}
	// The safest kind first, then the least recently used inside it, then the
	// path, so entries sharing a timestamp still produce the same plan twice.
	slices.SortFunc(ordered, func(a, b Candidate) int {
		if a.Tier != b.Tier {
			return int(a.Tier) - int(b.Tier)
		}
		if !a.Last.Equal(b.Last) {
			return a.Last.Compare(b.Last)
		}
		return strings.Compare(a.Path, b.Path)
	})

	remaining := plan.Size
	for _, c := range ordered {
		if remaining <= plan.Cap {
			break
		}
		// A profile directory chosen here may still contain entries already chosen from an earlier tier.
		fresh := c.Size - covered(c.Path, plan.Recycle)
		if fresh <= 0 {
			continue
		}
		c.Size = fresh
		plan.Recycle = append(plan.Recycle, c)
		plan.Reclaimed += fresh
		remaining -= fresh
	}
	return plan, nil
}

// An accumulator sums the files belonging to one candidate.
type accumulator struct {
	path string
	tier Tier
	size int64
	last time.Time
}

// candidatesFor names the entries one file under a target directory is part of.
func candidatesFor(target Target, rel string) []Candidate {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	candidates := make([]Candidate, 0, 2)
	if c, ok := primaryCandidate(target.Path, parts); ok {
		candidates = append(candidates, c)
	}
	// The marker files at the top of the directory are excluded. They are what
	// makes it validate as a cargo target directory. A plan that took them
	// would leave the rest of the tree invisible to the next scan.
	if target.Worktree != "" && parts[0] != cacheDirTag && parts[0] != rustcInfo {
		candidates = append(candidates, Candidate{
			Path: filepath.Join(append([]string{target.Path}, parts[0])...),
			Tier: Profiles,
		})
	}
	return candidates
}

// primaryCandidate returns the entry a file is counted into inside cargo's own
// accumulations. Each kind goes at the granularity cargo itself thinks in.
// Whole units for the caches keyed by hash, one file at a time for build
// artifacts.
func primaryCandidate(root string, parts []string) (Candidate, bool) {
	for i, part := range parts {
		switch part {
		case incrementalDir, fingerprintDir:
			if i+2 > len(parts) {
				continue
			}
			tier := Incremental
			if part == fingerprintDir {
				tier = Fingerprints
			}
			return Candidate{
				Path: filepath.Join(append([]string{root}, parts[:i+2]...)...),
				Tier: tier,
			}, true
		case depsDir:
			return Candidate{
				Path: filepath.Join(append([]string{root}, parts...)...),
				Tier: Deps,
			}, true
		}
	}
	return Candidate{}, false
}

// covered adds up what has already been chosen inside path, so a byte is never
// counted twice in one plan.
func covered(path string, chosen []Candidate) int64 {
	prefix := path + string(filepath.Separator)
	var total int64
	for _, c := range chosen {
		if strings.HasPrefix(c.Path, prefix) {
			total += c.Size
		}
	}
	return total
}

// A Refusal is something this package declined to recycle, and why.
type Refusal struct {
	Path   string
	Reason string
}

// An Outcome is the whole account of one run over one root.
type Outcome struct {
	// Plans holds an entry for every target directory found over the cap, dry-run or not, in the order they were found.
	Plans []Plan

	// Recycled counts the entries that reached the recycle bin, and Reclaimed their bytes as measured on the way out.
	Recycled  int
	Reclaimed int64

	Refusals []Refusal
	Errors   []error
}

// Planned is what the plans would take back, which is the number a dry run has
// to report in place of [Outcome.Reclaimed].
func (o *Outcome) Planned() int64 {
	var total int64
	for _, p := range o.Plans {
		total += p.Reclaimed
	}
	return total
}

// Options configures a [Trim].
type Options struct {
	// Cap is what one target directory may hold. Zero takes [DefaultCap], or [capEnv].
	Cap int64

	// DryRun measures and chooses, and touches nothing.
	DryRun bool

	// Free reads the available and total bytes behind a path.
	Free func(path string) (avail, total uint64, err error)

	// Bin is the recycle bin to move entries into. It is nil for this platform's.
	Bin bin.Backend
}

// Trim brings every cargo target directory under root back inside its cap,
// recycling the oldest regenerable output first and newest last. The build in
// progress keeps its cache.
//
// Nothing here is ever deleted: an entry goes through the recycle bin like any
// other path the tool takes. That is also why a target directory on a filesystem
// under the daemon's floor is refused rather than trimmed. A bin sits on the
// filesystem it takes from, so a move there frees nothing. The backends turn a
// move that cannot be deferred into a deletion. Build output that cargo writes
// again is not worth breaking that promise over, and the refusal says so.
func Trim(root string, opts Options) (*Outcome, error) {
	if opts.Cap == 0 {
		opts.Cap = CapFromEnv(DefaultCap)
	}
	free := opts.Free
	if free == nil {
		free = diskfree.Free
	}
	targets, err := Find(root)
	if err != nil {
		return nil, err
	}

	out := &Outcome{}
	b := opts.Bin
	if b == nil && !opts.DryRun {
		if b, err = trash.Backend(); err != nil {
			return nil, err
		}
	}

	for _, target := range targets {
		plan, err := Inspect(target, opts.Cap)
		if err != nil {
			out.Errors = append(out.Errors, err)
			continue
		}
		if plan.Room() {
			continue
		}
		out.Plans = append(out.Plans, plan)
		if opts.DryRun {
			continue
		}
		applied := &out.Plans[len(out.Plans)-1]
		applied.Recycled, applied.Applied = apply(b, applied, free, out)
		out.Recycled += applied.Recycled
		out.Reclaimed += applied.Applied
	}
	return out, nil
}

// CapFromEnv reads [capEnv], falling back to def when it is unset or unusable.
func CapFromEnv(def int64) int64 {
	raw := os.Getenv(capEnv)
	if raw == "" {
		return def
	}
	cap, err := ParseCap(raw)
	if err != nil {
		return def
	}
	return cap
}

// EnvCapName is the variable [CapFromEnv] reads, for the CLI to name in its help.
const EnvCapName = capEnv

// apply recycles a plan's entries in order, re-measuring each as it goes. The
// plan's own numbers come from an earlier walk of the same tree. An earlier
// tier has already moved some of what a whole profile directory used to
// contain. It returns the count and bytes that reached the bin.
func apply(b bin.Backend, plan *Plan, free func(string) (uint64, uint64, error), out *Outcome) (int, int64) {
	avail, total, err := free(filepath.Dir(plan.Target.Path))
	if err != nil {
		// No measurement is not a licence to delete anything either.
		out.Errors = append(out.Errors, fmt.Errorf("recycler: measuring the filesystem holding %s: %w", plan.Target.Path, err))
		return 0, 0
	}
	if floor := pressure.Floor(total); avail < floor {
		out.Refusals = append(out.Refusals, Refusal{
			Path: plan.Target.Path,
			Reason: fmt.Sprintf("only %s free, under the %s a recycle bin needs anywhere to defer to",
				pressure.Bytes(avail), pressure.Bytes(floor)),
		})
		return 0, 0
	}

	var recycled int
	var bytes int64
	for _, c := range plan.Recycle {
		size := DiskSize(c.Path)
		if size <= 0 {
			// It went away between the walk and here, or it cannot be read.
			continue
		}
		if uint64(size) > avail {
			out.Refusals = append(out.Refusals, Refusal{
				Path: c.Path,
				Reason: fmt.Sprintf("%s does not fit in the %s free, so it would be deleted rather than recycled",
					pressure.Bytes(uint64(size)), pressure.Bytes(avail)),
			})
			continue
		}
		disposals, err := b.Recycle([]string{c.Path})
		if err != nil {
			out.Errors = append(out.Errors, err)
			continue
		}
		for _, d := range disposals {
			if d.Permanent {
				// Checked again after the fact: the bin has the last word on whether it could defer.
				out.Refusals = append(out.Refusals, Refusal{Path: d.Path, Reason: "recycled permanently: " + d.Reason})
				continue
			}
			recycled++
			bytes += size
		}
	}
	return recycled, bytes
}

// ParseCap reads a size cap: a plain byte count, or a number followed by a unit
// of B, K, M, G or T.
func ParseCap(raw string) (int64, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, errors.New("recycler: empty size")
	}
	body := strings.ToUpper(text)
	letters := strings.TrimSpace(strings.TrimLeft(body, "0123456789."))
	digits := strings.TrimSpace(strings.TrimRight(body, letters))
	unit := strings.TrimSuffix(strings.TrimSuffix(letters, "B"), "I")

	multiplier := int64(1)
	switch unit {
	case "", "B":
	case "K":
		multiplier = 1 << 10
	case "M":
		multiplier = 1 << 20
	case "G":
		multiplier = 1 << 30
	case "T":
		multiplier = 1 << 40
	default:
		return 0, fmt.Errorf("recycler: unknown size unit %q in %q", letters, raw)
	}

	var n float64
	if _, err := fmt.Sscanf(digits, "%g", &n); err != nil {
		return 0, fmt.Errorf("recycler: cannot read a size from %q", raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("recycler: a size cap cannot be negative: %q", raw)
	}
	if n > float64(math.MaxInt64)/float64(multiplier) {
		return 0, fmt.Errorf("recycler: size cap %q is beyond the largest number of bytes", raw)
	}
	return int64(n * float64(multiplier)), nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// sizeOf is the space an entry occupies.
func sizeOf(info fs.FileInfo) int64 {
	if blocks, ok := blocksOf(info); ok {
		return blocks
	}
	return info.Size()
}

// DiskSize is the space a file, or everything under a directory, takes on its
// filesystem. The numbers a [Plan] reports are in these units. A path that cannot
// be read, or has gone away, weighs nothing here: it has nothing to reclaim.
func DiskSize(path string) int64 {
	var total int64
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return sizeOf(info)
		}
		return 0
	}
	// Only regular files count, which is the same rule the walk of a target
	// directory applies: a directory entry's own block. Anything a symlink
	// stands for, is not build output.
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		total += sizeOf(info)
		return nil
	})
	if err != nil {
		return total
	}
	return total
}
