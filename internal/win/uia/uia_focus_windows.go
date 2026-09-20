//go:build windows && amd64

package uia

import (
	"errors"
	"fmt"
	"unsafe"
)

// Protection must be checked before any text pattern or content property.
// A TextChild container need not itself have focus;
// its focused child is checked separately by the resolver.
func validateTextElement(el *comObject, requireFocus bool) error {
	// Check protection before requesting any text pattern or content property.
	password, err := scalar(el, 35)
	if err != nil {
		return fmt.Errorf("CurrentIsPassword: %w; text access disabled", err)
	}
	if password != 0 {
		return errors.New("password/protected field: text access disabled")
	}
	if requireFocus {
		focused, err := scalar(el, 26)
		if err != nil {
			return fmt.Errorf("CurrentHasKeyboardFocus: %w", err)
		}
		if focused == 0 {
			return errors.New("textbox does not have keyboard focus")
		}
	}
	enabled, err := scalar(el, 28)
	if err != nil {
		return fmt.Errorf("CurrentIsEnabled: %w", err)
	}
	if enabled == 0 {
		return errors.New("textbox is not enabled")
	}
	return nil
}

// Providers choose their own control type:
// browser search inputs with suggestions can be ComboBox, and contenteditable widgets can be Custom.
// Use the focused element's text capabilities rather than requiring an Edit or Document label.
func focusedTextPattern(el *comObject) (*comObject, error) {
	if err := validateTextElement(el, true); err != nil {
		return nil, err
	}
	pat, err := pattern(el, 10014, iidText)
	if err != nil {
		// ControlType is diagnostic metadata only; do not read Name or Value.
		if control, e := scalar(el, 21); e == nil {
			return nil, fmt.Errorf("focused control (UIA type %d) does not expose a text selection/caret: %w", control, err)
		}
		return nil, fmt.Errorf("focused control does not expose a text selection/caret: %w", err)
	}
	return pat, nil
}

var iidValue = guid{A: 0xa94cd8b1, B: 0x0844, C: 0x4cd6, D: [8]byte{0x9d, 0x2d, 0x64, 0x05, 0x37, 0xab, 0x39, 0xe9}}

// valueEditability reads metadata only, never CurrentValue or SetValue.
// The caller must already have validated the element's protection and focus state.
// Missing patterns are different from provider failures and explicit read-only.
func valueEditability(el *comObject) (bool, error) {
	value, err := pattern(el, 10002, iidValue)
	if err != nil {
		if patternUnavailable(err) {
			return false, nil
		}
		return false, fmt.Errorf("ValuePattern: %w", err)
	}
	defer release(value)
	readOnly := int32(-1) // An unwritten output must never be mistaken for false.
	hr := comCall(value, 5, uintptr(unsafe.Pointer(&readOnly)))
	if failed(hr) {
		return false, fmt.Errorf("ValuePattern.CurrentIsReadOnly failed (0x%08x)", uint32(hr))
	}
	if readOnly != 0 {
		return false, errors.New("ValuePattern.CurrentIsReadOnly: read-only text; completion disabled")
	}
	return true, nil
}

// CheckNotSupported takes VARIANT by value.
// The Windows x64 ABI passes this 24-byte structure through a pointer to a caller-owned, 16-byte-aligned copy.
// Only UIA's exact reserved unsupported token permits the ValuePattern fallback;
// VT_UNKNOWN alone could also mean a mixed attribute or a malformed provider.
func unsupportedAttribute(a *comObject, v variant) (bool, error) {
	var storage [40]byte
	base := unsafe.Pointer(&storage[0])
	aligned := (*variant)(unsafe.Add(base, (16-uintptr(base)%16)%16))
	*aligned = v
	var unsupported int32
	hr := comCall(a, 53, uintptr(unsafe.Pointer(aligned)), uintptr(unsafe.Pointer(&unsupported)))
	if failed(hr) {
		return false, fmt.Errorf("CheckNotSupported failed (0x%08x)", uint32(hr))
	}
	return unsupported != 0, nil
}

// caretEditability requires a separately verified collapsed selection.
// The ValuePattern belongs to the same validated focused editor, never an ancestor.
func caretEditability(a, el, caret *comObject) (string, error) {
	var v variant
	hr := comCall(caret, 9, 40015, uintptr(unsafe.Pointer(&v))) // UIA_IsReadOnlyAttributeId
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	if failed(hr) {
		return "", fmt.Errorf("TextRange.GetAttributeValue(IsReadOnly) failed (0x%08x)", uint32(hr))
	}
	attributeWritable := false
	switch v.VT {
	case 11: // VT_BOOL
		if *(*int16)(unsafe.Pointer(&v.Data[0])) != 0 {
			return "", errors.New("TextRange.IsReadOnly: read-only text; completion disabled")
		}
		attributeWritable = true
	case 13: // VT_UNKNOWN: validate the reserved token before falling back.
		if *(*uintptr)(unsafe.Pointer(&v.Data[0])) == 0 {
			return "", errors.New("TextRange.IsReadOnly: null attribute token")
		}
		unsupported, err := unsupportedAttribute(a, v)
		if err != nil {
			return "", err
		}
		if !unsupported {
			return "", errors.New("TextRange.IsReadOnly: mixed or unrecognized attribute; completion disabled")
		}
	default:
		return "", fmt.Errorf("TextRange.IsReadOnly: invalid attribute type %d", v.VT)
	}
	valueWritable, err := valueEditability(el)
	if err != nil {
		return "", err
	}
	if attributeWritable {
		return "TextPattern.IsReadOnly", nil
	}
	if valueWritable {
		return "ValuePattern.IsReadOnly (text attribute unsupported)", nil
	}
	return "", errors.New("TextRange.IsReadOnly is unsupported and the focused editor has no writable ValuePattern")
}
