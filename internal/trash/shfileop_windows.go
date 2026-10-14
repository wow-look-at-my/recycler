//go:build windows && (amd64 || arm64)

package trash

// shellapi.h declares SHFILEOPSTRUCTW with default packing on wide Windows -
// it wraps the struct in pshpack1.h only under #ifndef _WIN64.

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
