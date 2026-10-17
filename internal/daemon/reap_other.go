//go:build !linux

package daemon

// ReapStrays finds nothing here: only Linux lists another process's open files
// in a way this can read. The lock still keeps every daemon started by this
// build to one.
func ReapStrays(string) ([]int, error) { return nil, nil }
