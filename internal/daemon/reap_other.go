//go:build !linux

// ReapStrays finds nothing here: only Linux lists another process's open files in a way this can read.

package daemon

// ReapStrays finds nothing here: only Linux lists another process's open files in a way this can read.
func ReapStrays(string) ([]int, error) { return nil, nil }
