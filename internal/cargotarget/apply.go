package cargotarget

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/pressure"
)

// apply recycles a plan's entries, re-measuring each as it goes. The plan's
// own numbers come from an earlier walk of the same tree. An earlier tier has
// already moved some of what a whole profile directory used to contain. It
// returns the count and bytes that reached the bin.
//
// The entries go to the bin as one item. Each is renamed into a directory
// inside the target directory, keeping its path, and that directory is
// recycled. A deps directory alone can hold a vast number of files. One bin
// item each would cost a put back record each, and a listing of the bin as
// long as the tree.
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

	bundle := filepath.Join(plan.Target.Path, fsutil.UniqueName(bundlePrefix+time.Now().Format(bundleStamp), plan.Target.Path))
	var staged int
	var bytes int64
	for _, c := range plan.Recycle {
		size := DiskSize(c.Path)
		if size <= 0 {
			// It went away between the walk and here, or it cannot be read.
			continue
		}
		rel, err := filepath.Rel(plan.Target.Path, c.Path)
		if err != nil {
			out.Errors = append(out.Errors, err)
			continue
		}
		if err := stage(c.Path, filepath.Join(bundle, rel)); err != nil {
			out.Errors = append(out.Errors, fmt.Errorf("recycler: gathering %s for the bin: %w", c.Path, err))
			continue
		}
		staged++
		bytes += size
	}
	if staged == 0 {
		// Nothing moved, so the bundle holds at most the directories made for it.
		if err := os.RemoveAll(bundle); err != nil {
			out.Errors = append(out.Errors, err)
		}
		return 0, 0
	}

	// A bin on the target directory's own filesystem takes the bundle by rename.
	disposals, err := b.Recycle([]string{bundle})
	if err != nil {
		out.Errors = append(out.Errors, err)
		return 0, 0
	}
	for _, d := range disposals {
		if d.Permanent {
			// Checked again after the fact: the bin has the last word on whether it could defer.
			out.Refusals = append(out.Refusals, Refusal{Path: d.Path, Reason: "recycled permanently: " + d.Reason})
			return 0, 0
		}
	}
	return staged, bytes
}

// bundlePrefix and bundleStamp name the directory a run gathers entries into.
const (
	bundlePrefix = "recycler-trim-"
	bundleStamp  = "20060102-150405"
)

// stage renames src to dst, creating dst's parents. A directory whose
// destination already exists is merged into it: a profile directory goes
// after entries an earlier tier already took out of it.
func stage(src, dst string) error {
	dstInfo, err := os.Lstat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Rename(src, dst)
	}
	if err != nil {
		return err
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() || !dstInfo.IsDir() {
		return fmt.Errorf("%w: %s", bin.ErrExists, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := stage(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(src)
}
