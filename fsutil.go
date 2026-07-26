package recycler

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// move relocates src to dst, falling back to a copy when the two are on
// different filesystems. It never overwrites an existing dst.
func move(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// isCrossDevice reports whether err is a rename failure caused by src and dst
// living on different filesystems.
func isCrossDevice(err error) bool {
	var le *os.LinkError
	if !errors.As(err, &le) {
		return false
	}
	return errors.Is(le.Err, errCrossDevice)
}

// copyTree copies a file, directory tree or symlink from src to dst.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	case info.Mode().IsRegular():
		return copyFile(src, dst, info.Mode().Perm())
	default:
		return fmt.Errorf("recycler: cannot copy %s: unsupported file type %s", src, info.Mode().Type())
	}
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// treeSize returns the total size in bytes of a file or directory tree, or
// [SizeUnknown] if it cannot be determined.
func treeSize(path string) int64 {
	info, err := os.Lstat(path)
	if err != nil {
		return SizeUnknown
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	if err != nil {
		return SizeUnknown
	}
	return total
}

// uniqueName returns a name derived from want that does not yet exist in any of
// the given directories, together with the suffix that was appended (if any).
// Both directories are checked so a trash implementation can keep its data and
// metadata file names in sync.
func uniqueName(want string, dirs ...string) string {
	if want == "" || want == "." || want == ".." || strings.ContainsRune(want, filepath.Separator) {
		want = "recycled"
	}
	ext := filepath.Ext(want)
	base := strings.TrimSuffix(want, ext)
	for n := 0; ; n++ {
		candidate := want
		if n > 0 {
			candidate = fmt.Sprintf("%s_%d%s", base, n, ext)
		}
		if !existsIn(candidate, dirs) {
			return candidate
		}
	}
}

func existsIn(name string, dirs []string) bool {
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// sortItems orders items newest first, with the ID as a tie-breaker so listings
// are stable.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DeletedAt.Equal(items[j].DeletedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].DeletedAt.After(items[j].DeletedAt)
	})
}

// prepareDest validates a restore destination and creates its parent
// directories.
func prepareDest(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.MkdirAll(filepath.Dir(dest), 0o755)
}
