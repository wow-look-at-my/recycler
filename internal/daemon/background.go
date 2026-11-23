//go:build darwin || cosmo

// XNU's setpriority selectors, which neither syscall nor x/sys spells.

package daemon

import "syscall"

// XNU's setpriority selectors, which neither syscall nor x/sys spells.
const (
	prioDarwinProcess = 4
	prioDarwinBG      = 0x1000
)

// background puts every thread of this process, and every thread it makes
// later, in the darwin background band, as `taskpolicy -b` does.
func background() bool {
	return syscall.Setpriority(prioDarwinProcess, 0, prioDarwinBG) == nil
}
