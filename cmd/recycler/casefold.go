package main

import "runtime"

// caseInsensitiveFilesystem reports whether file names should be matched
// without regard to case. Windows and macOS default to case-insensitive
// filesystems; Linux does not.
var caseInsensitiveFilesystem = runtime.GOOS == "windows" || runtime.GOOS == "darwin"
