//go:build windows && amd64

package win

import (
	"syscall"
	"testing"
	"unsafe"
)

// Immutable fake ranges exercise the actual COM endpoint-comparison path. The
// clone shares immutable coordinates but has an independently counted reference.
type testCaretRange struct {
	comObject
	start, end  int32
	refs        int32
	unavailable bool
}

var testCaretVTable = [96]uintptr{
	2: syscall.NewCallback(func(r *testCaretRange) uintptr {
		r.refs--
		return uintptr(r.refs)
	}),
	3: syscall.NewCallback(func(r *testCaretRange, out **comObject) uintptr {
		r.refs++
		*out = &r.comObject
		return 0
	}),
	5: syscall.NewCallback(func(r *testCaretRange, endpoint uintptr, other *testCaretRange, otherEndpoint uintptr, comparison *int32) uintptr {
		if r.unavailable || other.unavailable {
			return 0x80040201 // UIA_E_ELEMENTNOTAVAILABLE
		}
		a, b := r.start, other.start
		if endpoint == 1 {
			a = r.end
		}
		if otherEndpoint == 1 {
			b = other.end
		}
		*comparison = a - b
		return 0
	}),
}

func newTestCaret(start, end int32) *testCaretRange {
	return &testCaretRange{comObject: comObject{VTable: &testCaretVTable}, start: start, end: end, refs: 1}
}

func TestCaretIdentityAcrossCaptures(t *testing.T) {
	var identity caretIdentity
	defer identity.close()
	first, fresh := newTestCaret(10, 10), newTestCaret(10, 10)
	id, err := identity.identify(7, "edit", &first.comObject)
	if err != nil {
		t.Fatal(err)
	}
	again, err := identity.identify(7, "edit", &fresh.comObject)
	if err != nil || id != again {
		t.Fatalf("same logical caret through a fresh COM range changed identity: %q %q %v", id, again, err)
	}
	if first.refs != 1 || fresh.refs != 2 {
		t.Fatalf("retained range ownership: old=%d new=%d", first.refs, fresh.refs)
	}
	for _, tc := range []struct {
		name   string
		window uintptr
		focus  string
		caret  *testCaretRange
	}{
		{"other identical passage", 7, "edit", newTestCaret(50, 50)},
		{"other textbox", 7, "edit2", newTestCaret(50, 50)},
		{"other window", 8, "edit2", newTestCaret(50, 50)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, err := identity.identify(tc.window, tc.focus, &tc.caret.comObject)
			if err != nil || next == again {
				t.Fatalf("caret identity reused: %q %v", next, err)
			}
			again = next
		})
	}
	identity.close()
	if fresh.refs != 1 {
		t.Fatal("retained reference leaked")
	}
}

func TestCaretIdentityFailsClosedOnStaleProviderRange(t *testing.T) {
	var identity caretIdentity
	defer identity.close()
	old := newTestCaret(10, 10)
	id, err := identity.identify(7, "edit", &old.comObject)
	if err != nil {
		t.Fatal(err)
	}
	old.unavailable = true
	fresh := newTestCaret(10, 10)
	next, err := identity.identify(7, "edit", &fresh.comObject)
	if err != nil || id == next {
		t.Fatal("unverifiable caret must invalidate the previous snapshot")
	}
}

func TestCaretComparisonRequiresBothEndpoints(t *testing.T) {
	a, b := newTestCaret(10, 10), newTestCaret(10, 11)
	if sameRangeEndpoints(&a.comObject, &b.comObject) {
		t.Fatal("non-collapsed TextPattern2 range accepted as the selection caret")
	}
	if unsafe.Offsetof(testCaretRange{}.comObject) != 0 {
		t.Fatal("fake COM object must start at the interface pointer")
	}
}
