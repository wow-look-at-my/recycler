//go:build windows && (386 || arm)

package recycler

import "encoding/binary"

// On 32-bit Windows shellapi.h wraps SHFILEOPSTRUCTW in #include <pshpack1.h>,
// so the fields after fFlags sit at odd offsets that Go's own layout rules
// would pad away. Everything from fAnyOperationsAborted on is therefore kept as
// raw bytes: this package never sets those fields, and only reads the abort
// flag back out.
//
//	offset  field
//	0       hwnd
//	4       wFunc
//	8       pFrom
//	12      pTo
//	16      fFlags
//	18      fAnyOperationsAborted   } tail
//	22      hNameMappings           }
//	26      lpszProgressTitle       }
type shFileOpStruct struct {
	hwnd   uintptr
	wFunc  uint32
	pFrom  *uint16
	pTo    *uint16
	fFlags uint16
	tail   [12]byte
}

func (op *shFileOpStruct) aborted() bool {
	return binary.LittleEndian.Uint32(op.tail[0:4]) != 0
}
