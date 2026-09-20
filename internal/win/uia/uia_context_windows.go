//go:build windows && amd64

package uia

import (
	"context"
	"errors"
	"fmt"
	"typenext/internal/core"
	"unicode/utf16"
	"unsafe"
)

const (
	maxUIATextUnits = 32768
	// Halving reaches one unit in at most 14 reads for every permitted context size.
	// Each request is capped separately, even when the provider promotes Character.
	maxUIAPrefixReads = 14
)

// A full buffer may be a truncated range. Inspect the BSTR length before decoding,
// since UTF-16 pairs and embedded NULs must not hide that condition.
func boundedRangeText(r *comObject, max int) (text string, complete bool, err error) {
	if max < 0 || max > maxUIATextUnits {
		return "", false, errors.New("textbox context read exceeds its text limit")
	}
	if max == 0 {
		return "", true, nil
	}
	var b *uint16
	hr := comCall(r, 12, uintptr(max), uintptr(unsafe.Pointer(&b)))
	if b != nil {
		defer pSysFreeString.Call(uintptr(unsafe.Pointer(b)))
	}
	if failed(hr) {
		return "", false, fmt.Errorf("TextRange.GetText failed (0x%08x)", uint32(hr))
	}
	if b == nil {
		return "", true, nil
	}
	n, _, _ := pSysStringLen.Call(uintptr(unsafe.Pointer(b)))
	if n > uintptr(max) {
		return "", false, errors.New("textbox provider exceeded the requested text limit")
	}
	return string(utf16.Decode(unsafe.Slice(b, int(n)))), n < uintptr(max), nil
}

// GetText truncates at the range's end. This is safe for a suffix, but a truncated
// prefix loses its adjacency to the caret. Shorten only the backward probe until
// its complete text fits; never compensate by reading an unbounded document.
func readCaretSide(ctx context.Context, caret *comObject, endpoint, chars int) (string, error) {
	return readCaretSideWithin(ctx, caret, nil, endpoint, chars)
}

func readCaretSideWithin(ctx context.Context, caret, boundary *comObject, endpoint, chars int) (string, error) {
	if endpoint < 0 || endpoint > 1 || chars < 0 || chars > 8000 {
		return "", errors.New("invalid textbox context limit")
	}
	if chars == 0 {
		return "", nil
	}
	if err := rangeWithinBoundary(caret, boundary); err != nil {
		return "", err
	}
	limit := chars*2 + 4
	for units, attempt := chars, 0; units > 0 && attempt < maxUIAPrefixReads; units, attempt = units/2, attempt+1 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r, err := cloneRange(caret)
		if err != nil {
			return "", err
		}
		count := units
		if endpoint == 0 {
			count = -count
		}
		err = moveEnd(r, endpoint, count)
		if err == nil {
			err = clampRangeToBoundary(r, boundary)
		}
		if err == nil && boundary != nil {
			// Clamping or a faulty provider must not separate the probe from its caret.
			var comparison int32
			comparison, err = compareEndpoint(r, 1-endpoint, caret, 1-endpoint)
			if err == nil && comparison != 0 {
				err = errors.New("textbox context range lost its caret boundary")
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		var text string
		var complete bool
		if err == nil {
			text, complete, err = boundedRangeText(r, limit)
		}
		release(r)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return "", err
		}
		if endpoint == 1 {
			return core.Head(text, chars), nil
		}
		if complete {
			return core.Tail(text, chars), nil
		}
	}
	return "", errors.New("textbox cannot expose bounded text immediately before the caret")
}

func collapsedSelection(pat *comObject) (*comObject, error) {
	var selections *comObject
	hr := comCall(pat, 5, uintptr(unsafe.Pointer(&selections)))
	defer release(selections)
	if failed(hr) || selections == nil {
		return nil, fmt.Errorf("TextPattern.GetSelection unavailable (0x%08x); no end-of-text guess is made", uint32(hr))
	}
	count, err := scalar(selections, 3)
	if err != nil {
		return nil, fmt.Errorf("TextRangeArray.Length: %w", err)
	}
	if count != 1 {
		return nil, errors.New("place one caret in the textbox without selecting text")
	}
	var caret *comObject
	hr = comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret)))
	if failed(hr) || caret == nil {
		release(caret)
		return nil, fmt.Errorf("TextRangeArray.GetElement(0) unavailable (0x%08x)", uint32(hr))
	}
	var compare int32
	hr = comCall(caret, 5, 0, uintptr(unsafe.Pointer(caret)), 1, uintptr(unsafe.Pointer(&compare)))
	if failed(hr) {
		release(caret)
		return nil, fmt.Errorf("caret CompareEndpoints failed (0x%08x)", uint32(hr))
	}
	if compare != 0 {
		release(caret)
		return nil, errors.New("selected text is not replaced; clear the selection first")
	}
	return caret, nil
}

// Returns an owned fresh caret so the caller can also check scope/editability.
func verifyCaretSelection(pat, expected *comObject) (*comObject, error) {
	current, err := collapsedSelection(pat)
	if err != nil {
		return nil, err
	}
	same, err := compareRangeEndpoints(expected, current)
	if err != nil {
		release(current)
		return nil, err
	}
	if !same {
		release(current)
		return nil, errors.New("caret changed while reading; try again")
	}
	return current, nil
}
