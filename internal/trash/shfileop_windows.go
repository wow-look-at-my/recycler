//go:build windows && (amd64 || arm64)

package trash

// shellapi.h declares SHFILEOPSTRUCTW with default packing on 64-bit Windows -
// it wraps the struct in pshpack1.h only under #ifndef _WIN64 - and that is
// exactly the layout Go's own struct rules produce here.
//
// 32-bit Windows, where the packed layout would move every field after fFlags,
// is not supported: see shfileop_windows_unsupported.go.

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

func (op *shFileOpStruct) aborted() bool { return op.fAnyOperationsAborted != 0 }
