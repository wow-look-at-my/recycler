package main

import "runtime"

// caseInsensitiveFilesystem is the Windows and macOS default, and not Linux's.
var caseInsensitiveFilesystem = runtime.GOOS == "windows" || runtime.GOOS == "darwin"
