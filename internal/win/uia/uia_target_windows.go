//go:build windows && amd64

package uia

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

var iidTextChild = guid{A: 0x6552b038, B: 0xae05, C: 0x40c8, D: [8]byte{0xab, 0xfd, 0xaa, 0x08, 0x35, 0x2a, 0xab, 0x86}}

type uiaTextTarget struct {
	owner, pattern, boundary *comObject
	focusID, ownerID, via    string
}

func (t *uiaTextTarget) close() {
	release(t.boundary)
	release(t.pattern)
	release(t.owner)
	t.boundary, t.pattern, t.owner = nil, nil, nil
}

func (t *uiaTextTarget) identity() string {
	return t.via + ":" + t.focusID + ":" + t.ownerID
}

func resolveTextTarget(ctx context.Context, a, el *comObject) (_ *uiaTextTarget, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	t := &uiaTextTarget{via: "TextPattern"}
	defer func() {
		if err != nil {
			t.close()
		}
	}()
	t.pattern, err = focusedTextPattern(el)
	if err != nil && !patternUnavailable(err) {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	t.focusID, err = focusID(el)
	if err != nil {
		return nil, err
	}
	if t.pattern != nil {
		comCall(el, 1) // Retain the owner separately from the caller's focused element.
		t.owner, t.ownerID = el, t.focusID
		return t, nil
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	child, err := pattern(el, 10029, iidTextChild)
	if err != nil {
		return nil, fmt.Errorf("focused control has neither TextPattern nor a usable TextChildPattern: %w", err)
	}
	defer release(child)
	// TextChild also describes links and embedded objects. Require the focused
	// child to expose a writable value before treating it as an independent editor.
	writable, err := valueEditability(el)
	if err != nil {
		return nil, err
	}
	if !writable {
		return nil, errors.New("TextChild focused element does not establish an independently editable field")
	}
	hr := comCall(child, 3, uintptr(unsafe.Pointer(&t.owner))) // TextContainer
	if failed(hr) || t.owner == nil {
		return nil, fmt.Errorf("TextChild.TextContainer unavailable (0x%08x)", uint32(hr))
	}
	if err = validateTextElement(t.owner, false); err != nil {
		return nil, fmt.Errorf("TextChild container: %w", err)
	}
	t.ownerID, err = focusID(t.owner)
	if err != nil {
		return nil, err
	}
	if t.focusID == t.ownerID {
		return nil, errors.New("TextChild container points back to the focused element")
	}
	t.pattern, err = pattern(t.owner, 10014, iidText)
	if err != nil {
		return nil, fmt.Errorf("TextChild container: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	hr = comCall(child, 4, uintptr(unsafe.Pointer(&t.boundary))) // TextRange
	if failed(hr) || t.boundary == nil {
		return nil, fmt.Errorf("TextChild.TextRange unavailable (0x%08x)", uint32(hr))
	}
	var enclosing *comObject
	hr = comCall(t.boundary, 11, uintptr(unsafe.Pointer(&enclosing))) // GetEnclosingElement
	defer release(enclosing)
	if failed(hr) || enclosing == nil {
		return nil, fmt.Errorf("TextChild range owner unavailable (0x%08x)", uint32(hr))
	}
	var same int32
	hr = comCall(a, 3, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(enclosing)), uintptr(unsafe.Pointer(&same)))
	if failed(hr) || same == 0 {
		return nil, fmt.Errorf("TextChild range does not belong to the focused field (0x%08x)", uint32(hr))
	}
	if _, err = caretEditability(a, el, t.boundary); err != nil {
		return nil, fmt.Errorf("TextChild field boundary: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	t.via = "TextChildPattern"
	return t, nil
}

func compareEndpoint(r *comObject, endpoint int, other *comObject, otherEndpoint int) (int32, error) {
	var comparison int32
	hr := comCall(r, 5, uintptr(endpoint), uintptr(unsafe.Pointer(other)), uintptr(otherEndpoint), uintptr(unsafe.Pointer(&comparison)))
	if failed(hr) {
		return 0, fmt.Errorf("text boundary CompareEndpoints failed (0x%08x)", uint32(hr))
	}
	return comparison, nil
}

func rangeWithinBoundary(r, boundary *comObject) error {
	if boundary == nil {
		return nil
	}
	start, err := compareEndpoint(r, 0, boundary, 0)
	if err != nil {
		return err
	}
	end, err := compareEndpoint(r, 1, boundary, 1)
	if err != nil {
		return err
	}
	order, err := compareEndpoint(r, 0, r, 1)
	if err != nil {
		return err
	}
	if start < 0 || end > 0 || order > 0 {
		return errors.New("text range is outside the focused field")
	}
	return nil
}

// Only mutate a cloned probe. A provider may promote Character to larger units,
// so clamp both endpoints and verify the result before requesting any text.
func clampRangeToBoundary(r, boundary *comObject) error {
	if boundary == nil {
		return nil
	}
	for endpoint := 0; endpoint <= 1; endpoint++ {
		comparison, err := compareEndpoint(r, endpoint, boundary, endpoint)
		if err != nil {
			return err
		}
		if (endpoint == 0 && comparison < 0) || (endpoint == 1 && comparison > 0) {
			hr := comCall(r, 15, uintptr(endpoint), uintptr(unsafe.Pointer(boundary)), uintptr(endpoint)) // MoveEndpointByRange
			if failed(hr) {
				return fmt.Errorf("text boundary MoveEndpointByRange failed (0x%08x)", uint32(hr))
			}
		}
	}
	return rangeWithinBoundary(r, boundary)
}

func verifyTextTarget(ctx context.Context, a, el *comObject, expected *uiaTextTarget, caret *comObject) error {
	current, err := resolveTextTarget(ctx, a, el)
	if err != nil {
		return err
	}
	defer current.close()
	if current.identity() != expected.identity() {
		return errors.New("textbox or text container changed while reading; try again")
	}
	if expected.boundary != nil {
		same, err := compareRangeEndpoints(expected.boundary, current.boundary)
		if err != nil {
			return err
		}
		if !same {
			return errors.New("focused field boundary changed while reading; try again")
		}
	}
	fresh, err := verifyCaretSelection(current.pattern, caret)
	if err != nil {
		return err
	}
	defer release(fresh)
	if err = rangeWithinBoundary(fresh, current.boundary); err != nil {
		return err
	}
	_, err = caretEditability(a, el, fresh)
	return err
}
