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

type testContextProvider struct {
	text   []uint16
	unit   int32
	clones []*testContextRange
	reads  []int
	onRead func()
}

type testContextRange struct {
	comObject
	provider   *testContextProvider
	start, end int32
	refs       int32
}

var testContextRangeVTable = [96]uintptr{
	2: syscall.NewCallback(func(r *testContextRange) uintptr {
		r.refs--
		return uintptr(r.refs)
	}),
	3: syscall.NewCallback(func(r *testContextRange, out **comObject) uintptr {
		clone := &testContextRange{comObject: r.comObject, provider: r.provider, start: r.start, end: r.end, refs: 1}
		r.provider.clones = append(r.provider.clones, clone)
		*out = &clone.comObject
		return 0
	}),
	12: syscall.NewCallback(func(r *testContextRange, limit uintptr, out *uintptr) uintptr {
		p := r.provider
		p.reads = append(p.reads, int(limit))
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
		*out = b // Native BSTR address, owned and freed by the caller.
		if p.onRead != nil {
			p.onRead()
		}
		return 0
	}),
	14: syscall.NewCallback(func(r *testContextRange, endpoint, unit, count uintptr, moved *int32) uintptr {
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
}

func newTestContextRange(text string, position, unit int32) *testContextRange {
	p := &testContextProvider{text: utf16.Encode([]rune(text)), unit: unit}
	return &testContextRange{comObject: comObject{VTable: &testContextRangeVTable}, provider: p, start: position, end: position, refs: 1}
}

func (r *testContextRange) assertReleased(t *testing.T) {
	t.Helper()
	if r.refs != 1 || r.start != r.end {
		t.Fatal("context reading changed the borrowed caret")
	}
	for _, clone := range r.provider.clones {
		if clone.refs != 0 {
			t.Fatal("context reading leaked a clone")
		}
	}
	if len(r.provider.reads) > maxUIAPrefixReads {
		t.Fatal("context reading exceeded its attempt budget")
	}
}

func TestReadCaretPrefixKeepsCaretAdjacencyWithPromotedUnits(t *testing.T) {
	for _, unit := range []int32{1, 4, 5} {
		text := strings.Repeat("old ", 100) + "adjacent"
		r := newTestContextRange(text, int32(len(text)), unit)
		got, err := readCaretSide(context.Background(), &r.comObject, 0, 8)
		if err != nil || got != "adjacent" {
			t.Fatalf("unit %d returned a non-adjacent prefix %q: %v", unit, got, err)
		}
		for _, limit := range r.provider.reads {
			if limit != 20 {
				t.Fatalf("prefix increased its text budget: %d", limit)
			}
		}
		if unit == 5 && len(r.provider.reads) != 3 {
			t.Fatal("an exactly full BSTR must be treated as potentially truncated")
		}
		r.assertReleased(t)
	}
}

func TestReadCaretPrefixRejectsOversizedSingleUnit(t *testing.T) {
	r := newTestContextRange(strings.Repeat("x", 200), 200, 64)
	got, err := readCaretSide(context.Background(), &r.comObject, 0, 8)
	if err == nil || got != "" || len(r.provider.reads) != 4 {
		t.Fatalf("oversized text unit accepted: %q, %v, reads=%d", got, err, len(r.provider.reads))
	}
	r.assertReleased(t)
}

func TestReadCaretSuffixAllowsFarEndTruncation(t *testing.T) {
	r := newTestContextRange("adjacent"+strings.Repeat(" old", 100), 0, 64)
	got, err := readCaretSide(context.Background(), &r.comObject, 1, 8)
	if err != nil || got != "adjacent" || len(r.provider.reads) != 1 {
		t.Fatalf("suffix lost caret adjacency: %q, %v", got, err)
	}
	r.assertReleased(t)
}

func TestReadCaretZeroContextDoesNotReadText(t *testing.T) {
	r := newTestContextRange("private suffix", 0, 1)
	got, err := readCaretSide(context.Background(), &r.comObject, 1, 0)
	if err != nil || got != "" || len(r.provider.reads) != 0 || len(r.provider.clones) != 0 {
		t.Fatalf("zero context accessed text: %q, %v", got, err)
	}
}

func TestBoundedRangeTextPreservesUnicodeAndEmbeddedNUL(t *testing.T) {
	text := "a\x00\U0001f600e\u0301"
	r := newTestContextRange(text, 0, 1)
	r.end = int32(len(r.provider.text))
	got, complete, err := boundedRangeText(&r.comObject, 20)
	if err != nil || got != text || !complete {
		t.Fatalf("BSTR content changed: %q, %v, %v", got, complete, err)
	}
	_, complete, err = boundedRangeText(&r.comObject, len(r.provider.text))
	if err != nil || complete {
		t.Fatalf("full UTF-16 buffer was considered complete: %v, %v", complete, err)
	}
}

func TestReadCaretPrefixStopsOnCancellation(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		r := newTestContextRange(strings.Repeat("x", 200), 200, 64)
		ctx, cancel := context.WithCancel(context.Background())
		if duringRead {
			r.provider.onRead = cancel
		} else {
			cancel()
		}
		got, err := readCaretSide(ctx, &r.comObject, 0, 8)
		cancel()
		if got != "" || !errors.Is(err, context.Canceled) || len(r.provider.reads) > 1 {
			t.Fatalf("cancelled prefix continued reading: %q, %v, reads=%d", got, err, len(r.provider.reads))
		}
		r.assertReleased(t)
	}
}

type testSelectionPattern struct {
	comObject
	selection *testSelectionArray
	failure   string
}

type testSelectionArray struct {
	comObject
	pattern *testSelectionPattern
	caret   *testCaretRange
	count   int32
	refs    int32
}

var testSelectionPatternVTable = [96]uintptr{
	5: syscall.NewCallback(func(p *testSelectionPattern, out **comObject) uintptr {
		p.selection.refs++
		*out = &p.selection.comObject
		if p.failure == "selection unavailable" {
			return 0x80040201
		}
		return 0
	}),
}

var testSelectionArrayVTable = [96]uintptr{
	2: syscall.NewCallback(func(a *testSelectionArray) uintptr {
		a.refs--
		return uintptr(a.refs)
	}),
	3: syscall.NewCallback(func(a *testSelectionArray, out *int32) uintptr {
		*out = a.count
		return 0
	}),
	4: syscall.NewCallback(func(a *testSelectionArray, index uintptr, out **comObject) uintptr {
		a.caret.refs++
		*out = &a.caret.comObject
		if a.pattern.failure == "caret unavailable" {
			return 0x80040201
		}
		return 0
	}),
}

func newTestSelection(caret *testCaretRange) *testSelectionPattern {
	p := &testSelectionPattern{comObject: comObject{VTable: &testSelectionPatternVTable}}
	p.selection = &testSelectionArray{comObject: comObject{VTable: &testSelectionArrayVTable}, pattern: p, caret: caret, count: 1, refs: 1}
	return p
}

func TestVerifyCaretSelectionAfterRead(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int32
		count      int32
		failure    string
		wantError  bool
	}{
		{"unchanged caret", 10, 10, 1, "", false},
		{"caret moved in same field", 20, 20, 1, "", true},
		{"new selection", 10, 20, 1, "", true},
		{"multiple selections", 10, 10, 2, "", true},
		{"selection disappeared", 10, 10, 0, "", true},
		{"selection provider failure", 10, 10, 1, "selection unavailable", true},
		{"caret provider failure", 10, 10, 1, "caret unavailable", true},
		{"comparison failure", 10, 10, 1, "comparison unavailable", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected, current := newTestCaret(10, 10), newTestCaret(tc.start, tc.end)
			p := newTestSelection(current)
			p.selection.count, p.failure = tc.count, tc.failure
			expected.unavailable = tc.failure == "comparison unavailable"
			verified, err := verifyCaretSelection(&p.comObject, &expected.comObject)
			release(verified)
			if (err != nil) != tc.wantError {
				t.Fatalf("selection verification = %v, want error=%v", err, tc.wantError)
			}
			if expected.refs != 1 || current.refs != 1 || p.selection.refs != 1 {
				t.Fatalf("verification leaked references: expected=%d, current=%d, array=%d", expected.refs, current.refs, p.selection.refs)
			}
		})
	}
}
