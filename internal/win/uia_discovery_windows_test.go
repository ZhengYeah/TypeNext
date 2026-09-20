//go:build windows && amd64

package win

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"typenext/internal/core"
	"unicode/utf16"
	"unsafe"
)

type discoveryElement struct {
	comObject
	password, enabled, focused int32
	unknownPassword            bool
	text, value, legacy        *comObject
	patterns                   []int
}

var discoveryElementVTable = [96]uintptr{
	2: syscall.NewCallback(func(*discoveryElement) uintptr { return 1 }),
	14: syscall.NewCallback(func(e *discoveryElement, id uintptr, iid *guid, out **comObject) uintptr {
		e.patterns = append(e.patterns, int(id))
		switch id {
		case 10024:
			*out = e.text
		case 10002:
			*out = e.value
		case 10018:
			*out = e.legacy
		}
		if *out == nil {
			return 0x80004002
		}
		return 0
	}),
	26: syscall.NewCallback(func(e *discoveryElement, out *int32) uintptr { *out = e.focused; return 0 }),
	28: syscall.NewCallback(func(e *discoveryElement, out *int32) uintptr { *out = e.enabled; return 0 }),
	35: syscall.NewCallback(func(e *discoveryElement, out *int32) uintptr {
		if e.unknownPassword {
			return 0x80004005
		}
		*out = e.password
		return 0
	}),
}

func newDiscoveryElement() *discoveryElement {
	return &discoveryElement{comObject: comObject{VTable: &discoveryElementVTable}, enabled: 1, focused: 1}
}

type discoveryTextPattern struct {
	comObject
	selection, caret, document *comObject
	active                     int32
	selectionReads             int
}

var discoveryTextVTable = [96]uintptr{
	2: syscall.NewCallback(func(*discoveryTextPattern) uintptr { return 1 }),
	5: syscall.NewCallback(func(p *discoveryTextPattern, out **comObject) uintptr {
		p.selectionReads++
		*out = p.selection
		if *out == nil {
			return 0x80004005
		}
		return 0
	}),
	7: syscall.NewCallback(func(p *discoveryTextPattern, out **comObject) uintptr { *out = p.document; return 0 }),
	10: syscall.NewCallback(func(p *discoveryTextPattern, active *int32, out **comObject) uintptr {
		*active, *out = p.active, p.caret
		return 0
	}),
}

type discoverySelection struct {
	comObject
	count int32
	caret *comObject
}

var discoverySelectionVTable = [96]uintptr{
	2: syscall.NewCallback(func(*discoverySelection) uintptr { return 1 }),
	3: syscall.NewCallback(func(s *discoverySelection, out *int32) uintptr { *out = s.count; return 0 }),
	4: syscall.NewCallback(func(s *discoverySelection, index uintptr, out **comObject) uintptr { *out = s.caret; return 0 }),
}

func TestDiscoveryDoesNotRequireEditOrDocument(t *testing.T) {
	for _, control := range []int32{50004, 50020, 50025, 50030, 50033, 50000} {
		if !trackingControlAllowed(control, true) {
			t.Fatalf("text capability rejected for ControlType %d", control)
		}
	}
	for _, control := range []int32{50020, 50025, 50033} {
		// Custom, Text, Pane remain eligible without exposing a text interface.
		if !trackingControlAllowed(control, false) {
			t.Fatalf("wrapper %d cannot use keyboard fallback", control)
		}
	}
	if trackingControlAllowed(50000, false) {
		t.Fatal("a plain Button must not record text context")
	}
}

func TestDiscoveryChecksProtectionBeforePatterns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*discoveryElement)
	}{
		{"password", func(e *discoveryElement) { e.password = 1 }},
		{"unknown protection", func(e *discoveryElement) { e.unknownPassword = true }},
		{"disabled", func(e *discoveryElement) { e.enabled = 0 }},
		{"unfocused", func(e *discoveryElement) { e.focused = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := newDiscoveryElement()
			tc.setup(el)
			_, _, _, err := focusedValue(&el.comObject)
			if err == nil || len(el.patterns) != 0 {
				t.Fatalf("unsafe field queried patterns: %v %v", el.patterns, err)
			}
		})
	}
}

func TestDiscoveryRejectsUnrelatedInactiveProvider(t *testing.T) {
	el := newDiscoveryElement()
	el.focused = 0
	pat := &discoveryTextPattern{comObject: comObject{VTable: &discoveryTextVTable}}
	el.text = &pat.comObject
	probe := &uiaFocusProbe{}
	_, err := probe.readProvider(&el.comObject, false, core.Config{}, &caretIdentity{})
	if !errors.Is(err, errNoTextProvider) || pat.selectionReads != 0 {
		t.Fatalf("inactive neighboring text was inspected: reads=%d err=%v", pat.selectionReads, err)
	}
	if len(el.patterns) != 1 || el.patterns[0] != 10024 {
		t.Fatalf("TextPattern2 was not independently probed first: %v", el.patterns)
	}
}

func TestDiscoveryKnownSelectionBlocksFallback(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count, start, end int32
		unavailable       bool
	}{
		{"selected text", 1, 4, 8, false},
		{"multiple carets", 2, 4, 4, false},
		{"missing selection", 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := newDiscoveryElement()
			rangeRef := newTestCaret(tc.start, tc.end)
			selection := &discoverySelection{comObject: comObject{VTable: &discoverySelectionVTable}, count: tc.count, caret: &rangeRef.comObject}
			pat := &discoveryTextPattern{comObject: comObject{VTable: &discoveryTextVTable}, selection: &selection.comObject}
			el.text = &pat.comObject
			_, err := (&uiaFocusProbe{}).readProvider(&el.comObject, true, core.Config{}, &caretIdentity{})
			if err == nil || errors.Is(err, errNoTextProvider) != tc.unavailable {
				t.Fatalf("selection/fallback classification: %v", err)
			}
		})
	}
}

func TestDiscoveryRequiresMatchingActiveCaret(t *testing.T) {
	el := newDiscoveryElement()
	el.focused = 0
	selectionRange, activeRange := newTestCaret(4, 4), newTestCaret(40, 40)
	selection := &discoverySelection{comObject: comObject{VTable: &discoverySelectionVTable}, count: 1, caret: &selectionRange.comObject}
	pat := &discoveryTextPattern{comObject: comObject{VTable: &discoveryTextVTable}, selection: &selection.comObject, caret: &activeRange.comObject, active: 1}
	el.text = &pat.comObject
	_, err := (&uiaFocusProbe{}).readProvider(&el.comObject, false, core.Config{}, &caretIdentity{})
	if !errors.Is(err, errNoTextProvider) {
		t.Fatalf("stale neighbor selection accepted: %v", err)
	}
}

type discoveryValuePattern struct {
	comObject
	readonly, state, role int32
	value                 string
	reads                 int
}

var discoveryAllocString = oleaut32.NewProc("SysAllocStringLen")
var discoveryValueVTable = [96]uintptr{
	2:  syscall.NewCallback(func(*discoveryValuePattern) uintptr { return 1 }),
	4:  syscall.NewCallback(discoveryReadValue),
	8:  syscall.NewCallback(discoveryReadValue),
	5:  syscall.NewCallback(func(p *discoveryValuePattern, out *int32) uintptr { *out = p.readonly; return 0 }),
	10: syscall.NewCallback(func(p *discoveryValuePattern, out *int32) uintptr { *out = p.role; return 0 }),
	11: syscall.NewCallback(func(p *discoveryValuePattern, out *int32) uintptr { *out = p.state; return 0 }),
}

func TestDiscoveryValueReadRequiresWritePermission(t *testing.T) {
	el := newDiscoveryElement()
	value := &discoveryValuePattern{comObject: comObject{VTable: &discoveryValueVTable}, readonly: 1, value: "must not be read"}
	el.value = &value.comObject
	if _, _, _, err := focusedValue(&el.comObject); err == nil || value.reads != 0 {
		t.Fatalf("read-only value read: %d %v", value.reads, err)
	}
	el.value = nil
	el.legacy = &value.comObject
	value.state = 0x20000000
	if _, _, _, err := focusedValue(&el.comObject); err == nil || value.reads != 0 {
		t.Fatalf("protected legacy value read: %d %v", value.reads, err)
	}
}

func TestDiscoveryValueDoesNotPretendTruncatedTextIsComplete(t *testing.T) {
	el := newDiscoveryElement()
	value := &discoveryValuePattern{comObject: comObject{VTable: &discoveryValueVTable}}
	el.value = &value.comObject
	for _, tc := range []struct {
		text  string
		known bool
	}{{"", true}, {"你好 example", true}, {"embedded\x00text", true}, {strings.Repeat("x", maxFallbackValue), true}, {strings.Repeat("x", maxFallbackValue+1), false}} {
		value.value = tc.text
		got, known, _, err := focusedValue(&el.comObject)
		if err != nil || known != tc.known || (known && got != tc.text) || (!known && got != "") {
			t.Fatalf("length=%d known=%v err=%v", len(tc.text), known, err)
		}
	}
}

func TestDiscoveryMarksOnlyProvenDocumentBoundaries(t *testing.T) {
	doc := newTestCaret(0, 100)
	pat := &discoveryTextPattern{comObject: comObject{VTable: &discoveryTextVTable}, document: &doc.comObject}
	for _, tc := range []struct {
		start, end    int32
		before, after bool
	}{{0, 100, true, true}, {0, 80, true, false}, {20, 100, false, true}, {20, 80, false, false}} {
		before, after := newTestCaret(tc.start, 40), newTestCaret(40, tc.end)
		gotBefore, gotAfter := knownRangeBoundaries(&pat.comObject, &before.comObject, &after.comObject)
		if gotBefore != tc.before || gotAfter != tc.after {
			t.Fatalf("boundaries %d..%d: %v %v", tc.start, tc.end, gotBefore, gotAfter)
		}
	}
}

func discoveryReadValue(p *discoveryValuePattern, out **uint16) uintptr {
	p.reads++
	u := utf16.Encode([]rune(p.value))
	var addr uintptr
	if len(u) > 0 {
		addr = uintptr(unsafe.Pointer(&u[0]))
	}
	b, _, _ := discoveryAllocString.Call(addr, uintptr(len(u)))
	// BSTR memory is owned by OleAut32, outside the Go heap.
	*(*uintptr)(unsafe.Pointer(out)) = b
	return 0
}

func TestDiscoveryLegacyValueIsOnlyTextWithWritableState(t *testing.T) {
	el := newDiscoveryElement()
	value := &discoveryValuePattern{comObject: comObject{VTable: &discoveryValueVTable}, role: 42, value: "legacy committed text"}
	el.legacy = &value.comObject
	got, known, source, err := focusedValue(&el.comObject)
	if err != nil || !known || got != value.value || source != "uia-legacy" {
		t.Fatalf("legacy text not read: %q %v %q %v", got, known, source, err)
	}
	value.role, value.reads = 51, 0 // ROLE_SYSTEM_SLIDER
	_, known, _, err = focusedValue(&el.comObject)
	if err != nil || known || value.reads != 0 {
		t.Fatalf("non-text legacy value read: known=%v reads=%d err=%v", known, value.reads, err)
	}
}

func TestDiscoveryDiagnosticContainsOnlyMetadata(t *testing.T) {
	el := newDiscoveryElement()
	value := &discoveryValuePattern{comObject: comObject{VTable: &discoveryValueVTable}, value: "private text"}
	el.value = &value.comObject
	got := capabilityDiagnostic(&el.comObject, 50025, 7)
	if !strings.Contains(got, "ControlType=50025") || !strings.Contains(got, "Value=true") || !strings.Contains(got, "7 nearby") || value.reads != 0 || strings.Contains(got, value.value) {
		t.Fatalf("unsafe or incomplete diagnostic: %q reads=%d", got, value.reads)
	}
}

func TestDiscoveryProtectedDescendantCannotEnableKeyboardFallback(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		password, focus           int32
		unknown, blocked, descend bool
	}{
		{"protected child", 1, 0, false, true, false},
		{"focused unknown child", 0, 1, true, true, false},
		{"unknown unrelated branch", 0, 0, true, false, false},
		{"safe child", 0, 0, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := newDiscoveryElement()
			el.password, el.focused, el.unknownPassword = tc.password, tc.focus, tc.unknown
			descend, err := safeDescendantBranch(&el.comObject)
			if (err != nil) != tc.blocked || descend != tc.descend {
				t.Fatalf("branch policy: descend=%v err=%v", descend, err)
			}
			if (tc.password != 0 || tc.unknown) && len(el.patterns) != 0 {
				t.Fatal("unsafe child queried patterns")
			}
		})
	}
	el := newDiscoveryElement()
	el.focused = 0
	legacy := &discoveryValuePattern{comObject: comObject{VTable: &discoveryValueVTable}, state: 0x20000000}
	el.legacy = &legacy.comObject
	if _, err := safeDescendantBranch(&el.comObject); err == nil || legacy.reads != 0 {
		t.Fatalf("legacy protected descendant bypassed protection: %v", err)
	}
}
