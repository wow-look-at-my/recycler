//go:build linux && !cosmo

// Package workspace serves a directory through FUSE so that a full filesystem never reaches the
// program writing to it.
//
// The cosmo toolchain is excluded because its syscall package declares no mount flags, so the FUSE
// library does not compile there. That build already answers every call with ErrUnsupported.
//
// Recycling defers a deletion. The space comes back only when something gives the item up, and the
// daemon does that on a timer against a threshold. A build that writes faster than the threshold
// predicts still meets ENOSPC, and a compiler answers that by failing.
//
// Every write here is retried behind that error: the mount reclaims recycled items and runs the
// operation again, so the caller sees the second result. The application is never told the disk
// filled up unless the bin had nothing left to give.
package workspace

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/wow-look-at-my/recycler/internal/daemon"
)

// reclaimChunk is what a single ENOSPC asks the bin to give back. It is large enough that a run of
// failing writes does not turn into a run of sweeps.
const reclaimChunk = 1 << 30

// Reclaimer gives back space and reports the bytes it accounted for.
type Reclaimer func(atLeast uint64) (uint64, error)

// defaultReclaimer forwards to the recycle bin.
func defaultReclaimer(atLeast uint64) (uint64, error) {
	freed, _, err := daemon.Reclaim(atLeast)
	return freed, err
}

// retrier runs an operation again once space has been given back. It serializes reclaims, so a
// burst of failing writes performs one sweep between them rather than one each.
type retrier struct {
	reclaim Reclaimer
	mu      sync.Mutex
	report  func(freed uint64, err error)
}

// do runs op, and on a full filesystem reclaims space and runs it again. The second result is what
// the caller gets, so a bin with nothing left to give still surfaces ENOSPC.
func (r *retrier) do(op func() syscall.Errno) syscall.Errno {
	errno := op()
	if errno != syscall.ENOSPC {
		return errno
	}
	freed, err := r.sweep()
	if r.report != nil {
		r.report(freed, err)
	}
	if freed == 0 {
		return errno
	}
	return op()
}

// sweep reclaims under the lock, so concurrent writers share one sweep.
func (r *retrier) sweep() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reclaim(reclaimChunk)
}

// node is a loopback node whose write paths retry behind a full filesystem.
type node struct {
	fs.LoopbackNode
	retry *retrier
}

// newNode is the root's factory, so every child carries the same retrier.
func (n *node) newChild(rootData *fs.LoopbackRoot, _ *fs.Inode, _ string, _ *syscall.Stat_t) fs.InodeEmbedder {
	return &node{
		LoopbackNode: fs.LoopbackNode{RootData: rootData},
		retry:        n.retry,
	}
}

var (
	_ fs.NodeCreater   = (*node)(nil)
	_ fs.NodeMkdirer   = (*node)(nil)
	_ fs.NodeSetattrer = (*node)(nil)
	_ fs.NodeRenamer   = (*node)(nil)
	_ fs.NodeSymlinker = (*node)(nil)
	_ fs.NodeMknoder   = (*node)(nil)
	_ fs.NodeLinker    = (*node)(nil)
)

// Create is where a new output file meets a full filesystem.
func (n *node) Create(ctx context.Context, name string, flags, mode uint32, out *fuse.EntryOut) (
	inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno,
) {
	errno = n.retry.do(func() syscall.Errno {
		inode, fh, fuseFlags, errno = n.LoopbackNode.Create(ctx, name, flags, mode, out)
		return errno
	})
	if errno != 0 {
		return nil, nil, 0, errno
	}
	return inode, &file{FileHandle: fh, retry: n.retry}, fuseFlags, 0
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (
	inode *fs.Inode, errno syscall.Errno,
) {
	errno = n.retry.do(func() syscall.Errno {
		inode, errno = n.LoopbackNode.Mkdir(ctx, name, mode, out)
		return errno
	})
	return inode, errno
}

// Setattr covers a truncate that grows a file, which allocates.
func (n *node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return n.retry.do(func() syscall.Errno {
		return n.LoopbackNode.Setattr(ctx, fh, in, out)
	})
}

// Rename across directories writes a directory entry.
func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return n.retry.do(func() syscall.Errno {
		return n.LoopbackNode.Rename(ctx, name, newParent, newName, flags)
	})
}

func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (
	inode *fs.Inode, errno syscall.Errno,
) {
	errno = n.retry.do(func() syscall.Errno {
		inode, errno = n.LoopbackNode.Symlink(ctx, target, name, out)
		return errno
	})
	return inode, errno
}

func (n *node) Mknod(ctx context.Context, name string, mode, dev uint32, out *fuse.EntryOut) (
	inode *fs.Inode, errno syscall.Errno,
) {
	errno = n.retry.do(func() syscall.Errno {
		inode, errno = n.LoopbackNode.Mknod(ctx, name, mode, dev, out)
		return errno
	})
	return inode, errno
}

func (n *node) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (
	inode *fs.Inode, errno syscall.Errno,
) {
	errno = n.retry.do(func() syscall.Errno {
		inode, errno = n.LoopbackNode.Link(ctx, target, name, out)
		return errno
	})
	return inode, errno
}

// file wraps an open handle, because the bytes of a large output arrive through Write rather than
// through Create.
type file struct {
	fs.FileHandle
	retry *retrier
}

var (
	_ fs.FileWriter    = (*file)(nil)
	_ fs.FileAllocater = (*file)(nil)
	_ fs.FileFlusher   = (*file)(nil)
	_ fs.FileFsyncer   = (*file)(nil)
	_ fs.FileReleaser  = (*file)(nil)
	_ fs.FileReader    = (*file)(nil)
	_ fs.FileGetattrer = (*file)(nil)
	_ fs.FileSetattrer = (*file)(nil)
	_ fs.FileLseeker   = (*file)(nil)
)

func (f *file) Write(ctx context.Context, data []byte, off int64) (written uint32, errno syscall.Errno) {
	errno = f.retry.do(func() syscall.Errno {
		w, ok := f.FileHandle.(fs.FileWriter)
		if !ok {
			return syscall.ENOTSUP
		}
		written, errno = w.Write(ctx, data, off)
		return errno
	})
	return written, errno
}

// Allocate is fallocate, which reserves blocks and so fails on a full filesystem.
func (f *file) Allocate(ctx context.Context, off, size uint64, mode uint32) syscall.Errno {
	return f.retry.do(func() syscall.Errno {
		a, ok := f.FileHandle.(fs.FileAllocater)
		if !ok {
			return syscall.ENOTSUP
		}
		return a.Allocate(ctx, off, size, mode)
	})
}

// Flush reports the deferred error of a write the kernel had buffered.
func (f *file) Flush(ctx context.Context) syscall.Errno {
	fl, ok := f.FileHandle.(fs.FileFlusher)
	if !ok {
		return 0
	}
	return fl.Flush(ctx)
}

func (f *file) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	s, ok := f.FileHandle.(fs.FileFsyncer)
	if !ok {
		return 0
	}
	return s.Fsync(ctx, flags)
}

func (f *file) Release(ctx context.Context) syscall.Errno {
	r, ok := f.FileHandle.(fs.FileReleaser)
	if !ok {
		return 0
	}
	return r.Release(ctx)
}

func (f *file) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	r, ok := f.FileHandle.(fs.FileReader)
	if !ok {
		return nil, syscall.ENOTSUP
	}
	return r.Read(ctx, dest, off)
}

func (f *file) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	g, ok := f.FileHandle.(fs.FileGetattrer)
	if !ok {
		return syscall.ENOTSUP
	}
	return g.Getattr(ctx, out)
}

func (f *file) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return f.retry.do(func() syscall.Errno {
		s, ok := f.FileHandle.(fs.FileSetattrer)
		if !ok {
			return syscall.ENOTSUP
		}
		return s.Setattr(ctx, in, out)
	})
}

func (f *file) Lseek(ctx context.Context, off uint64, whence uint32) (uint64, syscall.Errno) {
	l, ok := f.FileHandle.(fs.FileLseeker)
	if !ok {
		return 0, syscall.ENOTSUP
	}
	return l.Lseek(ctx, off, whence)
}

// Options configure a mount.
type Options struct {
	// Reclaim gives space back. The recycle bin is used when this is nil.
	Reclaim Reclaimer
	// Report is called after each reclaim, so a mount can say what it gave up.
	Report func(freed uint64, err error)
	// Debug turns on the FUSE protocol log.
	Debug bool
}

// Mount serves backing at mountpoint until the returned server is unmounted.
//
// The mount is taken through the mount syscall rather than the fusermount helper, which is not
// present on every image and is only needed by a user who is not root.
func Mount(mountpoint, backing string, opts Options) (*fuse.Server, error) {
	if opts.Reclaim == nil {
		opts.Reclaim = defaultReclaimer
	}
	if err := os.MkdirAll(backing, 0o755); err != nil {
		return nil, fmt.Errorf("recycler: preparing backing directory: %w", err)
	}
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return nil, fmt.Errorf("recycler: preparing mountpoint: %w", err)
	}

	// The root is built here rather than by NewLoopbackRoot, which returns the node and gives no
	// way to set the factory every child comes from.
	var st syscall.Stat_t
	if err := syscall.Stat(backing, &st); err != nil {
		return nil, fmt.Errorf("recycler: reading backing directory: %w", err)
	}
	rootData := &fs.LoopbackRoot{Path: backing, Dev: uint64(st.Dev)}
	root := &node{
		LoopbackNode: fs.LoopbackNode{RootData: rootData},
		retry:        &retrier{reclaim: opts.Reclaim, report: opts.Report},
	}
	rootData.NewNode = root.newChild
	rootData.RootNode = root

	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			DirectMount: true,
			FsName:      backing,
			Name:        "recycler",
			Debug:       opts.Debug,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("recycler: mounting %s: %w", mountpoint, err)
	}
	return server, nil
}
