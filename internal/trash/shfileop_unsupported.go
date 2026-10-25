//go:build windows && !amd64 && !arm64

// This package supports the wide Windows word.

package trash

// This package supports the wide Windows word.
func init() { recyclerSupportsOnly64BitWindows() }
