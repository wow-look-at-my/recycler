//go:build windows && !amd64 && !arm64

package recycler

// This package supports 64-bit Windows only.
//
// On a 32-bit Windows build shellapi.h byte-packs SHFILEOPSTRUCTW (pshpack1.h
// under #ifndef _WIN64), so every field after fFlags sits at an offset Go's own
// struct layout would not produce, and the shell would read the file operation
// from the wrong bytes. Rather than carry a second declaration for a platform
// nobody targets any more, the build stops here - loudly, instead of silently
// corrupting a delete.
func init() { recyclerSupportsOnly64BitWindows() }
