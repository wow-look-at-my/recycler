//go:build windows

package daemon

import "golang.org/x/sys/windows"

// raisePriority puts the sweep above a busy build's threads, and reports
// whether it was granted. Windows has no nice value, so this is the class that
// means the same thing.
func raisePriority() bool {
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return false
	}
	return windows.SetPriorityClass(h, windows.ABOVE_NORMAL_PRIORITY_CLASS) == nil
}
