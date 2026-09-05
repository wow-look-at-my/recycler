//go:build windows

package diskfree

import "golang.org/x/sys/windows"

// Free reports the available and total bytes of the volume holding path.
// Available is the caller's quota-adjusted free space, which is what Explorer
// shows.
func Free(path string) (avail, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var free, size, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &size, &totalFree); err != nil {
		return 0, 0, err
	}
	return free, size, nil
}
