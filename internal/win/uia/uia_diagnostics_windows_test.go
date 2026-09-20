//go:build windows && amd64

package uia

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"
)

type testDiagnosticElement struct {
	comObject
	password, focused, enabled             int32
	failSlot, failProperty                 int
	unknownProperty                        int
	metadataCalls, patternCalls, textCalls int
	properties                             []int
}

func (e *testDiagnosticElement) flag(slot int, value int32, out *int32) uintptr {
	e.properties = append(e.properties, slot)
	if e.failSlot == slot {
		return 0x80040201 // UIA_E_ELEMENTNOTAVAILABLE
	}
	*out = value
	return 0
}

func testDiagnosticString(s string, out **uint16) uintptr {
	units := utf16.Encode([]rune(s))
	var p uintptr
	if len(units) != 0 {
		p = uintptr(unsafe.Pointer(&units[0]))
	}
	b, _, _ := oleaut32.NewProc("SysAllocStringLen").Call(p, uintptr(len(units)))
	runtime.KeepAlive(units)
	*(*uintptr)(unsafe.Pointer(out)) = b // Native BSTR address, freed by the caller.
	return 0
}

var testDiagnosticElementVTable = [96]uintptr{
	11: syscall.NewCallback(func(e *testDiagnosticElement, property uintptr, ignoreDefault uintptr, out *variant) uintptr {
		e.properties = append(e.properties, int(property))
		if ignoreDefault != 1 {
			return 0x80070057
		}
		switch property {
		case 30040, 30119, 30136, 30043:
		default:
			e.textCalls++
			return 0x80070057
		}
		if int(property) == e.failProperty {
			return 0x80040201
		}
		if int(property) != e.unknownProperty {
			out.VT = 11
			if property == 30040 || property == 30043 {
				*(*int16)(unsafe.Pointer(&out.Data[0])) = -1
			}
		}
		return 0
	}),
	14: syscall.NewCallback(func(e *testDiagnosticElement, id uintptr, iid *guid, out **comObject) uintptr {
		e.patternCalls++
		return 0x80004002
	}),
	20: syscall.NewCallback(func(e *testDiagnosticElement, out *int32) uintptr {
		e.metadataCalls++
		return e.flag(20, 4567, out)
	}),
	21: syscall.NewCallback(func(e *testDiagnosticElement, out *int32) uintptr {
		e.metadataCalls++
		return e.flag(21, 50003, out)
	}),
	23: syscall.NewCallback(func(e *testDiagnosticElement, out **uint16) uintptr {
		e.textCalls++
		return testDiagnosticString("secret document text", out)
	}),
	26: syscall.NewCallback(func(e *testDiagnosticElement, out *int32) uintptr {
		return e.flag(26, e.focused, out)
	}),
	28: syscall.NewCallback(func(e *testDiagnosticElement, out *int32) uintptr {
		return e.flag(28, e.enabled, out)
	}),
	30: syscall.NewCallback(func(e *testDiagnosticElement, out **uint16) uintptr {
		e.metadataCalls++
		return testDiagnosticString("Browser\nControl", out)
	}),
	35: syscall.NewCallback(func(e *testDiagnosticElement, out *int32) uintptr {
		return e.flag(35, e.password, out)
	}),
	40: syscall.NewCallback(func(e *testDiagnosticElement, out **uint16) uintptr {
		e.metadataCalls++
		return testDiagnosticString("Chrome", out)
	}),
	51: syscall.NewCallback(func(e *testDiagnosticElement, out **uint16) uintptr {
		e.textCalls++
		return testDiagnosticString("secret window title", out)
	}),
}

func newTestDiagnosticElement() *testDiagnosticElement {
	return &testDiagnosticElement{
		comObject: comObject{VTable: &testDiagnosticElementVTable},
		focused:   1,
		enabled:   1,
	}
}

func TestInspectionMetadataDoesNotReadContent(t *testing.T) {
	el := newTestDiagnosticElement()
	r := &uiaInspection{}
	if err := inspectElementMetadata(context.Background(), &el.comObject, r); err != nil {
		t.Fatal(err)
	}
	report, err := r.finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Control type: 50003", "Framework: Chrome", "Class: Browser Control",
		"Provider process ID: 4567", "TextPattern available: true",
		"TextPattern2 available: false", "TextChildPattern available: false", "ValuePattern available: true",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("missing %q in report: %s", want, report)
		}
	}
	if el.textCalls != 0 || el.patternCalls != 0 || strings.Contains(report, "secret") {
		t.Fatalf("inspection read text: text calls=%d, pattern calls=%d, report=%s", el.textCalls, el.patternCalls, report)
	}
}

func TestInspectionProtectionStopsBeforeMetadataAndPatterns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password int32
		failSlot int
	}{
		{name: "protected", password: 1},
		{name: "unknown protection", failSlot: 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := newTestDiagnosticElement()
			el.password, el.failSlot = tc.password, tc.failSlot
			r := &uiaInspection{}
			err := inspectUIAElement(context.Background(), nil, &el.comObject, r)
			report, returnedErr := r.finish(err)
			if err == nil || returnedErr == nil || !strings.Contains(report, "Stopped at: CurrentIsPassword") {
				t.Fatalf("protected inspection = %s, %v", report, err)
			}
			if len(el.properties) != 1 || el.properties[0] != 35 || el.metadataCalls != 0 || el.patternCalls != 0 || el.textCalls != 0 {
				t.Fatalf("access after protection check: properties=%v, metadata=%d, patterns=%d, text=%d", el.properties, el.metadataCalls, el.patternCalls, el.textCalls)
			}
			if tc.failSlot != 0 && !strings.Contains(report, "0x80040201") {
				t.Fatalf("HRESULT was lost: %s", report)
			}
		})
	}
}

func TestInspectionRetainsMetadataAndUnavailableProperties(t *testing.T) {
	el := newTestDiagnosticElement()
	el.failProperty, el.unknownProperty = 30119, 30136
	r := &uiaInspection{}
	if err := inspectElementMetadata(context.Background(), &el.comObject, r); err != nil {
		t.Fatal(err)
	}
	selectionErr := errors.New("TextPattern.GetSelection: unavailable (0x80040201)")
	report, err := r.finish(selectionErr)
	if !errors.Is(err, selectionErr) {
		t.Fatalf("inspection failure was lost: %v", err)
	}
	for _, want := range []string{
		"Framework: Chrome", "TextPattern2 available: Unavailable (0x80040201)",
		"TextChildPattern available: Unknown", "Stopped at: TextPattern.GetSelection",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("missing %q in report: %s", want, report)
		}
	}
}

func TestInspectionStringAllowlistRejectsContentProperties(t *testing.T) {
	el := newTestDiagnosticElement()
	for _, slot := range []int{23, 29, 31, 42, 51} {
		if value := diagnosticMetadataString(&el.comObject, slot); !strings.Contains(value, "not a metadata property") {
			t.Errorf("slot %d was not rejected: %s", slot, value)
		}
	}
	if el.textCalls != 0 || el.metadataCalls != 0 {
		t.Fatal("disallowed property was read")
	}
}

func TestInspectionCancellationStopsBeforeProviderAccess(t *testing.T) {
	el := newTestDiagnosticElement()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &uiaInspection{}
	err := inspectUIAElement(ctx, nil, &el.comObject, r)
	if !errors.Is(err, context.Canceled) || len(el.properties) != 0 || el.metadataCalls != 0 || el.patternCalls != 0 {
		t.Fatalf("cancelled inspection accessed provider: error=%v, properties=%v", err, el.properties)
	}
}

// Add inspection metadata and a selection to the shared TextChild fixture.
// Content getters remain instrumented separately from the allowed metadata.
type testInspectElement struct {
	testTargetElement
	contentCalls int
}

var testInspectElementVTable = func() [96]uintptr {
	v := testTargetElementVTable
	v[11] = syscall.NewCallback(func(e *testInspectElement, property uintptr, ignoreDefault uintptr, out *variant) uintptr {
		if ignoreDefault != 1 {
			return 0x80070057
		}
		var available bool
		switch property {
		case 30040:
			available = e.text != nil
		case 30119:
		case 30136:
			available = e.child != nil
		case 30043:
			available = e.value != nil
		default:
			e.contentCalls++
			return 0x80070057
		}
		out.VT = 11
		if available {
			*(*int16)(unsafe.Pointer(&out.Data[0])) = -1
		}
		return 0
	})
	v[20] = syscall.NewCallback(func(e *testInspectElement, out *int32) uintptr { *out = 7890; return 0 })
	v[30] = syscall.NewCallback(func(e *testInspectElement, out **uint16) uintptr {
		return testDiagnosticString("BrowserEdit", out)
	})
	v[40] = syscall.NewCallback(func(e *testInspectElement, out **uint16) uintptr {
		return testDiagnosticString("Chrome", out)
	})
	content := syscall.NewCallback(func(e *testInspectElement, out **uint16) uintptr {
		e.contentCalls++
		return testDiagnosticString("secret field contents", out)
	})
	for _, slot := range []int{23, 29, 31, 42, 51} {
		v[slot] = content
	}
	return v
}()

type testInspectValue struct {
	testTargetValue
	contentCalls int
}

var testInspectValueVTable = func() [96]uintptr {
	v := testTargetValueVTable
	v[4] = syscall.NewCallback(func(v *testInspectValue, out **uint16) uintptr {
		v.contentCalls++
		return testDiagnosticString("secret field value", out)
	})
	return v
}()

type testInspectPattern struct {
	testTargetObject
	selection *testInspectSelection
	hr        uintptr
	gets      int
}

var testInspectPatternVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	5: syscall.NewCallback(func(p *testInspectPattern, out **comObject) uintptr {
		p.gets++
		p.selection.refs++
		*out = &p.selection.comObject
		return p.hr
	}),
	8: syscall.NewCallback(func(p *testInspectPattern, out *int32) uintptr { *out = 1; return 0 }),
}

type testInspectSelection struct {
	testTargetObject
	caret *testTargetRange
	count int32
}

var testInspectSelectionVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	3: syscall.NewCallback(func(s *testInspectSelection, out *int32) uintptr { *out = s.count; return 0 }),
	4: syscall.NewCallback(func(s *testInspectSelection, index uintptr, out **comObject) uintptr {
		s.caret.refs++
		*out = &s.caret.comObject
		return 0
	}),
}

type testInspectAutomation struct {
	testTargetAutomation
	current *testTargetElement
	gets    int
	onFocus func()
}

var testInspectAutomationVTable = func() [96]uintptr {
	v := testTargetAutomationVTable
	v[8] = syscall.NewCallback(func(a *testInspectAutomation, out **comObject) uintptr {
		a.gets++
		if a.onFocus != nil {
			a.onFocus()
		}
		a.current.refs++
		*out = &a.current.comObject
		return 0
	})
	return v
}()

type testInspectFixture struct {
	*testTargetFixture
	element *testInspectElement
	value   *testInspectValue
	pattern *testInspectPattern
	array   *testInspectSelection
	caret   *testTargetRange
	client  *testInspectAutomation
}

func newTestInspectFixture(direct bool) *testInspectFixture {
	f := &testInspectFixture{testTargetFixture: newTestTargetFixture()}
	f.element = &testInspectElement{testTargetElement: *f.focus}
	f.element.VTable = &testInspectElementVTable
	f.focus = &f.element.testTargetElement
	f.boundary.enclosing = f.focus
	f.value = &testInspectValue{testTargetValue: *f.testTargetFixture.value}
	f.value.VTable = &testInspectValueVTable
	f.testTargetFixture.value, f.focus.value = &f.value.testTargetValue, &f.value.testTargetValue
	f.caret = &testTargetRange{
		testTargetObject: testTargetRef(&testTargetRangeVTable),
		provider:         f.boundary.provider,
		start:            20,
		end:              20,
		enclosing:        f.focus,
	}
	f.array = &testInspectSelection{testTargetObject: testTargetRef(&testInspectSelectionVTable), caret: f.caret, count: 1}
	f.pattern = &testInspectPattern{testTargetObject: testTargetRef(&testInspectPatternVTable), selection: f.array}
	f.text, f.owner.text = &f.pattern.testTargetObject, &f.pattern.testTargetObject
	if direct {
		f.focus.text, f.focus.textHR = f.text, 0
	}
	f.client = &testInspectAutomation{testTargetAutomation: *f.automation, current: f.focus}
	f.client.VTable = &testInspectAutomationVTable
	f.automation = &f.client.testTargetAutomation
	return f
}

func (f *testInspectFixture) assertMetadataOnly(t *testing.T) {
	t.Helper()
	f.assertReleased(t)
	if f.element.contentCalls != 0 || f.value.contentCalls != 0 || len(f.caret.provider.reads) != 0 {
		t.Errorf("inspection read content: element calls=%d, Value calls=%d, text reads=%v", f.element.contentCalls, f.value.contentCalls, f.caret.provider.reads)
	}
	if f.array.refs != 1 || f.caret.refs != 1 {
		t.Errorf("inspection leaked selection/caret references: array=%d, caret=%d", f.array.refs, f.caret.refs)
	}
	if f.array.caret.refs != 1 || f.child.boundary.refs != 1 {
		t.Errorf("inspection leaked refreshed ranges: caret=%d, boundary=%d", f.array.caret.refs, f.child.boundary.refs)
	}
}

func TestInspectionResolvesCaretWithoutReadingText(t *testing.T) {
	for _, direct := range []bool{true, false} {
		name, scope, source := "TextChild", "TextChild enclosing range", "TextChildPattern"
		if direct {
			name, scope, source = "direct", "Focused control", "TextPattern"
		}
		t.Run(name, func(t *testing.T) {
			f := newTestInspectFixture(direct)
			r := &uiaInspection{}
			err := inspectUIAElement(context.Background(), &f.client.comObject, &f.focus.comObject, r)
			report, _ := r.finish(err)
			if err != nil {
				t.Fatalf("inspection failed: %s", report)
			}
			for _, want := range []string{
				"Framework: Chrome", "Class: BrowserEdit", "Read scope: " + scope,
				"Text source: " + source, "SupportedTextSelection: Single (1)",
				"Selection: One collapsed range", "Editability evidence: TextPattern.IsReadOnly",
			} {
				if !strings.Contains(report, want) {
					t.Errorf("missing %q in %s", want, report)
				}
			}
			if f.client.gets != 1 || f.pattern.gets != 2 {
				t.Errorf("focus and selection not revalidated: focus reads=%d, selection reads=%d", f.client.gets, f.pattern.gets)
			}
			f.assertMetadataOnly(t)
		})
	}
}

func TestInspectionRetainsMetadataWhenCaretCannotBeVerified(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testInspectFixture)
		want   string
	}{
		{"selected text", func(f *testInspectFixture) { f.caret.end++ }, "selected text is not replaced"},
		{"multiple selections", func(f *testInspectFixture) { f.array.count = 2 }, "without selecting text"},
		{"selection provider failure", func(f *testInspectFixture) { f.pattern.hr = 0x80040201 }, "0x80040201"},
		{"caret outside field", func(f *testInspectFixture) { f.caret.start, f.caret.end = 40, 40 }, "caret scope verification"},
		{"focus changed", func(f *testInspectFixture) {
			other := *f.element
			other.id = 99
			f.client.current = &other.testTargetElement
		}, "focus and caret verification"},
		{"owner changed during inspection", func(f *testInspectFixture) {
			f.client.onFocus = func() { f.owner.id = 88 }
		}, "textbox or text container changed"},
		{"boundary changed during inspection", func(f *testInspectFixture) {
			f.client.onFocus = func() {
				boundary := *f.boundary
				boundary.refs = 1
				boundary.start++
				f.child.boundary = &boundary
			}
		}, "focused field boundary changed"},
		{"selection appeared during inspection", func(f *testInspectFixture) {
			f.client.onFocus = func() {
				selection := *f.caret
				selection.refs = 1
				selection.end++
				f.array.caret = &selection
			}
		}, "selected text is not replaced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestInspectFixture(false)
			tc.change(f)
			r := &uiaInspection{}
			err := inspectUIAElement(context.Background(), &f.client.comObject, &f.focus.comObject, r)
			report, returnedErr := r.finish(err)
			if err == nil || returnedErr == nil || !strings.Contains(report, tc.want) {
				t.Fatalf("inspection failure = %s, %v; want %q", report, err, tc.want)
			}
			for _, want := range []string{"Framework: Chrome", "Text source: TextChildPattern", "Read scope: TextChild enclosing range", "Stopped at:"} {
				if !strings.Contains(report, want) {
					t.Errorf("lost metadata %q after failure: %s", want, report)
				}
			}
			if f.client.current.refs != 1 {
				t.Errorf("fresh focused element leaked: refs=%d", f.client.current.refs)
			}
			f.assertMetadataOnly(t)
		})
	}
}
