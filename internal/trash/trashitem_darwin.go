//go:build darwin && cgo

package trash

// The macOS trash operation, as the system performs it. A rename into ~/.Trash is
// a different operation. The kernel refuses a single whose source the caller may
// not rename out of even where it may unlink it. Which a sandboxed process meets
// routinely. A copy writes the bytes another time to reach the same place.
// `trashItemAtURL:` is the move itself, and answers where it put the item, having
// picked a free name in the trash on that item's own volume.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include "trashitem_darwin.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// trashItem hands path to the system's trash operation and returns the path
// the item now sits at inside the trash. The second argument is where a build
// with no such operation puts the item; the system picks the trash itself.
func trashItem(path, _ string) (string, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var landed *C.char
	msg := C.recyclerTrashItem(cPath, &landed)
	if msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return "", errors.New(C.GoString(msg))
	}
	defer C.free(unsafe.Pointer(landed))
	return C.GoString(landed), nil
}
