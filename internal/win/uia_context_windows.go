//go:build windows && amd64

package win

import (
	"context"
	"errors"
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
		return "", false, errors.New("textbox text could not be read")
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
	if endpoint < 0 || endpoint > 1 || chars < 0 || chars > 8000 {
		return "", errors.New("invalid textbox context limit")
	}
	if chars == 0 {
		return "", nil
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
		return nil, errors.New("textbox does not expose a reliable selection/caret; no end-of-text guess is made")
	}
	count, err := scalar(selections, 3)
	if err != nil || count != 1 {
		return nil, errors.New("place one caret in the textbox without selecting text")
	}
	var caret *comObject
	hr = comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret)))
	if failed(hr) || caret == nil {
		release(caret)
		return nil, errors.New("caret range unavailable")
	}
	var compare int32
	hr = comCall(caret, 5, 0, uintptr(unsafe.Pointer(caret)), 1, uintptr(unsafe.Pointer(&compare)))
	if failed(hr) || compare != 0 {
		release(caret)
		return nil, errors.New("selected text is not replaced; clear the selection first")
	}
	return caret, nil
}

func verifyCaretSelection(pat, expected *comObject) error {
	current, err := collapsedSelection(pat)
	if err != nil {
		return err
	}
	defer release(current)
	same, err := compareRangeEndpoints(expected, current)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("caret changed while reading; try again")
	}
	return nil
}
