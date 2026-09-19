//go:build windows && amd64

package win

import (
	"fmt"
	"unsafe"
)

// caretIdentity lives exclusively on the accessibility worker's COM thread.
// A retained clone distinguishes identical passages without relying on blinking
// native caret rectangles or a provider's changing geometry fallback.
type caretIdentity struct {
	window   uintptr
	focus    string
	rangeRef *comObject
	serial   uint64
}

func (c *caretIdentity) close() {
	release(c.rangeRef)
	c.rangeRef = nil
}

func sameRangeEndpoints(a, b *comObject) bool {
	for endpoint := uintptr(0); endpoint <= 1; endpoint++ {
		var comparison int32
		if failed(comCall(a, 5, endpoint, uintptr(unsafe.Pointer(b)), endpoint, uintptr(unsafe.Pointer(&comparison)))) || comparison != 0 {
			return false
		}
	}
	return true
}

func (c *caretIdentity) identify(window uintptr, focus string, caret *comObject) (string, error) {
	unchanged := c.rangeRef != nil && c.window == window && c.focus == focus && sameRangeEndpoints(c.rangeRef, caret)
	copy, err := cloneRange(caret)
	if err != nil {
		return "", err
	}
	c.close()
	c.window, c.focus, c.rangeRef = window, focus, copy
	if !unchanged {
		c.serial++
	}
	return fmt.Sprintf("uia:%d", c.serial), nil
}
