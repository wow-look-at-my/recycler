// recycle.go holds Recycle.

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/pressure"
	"github.com/wow-look-at-my/recycler/internal/trash"
)

// Recycle moves each path into the bin. A path the bin has no room for costs
// the oldest items there first. Only a path those cannot cover is deleted.
func Recycle(paths []string) ([]bin.Disposal, error) {
	b, err := trash.Backend()
	if err != nil {
		return nil, err
	}
	return recycleInto(b, paths, freeAt, fsutil.SameFilesystem)
}

// recycleInto is Recycle against a stated bin and injected filesystem probes.
func recycleInto(b bin.Backend, paths []string,
	free func(string) (uint64, uint64, error), same func(a, b string) (bool, error),
) ([]bin.Disposal, error) {
	r := &room{b: b, free: free, same: same}
	order := make(map[string]int, len(paths))
	costs := make(map[string]bin.Disposal, len(paths))
	deleted := make([]bin.Disposal, 0, len(paths))
	rest := make([]string, 0, len(paths))
	var errs []error
	for i, path := range paths {
		if abs, err := filepath.Abs(path); err == nil {
			order[abs] = i
		}
		d, err := r.place(path)
		switch {
		case err != nil:
			errs = append(errs, err)
		case d.Permanent:
			deleted = append(deleted, d)
		case d.Path != "":
			costs[d.Path] = d
			rest = append(rest, d.Path)
		default:
			rest = append(rest, path)
		}
	}

	var moved []bin.Disposal
	if len(rest) > 0 {
		var err error
		if moved, err = b.Recycle(rest); err != nil {
			errs = append(errs, err)
		}
	}
	for i, d := range moved {
		if cost, ok := costs[d.Path]; ok {
			moved[i].Evicted, moved[i].Reason = cost.Evicted, cost.Reason
			delete(costs, d.Path)
		}
	}
	for path, cost := range costs {
		if len(cost.Evicted) > 0 {
			errs = append(errs, fmt.Errorf("recycling %s: the bin gave back %d items to make room, and the move then failed",
				path, len(cost.Evicted)))
		}
	}

	disposals := append(deleted, moved...)
	sort.SliceStable(disposals, func(i, j int) bool { return order[disposals[i].Path] < order[disposals[j].Path] })
	return disposals, errors.Join(errs...)
}

// room is the free space a batch of recycles leaves on each filesystem. Each
// number is read once and then accounted.
type room struct {
	b     bin.Backend
	free  func(string) (uint64, uint64, error)
	same  func(a, b string) (bool, error)
	disks []*disk

	items  []bin.Item
	listed bool
}

type disk struct {
	probe        string
	avail, total uint64
}

// on answers the filesystem holding dir, or nil when it cannot be read.
func (r *room) on(dir string) *disk {
	probe := probePath(dir)
	for _, d := range r.disks {
		if same, err := r.same(d.probe, probe); err == nil && same {
			return d
		}
	}
	avail, total, err := r.free(probe)
	if err != nil {
		return nil
	}
	d := &disk{probe: probe, avail: avail, total: total}
	r.disks = append(r.disks, d)
	return d
}

// place decides what the bin gives up for path. The answer has no Path when
// there is nothing to decide, and the backend then reports on the path itself.
// A Permanent answer means place deleted the path.
func (r *room) place(path string) (bin.Disposal, error) {
	abs, err := filepath.Abs(path)
	if err != nil || filepath.Dir(abs) == abs {
		return bin.Disposal{}, nil
	}
	if _, err := os.Lstat(abs); err != nil {
		return bin.Disposal{}, nil
	}
	dir, err := r.b.DirFor(abs)
	if err != nil {
		return bin.Disposal{}, nil
	}
	src := r.on(filepath.Dir(abs))
	if src == nil {
		// No measurement is not a licence to delete anything.
		return bin.Disposal{}, nil
	}

	d := bin.Disposal{Path: abs}
	short, need, claim := src, uint64(0), uint64(0)
	if dst := r.on(dir); dst == nil || dst == src {
		// A rename needs no room whatever the path weighs. Only the floor counts.
		floor := pressure.Floor(src.total)
		if src.avail >= floor {
			return d, nil
		}
		need = floor - src.avail
		d.Reason = fmt.Sprintf("only %s free, under the %s the bin needs to defer anything",
			pressure.Bytes(src.avail), pressure.Bytes(floor))
	} else {
		// A copy needs room for the blocks the path occupies, not the length it states.
		size := fsutil.DiskUsage(abs)
		if size <= 0 {
			return d, nil
		}
		short, claim = dst, uint64(size)
		if claim <= dst.avail {
			dst.avail -= claim
			return d, nil
		}
		need = claim - dst.avail
		d.Reason = fmt.Sprintf("%s does not fit in the %s free on the filesystem holding the bin",
			pressure.Bytes(claim), pressure.Bytes(dst.avail))
	}

	var roomy bool
	if d.Evicted, roomy = r.makeRoom(short, need, d.Reason); roomy {
		short.avail -= claim
		return d, nil
	}
	if err := os.RemoveAll(abs); err != nil {
		return bin.Disposal{}, fmt.Errorf("deleting %s: %w", path, err)
	}
	d.Permanent = true
	d.Reason += ", and the bin holds too little there to make room"
	return d, nil
}

// makeRoom gives back the oldest items on d until need bytes are free there.
// It answers what went, and whether that covered need. It evicts nothing when
// all the bin holds on d cannot cover need. The path is deleted then, and the
// loss of the bin as well gains nothing.
func (r *room) makeRoom(d *disk, need uint64, why string) ([]bin.Item, bool) {
	if !r.listed {
		r.listed = true
		// A bin that cannot be listed has nothing to give back.
		r.items, _ = r.b.List()
	}
	var held []bin.Item
	var holds uint64
	for _, it := range r.items {
		if it.Size > 0 && r.on(filepath.Dir(it.ID)) == d {
			held = append(held, it)
			holds += uint64(it.Size)
		}
	}
	if holds < need {
		return nil, false
	}
	evicted, freed, _ := drain(held, need, everything, evictFrom(r.b), "made room for a recycle: "+why)
	d.avail += freed

	gone := set.New[string]()
	went := make([]bin.Item, 0, len(evicted))
	for _, ev := range evicted {
		if ev.Error == nil {
			gone.Add(ev.Item.ID)
			went = append(went, ev.Item)
		}
	}
	kept := r.items[:0]
	for _, it := range r.items {
		if !gone.Contains(it.ID) {
			kept = append(kept, it)
		}
	}
	r.items = kept
	return went, freed >= need
}
