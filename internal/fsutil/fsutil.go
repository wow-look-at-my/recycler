package fsutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// move relocates src to dst, falling back to a copy when the two are on
// different filesystems. It never overwrites an existing dst.
func Move(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", bin.ErrExists, dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !IsCrossDevice(err) {
		return err
	}
	if err := CopyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// IsCrossDevice reports whether err is a rename failure caused by src and dst
// living on different filesystems.
func IsCrossDevice(err error) bool {
	var le *os.LinkError
	if !errors.As(err, &le) {
		return false
	}
	return errors.Is(le.Err, errCrossDevice)
}

// CopyTree copies a file, directory tree or symlink from src to dst.
func CopyTree(src, dst string) error {
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
			if err := CopyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
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

// TreeSize returns the total size in bytes of a file or directory tree, or
// [bin.SizeUnknown] if it cannot be determined.
func TreeSize(path string) int64 {
	info, err := os.Lstat(path)
	if err != nil {
		return bin.SizeUnknown
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
		return bin.SizeUnknown
	}
	return total
}

// UniqueName returns a name derived from want that does not yet exist in any of
// the given directories, together with the suffix that was appended (if any).
// Both directories are checked so a trash implementation can keep its data and
// metadata file names in sync.
func UniqueName(want string, dirs ...string) string {
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

// PrepareDest validates a restore destination and creates its parent
// directories.
func PrepareDest(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%w: %s", bin.ErrExists, dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.MkdirAll(filepath.Dir(dest), 0o755)
}
