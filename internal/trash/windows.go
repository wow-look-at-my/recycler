//go:build windows

package trash

// Windows Recycle Bin implementation.
//
// Recycling goes through the shell (SHFileOperation with FOF_ALLOWUNDO), so it
// behaves exactly like deleting from Explorer. Listing and restoring read the
// bin's own on-disk layout directly: every recycled item is a "$R" data file
// with a matching "$I" metadata file in <drive>:\$Recycle.Bin\<user SID>\.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
	"github.com/wow-look-at-my/recycler/internal/winbin"
)

const (
	foDelete = 0x0003

	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoConfirmMkDir = 0x0200
	fofNoErrorUI      = 0x0400

	rpcChangedMode = 0x80010106

	recycleBinDirName = "$Recycle.Bin"
	dataPrefix        = "$R"
	metaPrefix        = "$I"
)

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
)

type winTrash struct {
	sid string
}

func Backend() (bin.Backend, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("recycler: determining the current user: %w", err)
	}
	return &winTrash{sid: user.User.Sid.String()}, nil
}

func (t *winTrash) Recycle(paths []string) error {
	var errs []error
	abs := make([]string, 0, len(paths))
	for _, path := range paths {
		p, err := filepath.Abs(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("recycling %s: %w", path, err))
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			errs = append(errs, fmt.Errorf("recycling %s: %w", path, err))
			continue
		}
		abs = append(abs, p)
	}
	if len(abs) == 0 {
		return errors.Join(errs...)
	}
	if err := shFileOperationDelete(abs); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// shFileOperationDelete asks the shell to delete every path to the recycle bin
// in the same call.
func shFileOperationDelete(paths []string) error {
	from, err := doubleNullTerminated(paths)
	if err != nil {
		return err
	}

	// The shell is apartment-threaded, so the operation has to stay on the same initialised OS.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		if !errors.Is(err, windows.Errno(rpcChangedMode)) {
			return fmt.Errorf("recycler: initialising the shell: %w", err)
		}
	} else {
		defer windows.CoUninitialize()
	}

	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofNoConfirmMkDir | fofNoErrorUI | fofSilent,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(from)

	if ret != 0 {
		return fmt.Errorf("recycler: the shell refused to recycle %s: %s",
			strings.Join(paths, ", "), shFileOperationError(uint32(ret)))
	}
	if op.aborted() {
		return fmt.Errorf("recycler: recycling %s was aborted", strings.Join(paths, ", "))
	}
	return nil
}

// doubleNullTerminated builds the NUL-separated, NUL-NUL-terminated string
// list that the file operation API expects.
func doubleNullTerminated(paths []string) ([]uint16, error) {
	var out []uint16
	for _, path := range paths {
		encoded, err := windows.UTF16FromString(path)
		if err != nil {
			return nil, fmt.Errorf("recycler: invalid path %q: %w", path, err)
		}
		out = append(out, encoded...) // UTF16FromString already appends the NUL
	}
	return append(out, 0), nil
}

// shFileOperationError describes the non-Win32 error codes SHFileOperation
// returns.
func shFileOperationError(code uint32) string {
	switch code {
	case 0x71:
		return "source and destination are the same file"
	case 0x74:
		return "the path is a root directory and cannot be recycled"
	case 0x75:
		return "the operation was cancelled"
	case 0x78:
		return "access denied"
	case 0x79:
		return "the path is too long"
	case 0x7C:
		return "invalid path"
	case 0x10000:
		return "an unspecified error occurred on the destination"
	case 0x402:
		return "the path could not be found"
	}
	return fmt.Sprintf("error 0x%X", code)
}

func (t *winTrash) List() ([]bin.Item, error) {
	var items []bin.Item
	for _, dir := range t.binDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, metaPrefix) {
				continue
			}
			data := filepath.Join(dir, dataPrefix+name[len(metaPrefix):])
			st, err := os.Lstat(data)
			if err != nil {
				continue // metadata without its data file
			}
			meta, err := winbin.Parse(readOrNil(filepath.Join(dir, name)))
			if err != nil {
				continue
			}
			size := meta.Size
			if size <= 0 && !st.IsDir() {
				size = st.Size()
			}
			deletedAt := meta.DeletedAt
			if deletedAt.IsZero() {
				deletedAt = st.ModTime()
			}
			items = append(items, bin.Item{
				ID:           data,
				Name:         filepath.Base(meta.OriginalPath),
				OriginalPath: meta.OriginalPath,
				DeletedAt:    deletedAt,
				Size:         size,
				IsDir:        st.IsDir(),
			})
		}
	}
	bin.Sort(items)
	return items, nil
}

func (t *winTrash) Restore(id, dest string) (string, error) {
	data, metaPath, err := t.resolveID(id)
	if err != nil {
		return "", err
	}
	if dest == "" {
		meta, err := winbin.Parse(readOrNil(metaPath))
		if err != nil {
			return "", err
		}
		if meta.OriginalPath == "" {
			return "", fmt.Errorf("%w: %s", bin.ErrUnknownOrigin, id)
		}
		dest = meta.OriginalPath
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err := fsutil.PrepareDest(dest); err != nil {
		return "", err
	}
	if err := fsutil.Move(data, dest); err != nil {
		return "", err
	}
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return dest, err
	}
	return dest, nil
}

// evict destroys a recycled item and its $I metadata. Only the disk-pressure
// daemon calls this.
func (t *winTrash) Evict(id string) error {
	data, metaPath, err := t.resolveID(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(data); err != nil {
		return err
	}
	// The data is gone.
	os.Remove(metaPath)
	return nil
}

// resolveID validates an item ID and returns the paths.
func (t *winTrash) resolveID(id string) (data, metaPath string, err error) {
	clean := filepath.Clean(id)
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	if !strings.HasPrefix(base, dataPrefix) {
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	known := false
	for _, candidate := range t.binDirs() {
		if strings.EqualFold(candidate, dir) {
			known = true
			break
		}
	}
	if !known {
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	if _, err := os.Lstat(clean); err != nil {
		return "", "", fmt.Errorf("%w: %s", bin.ErrNotFound, id)
	}
	return clean, filepath.Join(dir, metaPrefix+base[len(dataPrefix):]), nil
}

// binDirs returns this user's recycle bin directory on every volume that has
// a directory.
func (t *winTrash) binDirs() []string {
	var dirs []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return dirs
	}
	for letter := 'A'; letter <= 'Z'; letter++ {
		if mask&(1<<uint(letter-'A')) == 0 {
			continue
		}
		dir := filepath.Join(string(letter)+`:\`, recycleBinDirName, t.sid)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// readOrNil returns the content of a file, or nil when it cannot be read.
func readOrNil(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
