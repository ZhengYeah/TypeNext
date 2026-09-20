//go:build windows && amd64

package uia

import (
	"errors"
	"fmt"
	"unsafe"
)

// caretIdentity lives exclusively on the accessibility worker's COM thread.
// Exact offsets survive browser refreshes; a retained clone is the fallback for
// providers that cannot expose one. Neither scheme relies on popup geometry.
type caretIdentity struct {
	window      uintptr
	focus       string
	rangeRef    *comObject
	serial      uint64
	offsetBased bool
}

func (c *caretIdentity) close() {
	release(c.rangeRef)
	c.rangeRef = nil
	c.offsetBased = false
}

func (c *caretIdentity) identifyInPattern(window uintptr, focus string, pattern, caret *comObject) (string, error) {
	sameTarget := c.window == window && c.focus == focus
	// Keep one identity scheme throughout a request, including its insertion check.
	// A temporary provider failure must not look like a moved caret.
	if sameTarget && c.rangeRef != nil {
		return c.identify(window, focus, caret)
	}
	if id, ok := stableCaretOffset(pattern, caret); ok {
		c.close()
		c.window, c.focus, c.offsetBased = window, focus, true
		return id, nil
	}
	if sameTarget && c.offsetBased {
		return "", errors.New("textbox caret position is temporarily unavailable")
	}
	return c.identify(window, focus, caret)
}

func sameRangeEndpoints(a, b *comObject) bool {
	same, err := compareRangeEndpoints(a, b)
	return err == nil && same
}

func compareRangeEndpoints(a, b *comObject) (bool, error) {
	for endpoint := uintptr(0); endpoint <= 1; endpoint++ {
		var comparison int32
		hr := comCall(a, 5, endpoint, uintptr(unsafe.Pointer(b)), endpoint, uintptr(unsafe.Pointer(&comparison)))
		if failed(hr) {
			return false, fmt.Errorf("caret position could not be verified (0x%08x)", uint32(hr))
		}
		if comparison != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (c *caretIdentity) identify(window uintptr, focus string, caret *comObject) (string, error) {
	unchanged := false
	if c.rangeRef != nil && c.window == window && c.focus == focus {
		var err error
		unchanged, err = compareRangeEndpoints(c.rangeRef, caret)
		if err != nil {
			// A provider failure does not establish a different caret.
			// Keep the original range so a retry can still verify the same request.
			return "", err
		}
	}
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
