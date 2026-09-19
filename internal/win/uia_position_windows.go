//go:build windows && amd64

package win

import (
	"fmt"
	"unsafe"
)

const maxStableCaretOffset = 8192

// stableCaretOffset identifies a collapsed caret using only ranges from the current capture.
// Browser providers can retire older ranges when unrelated page content changes, even while the focused textbox and caret stay put.
// Large documents and providers without exact character movement fall back to the retained-range identity.
// No text or selection is read or modified here.
func stableCaretOffset(pattern, caret *comObject) (string, bool) {
	if pattern == nil || caret == nil {
		return "", false
	}
	var document *comObject
	hr := comCall(pattern, 7, uintptr(unsafe.Pointer(&document))) // DocumentRange
	defer release(document)
	if failed(hr) || document == nil {
		return "", false
	}
	var position *comObject
	hr = comCall(caret, 3, uintptr(unsafe.Pointer(&position))) // Clone
	defer release(position)
	if failed(hr) || position == nil {
		return "", false
	}

	count := -maxStableCaretOffset
	var moved int32
	hr = comCall(position, 14, 0, 0, uintptr(int64(count)), uintptr(unsafe.Pointer(&moved))) // Start, Character
	if failed(hr) || moved > 0 || moved < -maxStableCaretOffset {
		return "", false
	}
	var comparison int32
	hr = comCall(position, 5, 0, uintptr(unsafe.Pointer(document)), 0, uintptr(unsafe.Pointer(&comparison)))
	if failed(hr) || comparison != 0 {
		// The limit was exhausted before reaching the focused element's start.
		return "", false
	}

	// Character requests can be normalized or promoted to another unit by a provider.
	// Trust the count only when moving it forward returns exactly to the original caret, with both endpoints unchanged.
	offset := -moved
	var returned int32
	hr = comCall(position, 14, 0, 0, uintptr(offset), uintptr(unsafe.Pointer(&returned)))
	if failed(hr) || returned != offset || !sameRangeEndpoints(position, caret) {
		return "", false
	}
	return fmt.Sprintf("uia-offset:%d", offset), true
}
