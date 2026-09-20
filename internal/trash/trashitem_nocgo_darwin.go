//go:build darwin && !cgo

package trash

// The trash on macOS is an operation the system performs, reached through
// Foundation. There is no syscall behind it to call instead.
func init() { recyclerNeedsCgoOnMacOS() }
