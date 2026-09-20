//go:build windows && amd64

package uia

import (
	"fmt"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type testPositionProvider struct {
	comObject
	document, caret *testPositionRange
	clones          []*testPositionRange
	failure         string
	forbiddenCalls  int
}

type testPositionRange struct {
	comObject
	provider   *testPositionProvider
	start, end int32
	refs       int32
	moves      int
}

var testPositionPatternVTable = [96]uintptr{
	7: syscall.NewCallback(func(p *testPositionProvider, out **comObject) uintptr {
		if p.failure == "document unavailable" {
			return 0x80040201
		}
		if p.failure == "nil document" {
			return 0
		}
		p.document.refs++
		*out = &p.document.comObject
		if p.failure == "document failure with reference" {
			return 0x80040201
		}
		return 0
	}),
}

var testPositionRangeVTable = [96]uintptr{
	2: syscall.NewCallback(func(r *testPositionRange) uintptr {
		r.refs--
		return uintptr(r.refs)
	}),
	3: syscall.NewCallback(func(r *testPositionRange, out **comObject) uintptr {
		p := r.provider
		if p.failure == "clone unavailable" {
			return 0x80040201
		}
		if p.failure == "nil clone" {
			return 0
		}
		clone := &testPositionRange{
			comObject: comObject{VTable: r.VTable}, provider: p,
			start: r.start, end: r.end, refs: 1,
		}
		p.clones = append(p.clones, clone)
		*out = &clone.comObject
		if p.failure == "clone failure with reference" {
			return 0x80040201
		}
		return 0
	}),
	5: syscall.NewCallback(func(r *testPositionRange, endpoint uintptr, other *testPositionRange, otherEndpoint uintptr, comparison *int32) uintptr {
		p := r.provider
		if p != other.provider ||
			(other == p.document && p.failure == "document comparison unavailable") ||
			(other == p.caret && endpoint == 0 && p.failure == "start comparison unavailable") ||
			(other == p.caret && endpoint == 1 && p.failure == "end comparison unavailable") {
			return 0x80040201
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
	12: syscall.NewCallback(func(r *testPositionRange, count uintptr, out uintptr) uintptr {
		r.provider.forbiddenCalls++ // GetText must never be used for the offset.
		return 0x80004001
	}),
	14: syscall.NewCallback(func(r *testPositionRange, endpoint, unit, count uintptr, moved *int32) uintptr {
		p := r.provider
		if endpoint != 0 || unit != 0 || r == p.caret || r == p.document {
			p.forbiddenCalls++
			return 0x80004001
		}
		r.moves++
		if (r.moves == 1 && p.failure == "backward movement unavailable") ||
			(r.moves == 2 && p.failure == "forward movement unavailable") {
			return 0x80040201
		}
		old := r.start
		r.start += int32(count)
		if r.start < p.document.start {
			r.start = p.document.start
		}
		if r.start > p.document.end {
			r.start = p.document.end
		}
		*moved = r.start - old
		if r.moves == 1 {
			switch p.failure {
			case "positive backward count":
				*moved = 1
			case "oversized backward count":
				*moved = -maxStableCaretOffset - 1
			}
		} else {
			switch p.failure {
			case "inexact forward count":
				*moved--
			case "normalized roundtrip":
				r.start--
			case "changed end":
				r.end++
			}
		}
		if r.start > r.end {
			r.end = r.start
		}
		return 0
	}),
	17: syscall.NewCallback(func(r *testPositionRange) uintptr {
		r.provider.forbiddenCalls++ // Select must never change the actual caret.
		return 0x80004001
	}),
}

func newTestPositionProvider(origin, offset int32) *testPositionProvider {
	p := &testPositionProvider{comObject: comObject{VTable: &testPositionPatternVTable}}
	p.document = &testPositionRange{
		comObject: comObject{VTable: &testPositionRangeVTable}, provider: p,
		start: origin, end: origin + 2*maxStableCaretOffset, refs: 1,
	}
	p.caret = &testPositionRange{
		comObject: comObject{VTable: &testPositionRangeVTable}, provider: p,
		start: origin + offset, end: origin + offset, refs: 1,
	}
	return p
}

func (p *testPositionProvider) assertOwnership(t *testing.T, origin, offset int32) {
	t.Helper()
	if p.document.refs != 1 || p.caret.refs != 1 {
		t.Fatalf("borrowed references changed: document=%d caret=%d", p.document.refs, p.caret.refs)
	}
	for _, clone := range p.clones {
		if clone.refs != 0 {
			t.Fatalf("temporary clone reference leaked: %d", clone.refs)
		}
	}
	if p.document.start != origin || p.document.end != origin+2*maxStableCaretOffset ||
		p.caret.start != origin+offset || p.caret.end != origin+offset || p.forbiddenCalls != 0 {
		t.Fatal("offset probing read text, changed a borrowed range, or changed the selection")
	}
}

func TestStableCaretOffsetAcrossProviderRecreation(t *testing.T) {
	first, fresh := newTestPositionProvider(0, 4), newTestPositionProvider(1000, 4)
	id, ok := stableCaretOffset(&first.comObject, &first.caret.comObject)
	if !ok || id != "uia-offset:4" {
		t.Fatalf("first caret offset = %q, %v", id, ok)
	}
	again, ok := stableCaretOffset(&fresh.comObject, &fresh.caret.comObject)
	if !ok || again != id {
		t.Fatalf("recreated provider changed logical identity: %q, %v", again, ok)
	}
	first.assertOwnership(t, 0, 4)
	fresh.assertOwnership(t, 1000, 4)
}

func TestStableCaretOffsetDistinguishesRepeatedTextPositions(t *testing.T) {
	// These positions may expose identical bounded before/after text. Their
	// position must remain distinct without reading any additional document text.
	ids := make(map[string]bool)
	for _, offset := range []int32{0, 10, 50, maxStableCaretOffset} {
		p := newTestPositionProvider(100, offset)
		id, ok := stableCaretOffset(&p.comObject, &p.caret.comObject)
		if !ok || id != fmt.Sprintf("uia-offset:%d", offset) || ids[id] {
			t.Fatalf("offset %d has missing or reused identity: %q, %v", offset, id, ok)
		}
		ids[id] = true
		p.assertOwnership(t, 100, offset)
	}
}

func TestStableCaretOffsetRejectsUnverifiedPositions(t *testing.T) {
	for _, failure := range []string{
		"document unavailable", "nil document", "document failure with reference",
		"clone unavailable", "nil clone", "clone failure with reference",
		"backward movement unavailable", "forward movement unavailable",
		"document comparison unavailable", "start comparison unavailable", "end comparison unavailable",
		"positive backward count", "oversized backward count", "inexact forward count",
		"normalized roundtrip", "changed end",
	} {
		t.Run(failure, func(t *testing.T) {
			p := newTestPositionProvider(100, 10)
			p.failure = failure
			if id, ok := stableCaretOffset(&p.comObject, &p.caret.comObject); ok || id != "" {
				t.Fatalf("unverified position accepted: %q, %v", id, ok)
			}
			p.assertOwnership(t, 100, 10)
		})
	}
}

func TestStableCaretOffsetLimitExhaustion(t *testing.T) {
	p := newTestPositionProvider(100, maxStableCaretOffset+1)
	if id, ok := stableCaretOffset(&p.comObject, &p.caret.comObject); ok || id != "" {
		t.Fatalf("offset beyond probe limit accepted: %q, %v", id, ok)
	}
	if len(p.clones) != 1 || p.clones[0].moves != 1 {
		t.Fatal("exhausted probe should stop before attempting the roundtrip")
	}
	p.assertOwnership(t, 100, maxStableCaretOffset+1)
}

func TestStableCaretOffsetRejectsMissingInterfaces(t *testing.T) {
	p := newTestPositionProvider(0, 4)
	for _, pair := range [][2]*comObject{
		{nil, nil}, {&p.comObject, nil}, {nil, &p.caret.comObject},
	} {
		if id, ok := stableCaretOffset(pair[0], pair[1]); ok || id != "" {
			t.Fatalf("missing interface accepted: %q, %v", id, ok)
		}
	}
	p.assertOwnership(t, 0, 4)
}

func TestStableCaretOffsetWithWindowsProvider(t *testing.T) {
	edit, update := testPadControl(t)
	w, err := newUIA(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer close(w.jobs)
	for _, offset := range []uintptr{0, 4, 4, 9} {
		update("same same same", offset, offset)
		type result struct {
			id  string
			err error
		}
		reply := make(chan result, 1)
		w.jobs <- func(a *comObject) {
			id, err := readTestPadOffset(a, edit)
			reply <- result{id, err}
		}
		select {
		case got := <-reply:
			if got.err != nil {
				t.Fatal(got.err)
			}
			if want := fmt.Sprintf("uia-offset:%d", offset); got.id != want {
				t.Fatalf("Windows caret at %d = %q, want %q", offset, got.id, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("test pad offset provider timed out")
		}
	}
}

func readTestPadOffset(a *comObject, handle uintptr) (string, error) {
	var el *comObject
	hr := comCall(a, 6, handle, uintptr(unsafe.Pointer(&el))) // ElementFromHandle
	defer release(el)
	if failed(hr) || el == nil {
		return "", fmt.Errorf("test pad ElementFromHandle: 0x%08x", uint32(hr))
	}
	pat, err := pattern(el, 10014, iidText)
	if err != nil {
		return "", err
	}
	defer release(pat)
	var selections *comObject
	hr = comCall(pat, 5, uintptr(unsafe.Pointer(&selections))) // GetSelection
	defer release(selections)
	if failed(hr) || selections == nil {
		return "", fmt.Errorf("test pad GetSelection: 0x%08x", uint32(hr))
	}
	count, err := scalar(selections, 3)
	if err != nil || count != 1 {
		return "", fmt.Errorf("test pad selection count=%d: %v", count, err)
	}
	var caret *comObject
	hr = comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret)))
	defer release(caret)
	if failed(hr) || caret == nil {
		return "", fmt.Errorf("test pad selection range: 0x%08x", uint32(hr))
	}
	id, ok := stableCaretOffset(pat, caret)
	if !ok {
		return "", fmt.Errorf("test pad did not expose an exact caret offset")
	}
	return id, nil
}
