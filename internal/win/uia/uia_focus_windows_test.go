//go:build windows && amd64

package uia

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

type testFocusedPattern struct {
	comObject
	refs int32
}

var testFocusedPatternVTable = [96]uintptr{
	2: syscall.NewCallback(func(p *testFocusedPattern) uintptr {
		p.refs--
		return uintptr(p.refs)
	}),
}

type testFocusedElement struct {
	comObject
	text                              *testFocusedPattern
	control, password, focus, enabled int32
	failProperty                      int
	patternHR                         uintptr
	patternMissing                    bool
	properties                        []int
	patternCalls                      int
	wrongPattern                      bool
	refs                              int32
	value                             *testEditableValuePattern
	valuePatternHR                    uintptr
	valuePatternMissing               bool
	valuePatternCalls                 int
}

func (e *testFocusedElement) property(slot int, value int32, out *int32) uintptr {
	e.properties = append(e.properties, slot)
	*out = value
	if e.failProperty == slot {
		return 0x80004005
	}
	return 0
}

var testFocusedElementVTable = [96]uintptr{
	2: syscall.NewCallback(func(e *testFocusedElement) uintptr {
		e.refs--
		return uintptr(e.refs)
	}),
	14: syscall.NewCallback(func(e *testFocusedElement, id uintptr, iid *guid, out **comObject) uintptr {
		if id == 10002 && *iid == iidValue {
			e.valuePatternCalls++
			if e.value != nil && !e.valuePatternMissing {
				e.value.refs++
				*out = &e.value.comObject
			}
			return e.valuePatternHR
		}
		e.patternCalls++
		if id != 10014 || *iid != iidText {
			e.wrongPattern = true
			return 0x80004002
		}
		if !e.patternMissing {
			e.text.refs++
			*out = &e.text.comObject
		}
		return e.patternHR
	}),
	21: syscall.NewCallback(func(e *testFocusedElement, out *int32) uintptr {
		return e.property(21, e.control, out)
	}),
	26: syscall.NewCallback(func(e *testFocusedElement, out *int32) uintptr {
		return e.property(26, e.focus, out)
	}),
	28: syscall.NewCallback(func(e *testFocusedElement, out *int32) uintptr {
		return e.property(28, e.enabled, out)
	}),
	35: syscall.NewCallback(func(e *testFocusedElement, out *int32) uintptr {
		return e.property(35, e.password, out)
	}),
}

func newTestFocusedElement(control int32) *testFocusedElement {
	return &testFocusedElement{
		comObject: comObject{VTable: &testFocusedElementVTable},
		text:      &testFocusedPattern{comObject: comObject{VTable: &testFocusedPatternVTable}, refs: 1},
		control:   control,
		focus:     1,
		enabled:   1,
		refs:      1,
	}
}

func TestFocusedTextPatternUsesCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name         string
		control      int32
		failProperty int
	}{
		{name: "search combobox", control: 50003},
		{name: "edit", control: 50004},
		{name: "document", control: 50030},
		{name: "custom editor", control: 50025},
		{name: "control type unavailable", control: 50003, failProperty: 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestFocusedElement(tc.control)
			e.failProperty = tc.failProperty
			p, err := focusedTextPattern(&e.comObject)
			if err != nil || p != &e.text.comObject {
				t.Fatalf("focused text pattern = %p, %v", p, err)
			}
			if e.patternCalls != 1 || e.wrongPattern || e.text.refs != 2 {
				t.Fatalf("pattern calls=%d, wrong interface=%v, references=%d", e.patternCalls, e.wrongPattern, e.text.refs)
			}
			if len(e.properties) != 3 || e.properties[0] != 35 || e.properties[1] != 26 || e.properties[2] != 28 {
				t.Fatalf("expected protection/focus/enabled checks before capabilities: %v", e.properties)
			}
			release(p)
			if e.text.refs != 1 || e.refs != 1 {
				t.Fatalf("borrowed references changed: pattern=%d, element=%d", e.text.refs, e.refs)
			}
		})
	}
}

func TestFocusedTextPatternRejectsUnavailableCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name     string
		control  int32
		hr       uintptr
		missing  bool
		failType bool
	}{
		{name: "button", control: 50000, missing: true, hr: 0x80004002},
		{name: "dropdown without text", control: 50003, missing: true},
		{name: "edit without text", control: 50004, missing: true},
		{name: "provider failure with reference", control: 50003, hr: 0x80004005},
		{name: "missing diagnostic type", control: 50003, missing: true, failType: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestFocusedElement(tc.control)
			e.patternMissing, e.patternHR = tc.missing, tc.hr
			if tc.failType {
				e.failProperty = 21
			}
			p, err := focusedTextPattern(&e.comObject)
			if err == nil || p != nil {
				t.Fatalf("unsupported focused control accepted: %p, %v", p, err)
			}
			if e.patternCalls != 1 || e.wrongPattern || e.text.refs != 1 || e.refs != 1 {
				t.Fatalf("calls=%d, wrong interface=%v, pattern refs=%d, element refs=%d", e.patternCalls, e.wrongPattern, e.text.refs, e.refs)
			}
		})
	}
}

func TestFocusedTextPatternChecksProtectionBeforePatterns(t *testing.T) {
	for _, tc := range []struct {
		name         string
		password     int32
		focus        int32
		enabled      int32
		failProperty int
	}{
		{name: "password", password: 1, focus: 1, enabled: 1},
		{name: "password property unavailable", focus: 1, enabled: 1, failProperty: 35},
		{name: "not focused", enabled: 1},
		{name: "focus property unavailable", focus: 1, enabled: 1, failProperty: 26},
		{name: "disabled", focus: 1},
		{name: "enabled property unavailable", focus: 1, enabled: 1, failProperty: 28},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestFocusedElement(50003)
			e.password, e.focus, e.enabled, e.failProperty = tc.password, tc.focus, tc.enabled, tc.failProperty
			p, err := focusedTextPattern(&e.comObject)
			if err == nil || p != nil || e.patternCalls != 0 {
				t.Fatalf("unsafe control accessed: pattern=%p, error=%v, pattern calls=%d", p, err, e.patternCalls)
			}
			if len(e.properties) == 0 || e.properties[0] != 35 {
				t.Fatalf("password protection was not checked first: %v", e.properties)
			}
			if e.text.refs != 1 || e.refs != 1 {
				t.Fatalf("rejected element changed references: pattern=%d, element=%d", e.text.refs, e.refs)
			}
		})
	}
}

type testReadonlyRange struct {
	comObject
	value           variant
	unknown         *testFocusedPattern
	hr              uintptr
	attributes      []uintptr
	textReads, refs int32
}

var testReadonlyRangeVTable = [96]uintptr{
	2: syscall.NewCallback(func(r *testReadonlyRange) uintptr {
		r.refs--
		return uintptr(r.refs)
	}),
	9: syscall.NewCallback(func(r *testReadonlyRange, attribute uintptr, out *variant) uintptr {
		r.attributes = append(r.attributes, attribute)
		*out = r.value
		if r.unknown != nil {
			r.unknown.refs++
			*(*uintptr)(unsafe.Pointer(&out.Data[0])) = uintptr(unsafe.Pointer(&r.unknown.comObject))
		}
		return r.hr
	}),
	12: syscall.NewCallback(func(r *testReadonlyRange, limit uintptr, out *uintptr) uintptr {
		r.textReads++
		return 0x80004005
	}),
}

func TestCaretEditabilityRequiresPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		vt        uint16
		value     int16
		hr        uintptr
		unknown   bool
		wantError bool
	}{
		{name: "writable", vt: 11},
		{name: "read only", vt: 11, value: -1, wantError: true},
		{name: "noncanonical true", vt: 11, value: 1, wantError: true},
		{name: "empty attribute", wantError: true},
		{name: "wrong scalar type", vt: 3, wantError: true},
		{name: "unsupported or mixed attribute", vt: 13, unknown: true, wantError: true},
		{name: "null unknown attribute", vt: 13, wantError: true},
		{name: "failed writable attribute", vt: 11, hr: 0x80004005, wantError: true},
		{name: "failed attribute with reference", vt: 13, unknown: true, hr: 0x80004005, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &testReadonlyRange{comObject: comObject{VTable: &testReadonlyRangeVTable}, value: variant{VT: tc.vt}, hr: tc.hr, refs: 1}
			*(*int16)(unsafe.Pointer(&r.value.Data[0])) = tc.value
			if tc.unknown {
				r.unknown = &testFocusedPattern{comObject: comObject{VTable: &testFocusedPatternVTable}, refs: 1}
			}
			e := newTestFocusedElement(50003)
			a := &testEditabilityAutomation{comObject: comObject{VTable: &testEditabilityAutomationVTable}}
			_, err := caretEditability(&a.comObject, &e.comObject, &r.comObject)
			if (err != nil) != tc.wantError {
				t.Fatalf("writable check = %v, want error=%v", err, tc.wantError)
			}
			if len(r.attributes) != 1 || r.attributes[0] != 40015 || r.textReads != 0 || r.refs != 1 {
				t.Fatalf("attribute check accessed unexpected data: attrs=%v, text reads=%d, range refs=%d", r.attributes, r.textReads, r.refs)
			}
			if r.unknown != nil && r.unknown.refs != 1 {
				t.Fatalf("attribute VARIANT reference leaked: refs=%d", r.unknown.refs)
			}
		})
	}
}

func TestValidateTextContainerDoesNotRequireFocus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		password     int32
		enabled      int32
		failProperty int
		wantError    bool
	}{
		{name: "unfocused container", enabled: 1},
		{name: "focus unavailable", enabled: 1, failProperty: 26},
		{name: "password container", password: 1, enabled: 1, wantError: true},
		{name: "password unavailable", enabled: 1, failProperty: 35, wantError: true},
		{name: "disabled container", wantError: true},
		{name: "enabled unavailable", enabled: 1, failProperty: 28, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestFocusedElement(50030)
			e.focus, e.password, e.enabled, e.failProperty = 0, tc.password, tc.enabled, tc.failProperty
			err := validateTextElement(&e.comObject, false)
			if (err != nil) != tc.wantError {
				t.Fatalf("container validation = %v, want error=%v", err, tc.wantError)
			}
			for i, slot := range e.properties {
				if slot == 26 || i == 0 && slot != 35 {
					t.Fatalf("unexpected validation order: %v", e.properties)
				}
			}
			if tc.failProperty != 0 && tc.failProperty != 26 && !strings.Contains(err.Error(), "0x80004005") {
				t.Fatalf("property failure lost HRESULT: %v", err)
			}
			if e.patternCalls != 0 || e.valuePatternCalls != 0 {
				t.Fatal("validation requested text capabilities")
			}
		})
	}
}

type testEditableValuePattern struct {
	comObject
	readOnly                 int32
	hr                       uintptr
	missingOutput            bool
	refs, checks, textAccess int32
}

var testEditableValueVTable = [96]uintptr{
	2: syscall.NewCallback(func(v *testEditableValuePattern) uintptr {
		v.refs--
		return uintptr(v.refs)
	}),
	3: syscall.NewCallback(func(v *testEditableValuePattern, value uintptr) uintptr {
		v.textAccess++
		return 0x80004005
	}),
	4: syscall.NewCallback(func(v *testEditableValuePattern, out *uintptr) uintptr {
		v.textAccess++
		return 0x80004005
	}),
	5: syscall.NewCallback(func(v *testEditableValuePattern, out *int32) uintptr {
		v.checks++
		if !v.missingOutput {
			*out = v.readOnly
		}
		return v.hr
	}),
}

type testEditabilityAutomation struct {
	comObject
	unsupported       *testFocusedPattern
	hr                uintptr
	checks            int
	misalignedVariant bool
}

var testEditabilityAutomationVTable = [96]uintptr{
	53: syscall.NewCallback(func(a *testEditabilityAutomation, value *variant, out *int32) uintptr {
		a.checks++
		a.misalignedVariant = uintptr(unsafe.Pointer(value))%16 != 0
		if value.VT == 13 && a.unsupported != nil && *(*uintptr)(unsafe.Pointer(&value.Data[0])) == uintptr(unsafe.Pointer(&a.unsupported.comObject)) {
			*out = 1
		}
		return a.hr
	}),
}

func TestCaretEditabilityFallbackAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		vt                    uint16
		attributeReadOnly     int16
		attributeHR           uintptr
		unknown, unsupported  bool
		checkHR               uintptr
		value                 bool
		valueReadOnly         int32
		valueHR, patternHR    uintptr
		missingPatternOutput  bool
		missingReadonlyOutput bool
		wantValueCalls        int
		wantError, wantSource string
	}{
		{name: "writable attribute without ValuePattern", vt: 11, wantValueCalls: 1, wantSource: "TextPattern.IsReadOnly"},
		{name: "writable attribute unsupported ValuePattern", vt: 11, patternHR: 0x80040204, wantValueCalls: 1, wantSource: "TextPattern.IsReadOnly"},
		{name: "writable attribute unavailable interface", vt: 11, patternHR: 0x80004002, wantValueCalls: 1, wantSource: "TextPattern.IsReadOnly"},
		{name: "agreeing writable patterns", vt: 11, value: true, wantValueCalls: 1, wantSource: "TextPattern.IsReadOnly"},
		{name: "readonly range", vt: 11, attributeReadOnly: -1, value: true, wantError: "TextRange.IsReadOnly: read-only"},
		{name: "noncanonical readonly range", vt: 11, attributeReadOnly: 1, value: true, wantError: "TextRange.IsReadOnly: read-only"},
		{name: "contradictory readonly ValuePattern", vt: 11, value: true, valueReadOnly: 1, wantValueCalls: 1, wantError: "ValuePattern.CurrentIsReadOnly: read-only"},
		{name: "explicit unsupported uses writable value", vt: 13, unknown: true, unsupported: true, value: true, wantValueCalls: 1, wantSource: "ValuePattern.IsReadOnly (text attribute unsupported)"},
		{name: "unsupported with readonly value", vt: 13, unknown: true, unsupported: true, value: true, valueReadOnly: 1, wantValueCalls: 1, wantError: "ValuePattern.CurrentIsReadOnly: read-only"},
		{name: "unsupported without value", vt: 13, unknown: true, unsupported: true, wantValueCalls: 1, wantError: "no writable ValuePattern"},
		{name: "mixed token never falls back", vt: 13, unknown: true, value: true, wantError: "mixed or unrecognized"},
		{name: "null token never falls back", vt: 13, value: true, wantError: "null attribute token"},
		{name: "empty attribute never falls back", value: true, wantError: "invalid attribute type 0"},
		{name: "wrong type never falls back", vt: 3, value: true, wantError: "invalid attribute type 3"},
		{name: "failed attribute never falls back", vt: 11, attributeHR: 0x80004005, value: true, wantError: "GetAttributeValue(IsReadOnly) failed (0x80004005)"},
		{name: "failed token attribute releases reference", vt: 13, unknown: true, unsupported: true, attributeHR: 0x80004005, value: true, wantError: "GetAttributeValue(IsReadOnly) failed (0x80004005)"},
		{name: "token check failure never falls back", vt: 13, unknown: true, unsupported: true, checkHR: 0x80004005, value: true, wantError: "CheckNotSupported failed (0x80004005)"},
		{name: "failed ValuePattern query", vt: 11, value: true, patternHR: 0x80004005, wantValueCalls: 1, wantError: "0x80004005"},
		{name: "failed value metadata", vt: 11, value: true, valueHR: 0x80004005, wantValueCalls: 1, wantError: "CurrentIsReadOnly failed (0x80004005)"},
		{name: "missing value metadata", vt: 11, value: true, missingReadonlyOutput: true, wantValueCalls: 1, wantError: "ValuePattern.CurrentIsReadOnly: read-only"},
		{name: "fallback cannot use missing pattern output", vt: 13, unknown: true, unsupported: true, value: true, missingPatternOutput: true, wantValueCalls: 1, wantError: "no writable ValuePattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestFocusedElement(50003)
			e.valuePatternHR, e.valuePatternMissing = tc.patternHR, tc.missingPatternOutput
			if tc.value {
				e.value = &testEditableValuePattern{comObject: comObject{VTable: &testEditableValueVTable}, refs: 1, readOnly: tc.valueReadOnly, hr: tc.valueHR, missingOutput: tc.missingReadonlyOutput}
			}
			r := &testReadonlyRange{comObject: comObject{VTable: &testReadonlyRangeVTable}, value: variant{VT: tc.vt}, hr: tc.attributeHR, refs: 1}
			*(*int16)(unsafe.Pointer(&r.value.Data[0])) = tc.attributeReadOnly
			if tc.unknown {
				r.unknown = &testFocusedPattern{comObject: comObject{VTable: &testFocusedPatternVTable}, refs: 1}
			}
			a := &testEditabilityAutomation{comObject: comObject{VTable: &testEditabilityAutomationVTable}, hr: tc.checkHR}
			if tc.unsupported {
				a.unsupported = r.unknown
			}
			source, err := caretEditability(&a.comObject, &e.comObject, &r.comObject)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || source != "" {
					t.Fatalf("editability = %q, %v; want error containing %q", source, err, tc.wantError)
				}
			} else if err != nil || source != tc.wantSource {
				t.Fatalf("editability = %q, %v; want %q", source, err, tc.wantSource)
			}
			if len(r.attributes) != 1 || r.attributes[0] != 40015 || r.textReads != 0 || r.refs != 1 || e.refs != 1 {
				t.Fatalf("unexpected content or reference access: attrs=%v, text reads=%d, range refs=%d, element refs=%d", r.attributes, r.textReads, r.refs, e.refs)
			}
			if r.unknown != nil && r.unknown.refs != 1 {
				t.Fatalf("attribute token leaked: refs=%d", r.unknown.refs)
			}
			if e.valuePatternCalls != tc.wantValueCalls || e.patternCalls != 0 || e.wrongPattern || a.misalignedVariant {
				t.Fatalf("unexpected pattern/ABI use: Value calls=%d, Text calls=%d, wrong pattern=%v, misaligned=%v", e.valuePatternCalls, e.patternCalls, e.wrongPattern, a.misalignedVariant)
			}
			if e.value != nil && (e.value.refs != 1 || e.value.textAccess != 0) {
				t.Fatalf("ValuePattern leaked/read/wrote content: refs=%d, text access=%d", e.value.refs, e.value.textAccess)
			}
		})
	}
}

// Exercise the native API with reserved metadata tokens only. No application
// element, focus state, document or text content is queried by this test.
func TestUIAReservedAttributeTokens(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := pCoInitializeEx.Call(0, 0)
	if failed(hr) {
		t.Fatalf("CoInitializeEx: 0x%08x", uint32(hr))
	}
	defer pCoUninitialize.Call()
	a, err := createUIAutomation(createAutomationClass)
	if err != nil {
		t.Fatal(err)
	}
	defer release(a)
	for _, tc := range []struct {
		name        string
		slot        int
		unsupported bool
	}{
		{name: "unsupported", slot: 54, unsupported: true},
		{name: "mixed", slot: 55},
	} {
		var token *comObject
		hr := comCall(a, tc.slot, uintptr(unsafe.Pointer(&token)))
		defer release(token)
		if failed(hr) || token == nil {
			t.Fatalf("%s reserved token retrieval failed: 0x%08x", tc.name, uint32(hr))
		}
		v := variant{VT: 13}
		*(*uintptr)(unsafe.Pointer(&v.Data[0])) = uintptr(unsafe.Pointer(token))
		unsupported, err := unsupportedAttribute(a, v)
		if err != nil || unsupported != tc.unsupported {
			t.Fatalf("%s CheckNotSupported = %v, %v; want %v", tc.name, unsupported, err, tc.unsupported)
		}
	}
}
