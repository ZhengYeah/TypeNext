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

// These fakes deliberately expose a focused child and an unfocused document.
// Text is readable through the document, but only the child's range is allowed.
type testTargetObject struct {
	comObject
	refs int32
}

var (
	testTargetAddRef = syscall.NewCallback(func(o *testTargetObject) uintptr {
		o.refs++
		return uintptr(o.refs)
	})
	testTargetRelease = syscall.NewCallback(func(o *testTargetObject) uintptr {
		o.refs--
		return uintptr(o.refs)
	})
	testTargetTextChildIID = guid{A: 0x6552b038, B: 0xae05, C: 0x40c8, D: [8]byte{0xab, 0xfd, 0xaa, 0x08, 0x35, 0x2a, 0xab, 0x86}}
	testTargetValueIID     = guid{A: 0xa94cd8b1, B: 0x0844, C: 0x4cd6, D: [8]byte{0x9d, 0x2d, 0x64, 0x05, 0x37, 0xab, 0x39, 0xe9}}
)

type testTargetElement struct {
	testTargetObject
	id, password, focus, enabled int32
	text                         *testTargetObject
	child                        *testTargetChild
	value                        *testTargetValue
	textHR, childHR, valueHR     uintptr
	patternCalls                 []uintptr
	wrongIID                     bool
}

var testTargetElementVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	4: syscall.NewCallback(func(e *testTargetElement, out *uintptr) uintptr {
		sa, _, _ := oleaut32.NewProc("SafeArrayCreateVector").Call(3, 0, 1) // VT_I4
		if sa == 0 {
			return 0x8007000e
		}
		var data unsafe.Pointer
		hr, _, _ := pSafeArrayAccessData.Call(sa, uintptr(unsafe.Pointer(&data)))
		if failed(hr) {
			pSafeArrayDestroy.Call(sa)
			return hr
		}
		*(*int32)(data) = e.id
		pSafeArrayUnaccessData.Call(sa)
		*out = sa
		return 0
	}),
	14: syscall.NewCallback(func(e *testTargetElement, id uintptr, iid *guid, out **comObject) uintptr {
		e.patternCalls = append(e.patternCalls, id)
		var o *testTargetObject
		var want guid
		var hr uintptr
		switch id {
		case 10014:
			o, want, hr = e.text, iidText, e.textHR
		case 10029:
			if e.child != nil {
				o = &e.child.testTargetObject
			}
			want, hr = testTargetTextChildIID, e.childHR
		case 10002:
			if e.value != nil {
				o = &e.value.testTargetObject
			}
			want, hr = testTargetValueIID, e.valueHR
		default:
			return 0x80004002
		}
		if *iid != want {
			e.wrongIID = true
			return 0x80004002
		}
		if o != nil {
			o.refs++
			*out = &o.comObject
		}
		return hr
	}),
	21: syscall.NewCallback(func(e *testTargetElement, out *int32) uintptr { *out = 50003; return 0 }),
	26: syscall.NewCallback(func(e *testTargetElement, out *int32) uintptr { *out = e.focus; return 0 }),
	28: syscall.NewCallback(func(e *testTargetElement, out *int32) uintptr { *out = e.enabled; return 0 }),
	35: syscall.NewCallback(func(e *testTargetElement, out *int32) uintptr { *out = e.password; return 0 }),
}

type testTargetChild struct {
	testTargetObject
	container            *testTargetElement
	boundary             *testTargetRange
	containerHR, rangeHR uintptr
}

var testTargetChildVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	3: syscall.NewCallback(func(c *testTargetChild, out **comObject) uintptr {
		if c.container != nil {
			c.container.refs++
			*out = &c.container.comObject
		}
		return c.containerHR
	}),
	4: syscall.NewCallback(func(c *testTargetChild, out **comObject) uintptr {
		if c.boundary != nil {
			c.boundary.refs++
			*out = &c.boundary.comObject
		}
		return c.rangeHR
	}),
}

type testTargetValue struct {
	testTargetObject
	readonly int32
	hr       uintptr
}

var testTargetValueVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	5: syscall.NewCallback(func(v *testTargetValue, out *int32) uintptr { *out = v.readonly; return v.hr }),
}

type testTargetAutomation struct {
	testTargetObject
	compareHR uintptr
}

var testTargetAutomationVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	3: syscall.NewCallback(func(a *testTargetAutomation, left, right *comObject, out *int32) uintptr {
		if left == right {
			*out = 1
		}
		return a.compareHR
	}),
}

type testTargetRangeProvider struct {
	text                   []uint16
	unit                   int32
	compareHR, moveRangeHR uintptr
	ignoreBoundaryMove     bool
	clones                 []*testTargetRange
	reads                  [][2]int32
}

type testTargetRange struct {
	testTargetObject
	provider    *testTargetRangeProvider
	start, end  int32
	enclosing   *testTargetElement
	enclosingHR uintptr
	readonly    int16
}

func (r *testTargetRange) endpoint(endpoint uintptr) int32 {
	if endpoint == 0 {
		return r.start
	}
	return r.end
}

var testTargetRangeVTable = [96]uintptr{
	1: testTargetAddRef,
	2: testTargetRelease,
	3: syscall.NewCallback(func(r *testTargetRange, out **comObject) uintptr {
		clone := *r
		clone.refs = 1
		r.provider.clones = append(r.provider.clones, &clone)
		*out = &clone.comObject
		return 0
	}),
	5: syscall.NewCallback(func(r *testTargetRange, endpoint uintptr, other *testTargetRange, otherEndpoint uintptr, out *int32) uintptr {
		*out = r.endpoint(endpoint) - other.endpoint(otherEndpoint)
		return r.provider.compareHR
	}),
	9: syscall.NewCallback(func(r *testTargetRange, attribute uintptr, out *variant) uintptr {
		if attribute != 40015 {
			return 0x80004001
		}
		*out = variant{VT: 11}
		*(*int16)(unsafe.Pointer(&out.Data[0])) = r.readonly
		return 0
	}),
	11: syscall.NewCallback(func(r *testTargetRange, out **comObject) uintptr {
		if r.enclosing != nil {
			r.enclosing.refs++
			*out = &r.enclosing.comObject
		}
		return r.enclosingHR
	}),
	12: syscall.NewCallback(func(r *testTargetRange, limit uintptr, out *uintptr) uintptr {
		p := r.provider
		p.reads = append(p.reads, [2]int32{r.start, r.end})
		units := p.text[r.start:r.end]
		if len(units) > int(limit) {
			units = units[:int(limit)]
		}
		var pointer uintptr
		if len(units) > 0 {
			pointer = uintptr(unsafe.Pointer(&units[0]))
		}
		b, _, _ := oleaut32.NewProc("SysAllocStringLen").Call(pointer, uintptr(len(units)))
		runtime.KeepAlive(units)
		*out = b
		return 0
	}),
	14: syscall.NewCallback(func(r *testTargetRange, endpoint, unit, count uintptr, moved *int32) uintptr {
		if unit != 0 || endpoint > 1 {
			return 0x80004001
		}
		position := &r.start
		if endpoint == 1 {
			position = &r.end
		}
		old := *position
		*position += int32(count) * r.provider.unit
		if *position < 0 {
			*position = 0
		}
		if *position > int32(len(r.provider.text)) {
			*position = int32(len(r.provider.text))
		}
		*moved = (*position - old) / r.provider.unit
		return 0
	}),
	15: syscall.NewCallback(func(r *testTargetRange, endpoint uintptr, other *testTargetRange, otherEndpoint uintptr) uintptr {
		if !r.provider.ignoreBoundaryMove {
			if endpoint == 0 {
				r.start = other.endpoint(otherEndpoint)
			} else {
				r.end = other.endpoint(otherEndpoint)
			}
		}
		return r.provider.moveRangeHR
	}),
}

type testTargetFixture struct {
	automation   *testTargetAutomation
	focus, owner *testTargetElement
	text         *testTargetObject
	child        *testTargetChild
	value        *testTargetValue
	boundary     *testTargetRange
}

func testTargetRef(vtable *[96]uintptr) testTargetObject {
	return testTargetObject{comObject: comObject{VTable: vtable}, refs: 1}
}

func newTestTargetFixture() *testTargetFixture {
	f := &testTargetFixture{
		automation: &testTargetAutomation{testTargetObject: testTargetRef(&testTargetAutomationVTable)},
		focus:      &testTargetElement{testTargetObject: testTargetRef(&testTargetElementVTable), id: 1, focus: 1, enabled: 1},
		owner:      &testTargetElement{testTargetObject: testTargetRef(&testTargetElementVTable), id: 2, enabled: 1},
		text:       &testTargetObject{comObject: comObject{VTable: &[96]uintptr{1: testTargetAddRef, 2: testTargetRelease}}, refs: 1},
		child:      &testTargetChild{testTargetObject: testTargetRef(&testTargetChildVTable)},
		value:      &testTargetValue{testTargetObject: testTargetRef(&testTargetValueVTable)},
		boundary:   &testTargetRange{testTargetObject: testTargetRef(&testTargetRangeVTable), provider: &testTargetRangeProvider{unit: 1}, start: 10, end: 30},
	}
	f.focus.child, f.focus.value, f.focus.textHR = f.child, f.value, 0x80004002
	f.owner.text = f.text
	f.child.container, f.child.boundary = f.owner, f.boundary
	f.boundary.enclosing = f.focus
	return f
}

func (f *testTargetFixture) assertReleased(t *testing.T) {
	t.Helper()
	for name, object := range map[string]*testTargetObject{
		"automation": &f.automation.testTargetObject, "focus": &f.focus.testTargetObject,
		"owner": &f.owner.testTargetObject, "text": f.text, "child": &f.child.testTargetObject,
		"value": &f.value.testTargetObject, "boundary": &f.boundary.testTargetObject,
	} {
		if object.refs != 1 {
			t.Errorf("%s references = %d, want borrowed reference only", name, object.refs)
		}
	}
	if f.focus.wrongIID || f.owner.wrongIID {
		t.Error("requested incorrect UIA pattern IID")
	}
	if len(f.boundary.provider.reads) != 0 {
		t.Error("target resolution read application text")
	}
}

func TestResolveTextTargetPrefersFocusedTextPattern(t *testing.T) {
	f := newTestTargetFixture()
	f.focus.text, f.focus.textHR = f.text, 0
	target, err := resolveTextTarget(context.Background(), &f.automation.comObject, &f.focus.comObject)
	if err != nil {
		t.Fatalf("direct target: %v", err)
	}
	if target.owner != &f.focus.comObject || target.pattern != &f.text.comObject || target.boundary != nil {
		t.Errorf("did not retain the focused element's direct text pattern: %+v", target)
	}
	for _, id := range f.focus.patternCalls {
		if id == 10029 {
			t.Error("tried a TextChild fallback despite a direct text pattern")
		}
	}
	target.close()
	f.assertReleased(t)
}

func TestResolveTextTargetUsesVerifiedChildBoundary(t *testing.T) {
	for _, noInterface := range []bool{true, false} {
		f := newTestTargetFixture()
		if !noInterface {
			f.focus.textHR = 0
		} // Providers also indicate absence by returning S_OK + nil.
		target, err := resolveTextTarget(context.Background(), &f.automation.comObject, &f.focus.comObject)
		if err != nil {
			t.Fatalf("TextChild target (no interface=%v): %v", noInterface, err)
		}
		if target.owner != &f.owner.comObject || target.pattern != &f.text.comObject || target.boundary != &f.boundary.comObject {
			t.Errorf("TextChild resolved the wrong owner or field range: %+v", target)
		}
		if target.focusID == "" || target.ownerID == "" || target.focusID == target.ownerID {
			t.Errorf("lost distinct focus/container identities: %+v", target)
		}
		target.close()
		f.assertReleased(t)
	}
}

func TestResolveTextTargetRejectsUnverifiedChildScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testTargetFixture)
	}{
		{"focused password", func(f *testTargetFixture) { f.focus.password = 1 }},
		{"focus lost", func(f *testTargetFixture) { f.focus.focus = 0 }},
		{"focused disabled", func(f *testTargetFixture) { f.focus.enabled = 0 }},
		{"direct provider failed", func(f *testTargetFixture) { f.focus.textHR = 0x80004005 }},
		{"direct provider failed with reference", func(f *testTargetFixture) { f.focus.text, f.focus.textHR = f.text, 0x80004005 }},
		{"missing TextChild", func(f *testTargetFixture) { f.focus.child = nil }},
		{"TextChild failed with reference", func(f *testTargetFixture) { f.focus.childHR = 0x80004005 }},
		{"missing container", func(f *testTargetFixture) { f.child.container = nil }},
		{"container failed with reference", func(f *testTargetFixture) { f.child.containerHR = 0x80004005 }},
		{"container is focused element", func(f *testTargetFixture) { f.child.container = f.focus }},
		{"container password", func(f *testTargetFixture) { f.owner.password = 1 }},
		{"container disabled", func(f *testTargetFixture) { f.owner.enabled = 0 }},
		{"container lacks TextPattern", func(f *testTargetFixture) { f.owner.text = nil }},
		{"missing field writable metadata", func(f *testTargetFixture) { f.focus.value = nil }},
		{"field read only", func(f *testTargetFixture) { f.value.readonly = 1 }},
		{"field writable metadata failed", func(f *testTargetFixture) { f.value.hr = 0x80004005 }},
		{"missing field boundary", func(f *testTargetFixture) { f.child.boundary = nil }},
		{"field boundary failed with reference", func(f *testTargetFixture) { f.child.rangeHR = 0x80004005 }},
		{"boundary read only", func(f *testTargetFixture) { f.boundary.readonly = -1 }},
		{"boundary covers whole document", func(f *testTargetFixture) { f.boundary.enclosing = f.owner }},
		{"boundary enclosing element missing", func(f *testTargetFixture) { f.boundary.enclosing = nil }},
		{"boundary enclosing element failed with reference", func(f *testTargetFixture) { f.boundary.enclosingHR = 0x80004005 }},
		{"element comparison failed", func(f *testTargetFixture) { f.automation.compareHR = 0x80004005 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestTargetFixture()
			tc.change(f)
			target, err := resolveTextTarget(context.Background(), &f.automation.comObject, &f.focus.comObject)
			if target != nil {
				target.close()
			}
			if err == nil || target != nil {
				t.Errorf("unverified target accepted: %p, %v", target, err)
			}
			f.assertReleased(t)
		})
	}
}

func TestResolveTextTargetHonorsCancellation(t *testing.T) {
	f := newTestTargetFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target, err := resolveTextTarget(ctx, &f.automation.comObject, &f.focus.comObject)
	if target != nil {
		target.close()
	}
	if target != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled resolution = %p, %v", target, err)
	}
	if len(f.focus.patternCalls) != 0 {
		t.Error("cancelled resolution accessed provider patterns")
	}
	f.assertReleased(t)
}

func newTestBoundedCaret(unit int32) (*testTargetRange, *testTargetRange) {
	left, field, right := strings.Repeat("private-left ", 20), "before|after", strings.Repeat(" private-right", 20)
	p := &testTargetRangeProvider{text: utf16.Encode([]rune(left + field + right)), unit: unit}
	start := int32(len(left))
	boundary := &testTargetRange{testTargetObject: testTargetRef(&testTargetRangeVTable), provider: p, start: start, end: start + int32(len(field))}
	caret := &testTargetRange{testTargetObject: testTargetRef(&testTargetRangeVTable), provider: p, start: start + 7, end: start + 7}
	return caret, boundary
}

func assertBoundedCaretUnchanged(t *testing.T, caret, boundary *testTargetRange, position, start, end int32) {
	t.Helper()
	if caret.refs != 1 || boundary.refs != 1 || caret.start != position || caret.end != position || boundary.start != start || boundary.end != end {
		t.Error("bounded read mutated or released borrowed ranges")
	}
	for _, clone := range caret.provider.clones {
		if clone.refs != 0 {
			t.Errorf("bounded read leaked clone with %d references", clone.refs)
		}
	}
	for _, span := range caret.provider.reads {
		if span[0] < start || span[1] > end {
			t.Errorf("read neighboring text at %v outside [%d,%d]", span, start, end)
		}
	}
}

func TestReadCaretSideWithinClampsBeforeReading(t *testing.T) {
	for _, unit := range []int32{1, 4, 64} {
		for endpoint, want := range []string{"before|", "after"} {
			caret, boundary := newTestBoundedCaret(unit)
			position, start, end := caret.start, boundary.start, boundary.end
			got, err := readCaretSideWithin(context.Background(), &caret.comObject, &boundary.comObject, endpoint, 40)
			if err != nil || got != want {
				t.Errorf("unit %d endpoint %d = %q, %v; want %q", unit, endpoint, got, err, want)
			}
			if len(caret.provider.reads) != 1 {
				t.Errorf("bounded field read count = %d", len(caret.provider.reads))
			}
			assertBoundedCaretUnchanged(t, caret, boundary, position, start, end)
		}
	}
}

func TestReadCaretSideWithinRejectsScopeFailureBeforeText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testTargetRange, *testTargetRange)
	}{
		{"caret before field", func(c, b *testTargetRange) { c.start, c.end = b.start-1, b.start-1 }},
		{"caret after field", func(c, b *testTargetRange) { c.start, c.end = b.end+1, b.end+1 }},
		{"comparison failed", func(c, b *testTargetRange) { c.provider.compareHR = 0x80004005 }},
		{"boundary movement failed", func(c, b *testTargetRange) { c.provider.moveRangeHR = 0x80004005 }},
		{"provider ignored boundary movement", func(c, b *testTargetRange) { c.provider.ignoreBoundaryMove = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []int{0, 1} {
				caret, boundary := newTestBoundedCaret(64)
				tc.change(caret, boundary)
				position, start, end := caret.start, boundary.start, boundary.end
				got, err := readCaretSideWithin(context.Background(), &caret.comObject, &boundary.comObject, endpoint, 40)
				if err == nil || got != "" || len(caret.provider.reads) != 0 {
					t.Errorf("endpoint %d read outside verified scope: %q, %v, reads=%v", endpoint, got, err, caret.provider.reads)
				}
				assertBoundedCaretUnchanged(t, caret, boundary, position, start, end)
			}
		})
	}
}

func TestRangeWithinBoundaryAllowsEdgesAndRejectsEscape(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int32
		wantError  bool
	}{
		{"at start", 0, 0, false}, {"at end", 12, 12, false}, {"whole field", 0, 12, false},
		{"prefix escape", -1, 5, true}, {"suffix escape", 5, 13, true}, {"both sides escape", -1, 13, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caret, boundary := newTestBoundedCaret(1)
			caret.start, caret.end = boundary.start+tc.start, boundary.start+tc.end
			err := rangeWithinBoundary(&caret.comObject, &boundary.comObject)
			if (err != nil) != tc.wantError {
				t.Errorf("containment error=%v, want error=%v", err, tc.wantError)
			}
			if len(caret.provider.reads) != 0 {
				t.Error("containment verification read application text")
			}
		})
	}
}
