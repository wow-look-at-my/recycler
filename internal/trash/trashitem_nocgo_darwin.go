//go:build darwin && !cgo

// The trash on macOS is an operation the system performs, reached through Foundation.

package trash

// The trash on macOS is an operation the system performs, reached through Foundation.
func init() { recyclerNeedsCgoOnMacOS() }
