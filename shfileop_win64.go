//go:build windows && (amd64 || arm64)

package recycler

// On 64-bit Windows shellapi.h declares SHFILEOPSTRUCTW with default packing,
// which is exactly what Go's own struct layout produces.

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
