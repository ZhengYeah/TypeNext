//go:build windows && amd64

package win

import (
	"context"
	"syscall"
	"testing"
	"unsafe"
)

type testGeometryRange struct {
	comObject
	provider   *testGeometryProvider
	start, end int
	refs       int
}

type testGeometryProvider struct {
	length, caret, forbiddenCalls int
	noPrevious, unavailable       bool
	clones                        []*testGeometryRange
	cancel                        context.CancelFunc
}

var testGeometryRangeVTable = [96]uintptr{
	2: syscall.NewCallback(func(r *testGeometryRange) uintptr {
		r.refs--
		return uintptr(r.refs)
	}),
	3: syscall.NewCallback(func(r *testGeometryRange, out **comObject) uintptr {
		clone := &testGeometryRange{comObject: comObject{VTable: r.VTable}, provider: r.provider, start: r.start, end: r.end, refs: 1}
		r.provider.clones = append(r.provider.clones, clone)
		*out = &clone.comObject
		return 0
	}),
	10: syscall.NewCallback(func(r *testGeometryRange, out *uintptr) uintptr {
		p := r.provider
		if p.cancel != nil {
			p.cancel()
			p.cancel = nil
		}
		if r.start == r.end || p.unavailable || (p.noPrevious && r.start < p.caret) {
			return 0x80004005
		}
		sa, _, _ := oleaut32.NewProc("SafeArrayCreateVector").Call(5, 0, 4) // VT_R8
		if sa == 0 {
			return 0x8007000e
		}
		var data unsafe.Pointer
		hr, _, _ := pSafeArrayAccessData.Call(sa, uintptr(unsafe.Pointer(&data)))
		if failed(hr) {
			pSafeArrayDestroy.Call(sa)
			return hr
		}
		copy(unsafe.Slice((*float64)(data), 4), []float64{100 + float64(r.start*10), 200, float64((r.end - r.start) * 10), 20})
		pSafeArrayUnaccessData.Call(sa)
		*out = sa
		return 0
	}),
	12: syscall.NewCallback(func(r *testGeometryRange, count, out uintptr) uintptr {
		r.provider.forbiddenCalls++
		return 0x80004001
	}),
	14: syscall.NewCallback(func(r *testGeometryRange, endpoint, unit, count uintptr, moved *int32) uintptr {
		if unit != 0 || (endpoint != 0 && endpoint != 1) {
			r.provider.forbiddenCalls++
			return 0x80004001
		}
		target := &r.start
		if endpoint == 1 {
			target = &r.end
		}
		old := *target
		*target += int(int32(count))
		if *target < 0 {
			*target = 0
		}
		if *target > r.provider.length {
			*target = r.provider.length
		}
		*moved = int32(*target - old)
		return 0
	}),
	17: syscall.NewCallback(func(r *testGeometryRange) uintptr {
		r.provider.forbiddenCalls++
		return 0x80004001
	}),
}

func TestAdjacentCaretPositionCancellation(t *testing.T) {
	for _, before := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		p := &testGeometryProvider{length: 3, noPrevious: true, cancel: cancel}
		caret := &testGeometryRange{comObject: comObject{VTable: &testGeometryRangeVTable}, provider: p, refs: 1}
		if before {
			cancel()
		}
		_, _, _, ok := adjacentCaretPosition(ctx, &caret.comObject)
		cancel()
		wantProbes := 1
		if before {
			wantProbes = 0
		}
		if ok || len(p.clones) != wantProbes {
			t.Fatalf("cancel before=%v: valid=%v probes=%d", before, ok, len(p.clones))
		}
		for _, clone := range p.clones {
			if clone.refs != 0 {
				t.Fatal("cancellation leaked a geometry range")
			}
		}
	}
}

func TestAdjacentCaretPosition(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		length, caret           int
		noPrevious, unavailable bool
		wantX                   int32
		wantOK                  bool
	}{
		{name: "start uses following glyph", length: 3, wantX: 100, wantOK: true},
		{name: "middle", length: 3, caret: 1, wantX: 110, wantOK: true},
		{name: "end", length: 3, caret: 3, wantX: 130, wantOK: true},
		{name: "preceding glyph unavailable", length: 3, caret: 1, noPrevious: true, wantX: 110, wantOK: true},
		{name: "empty field"},
		{name: "no visible glyphs", length: 3, caret: 1, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &testGeometryProvider{length: tc.length, caret: tc.caret, noPrevious: tc.noPrevious, unavailable: tc.unavailable}
			caret := &testGeometryRange{comObject: comObject{VTable: &testGeometryRangeVTable}, provider: p, start: tc.caret, end: tc.caret, refs: 1}
			x, y, height, ok := adjacentCaretPosition(context.Background(), &caret.comObject)
			if ok != tc.wantOK || (ok && (x != tc.wantX || y != 220 || height != 20)) {
				t.Fatalf("position=%d/%d/%d valid=%v", x, y, height, ok)
			}
			if p.forbiddenCalls != 0 || caret.start != tc.caret || caret.end != tc.caret || caret.refs != 1 || len(p.clones) > 2 {
				t.Fatal("positioning read text, changed the original caret, or exceeded two bounded probes")
			}
			for _, clone := range p.clones {
				if clone.refs != 0 {
					t.Fatal("temporary geometry range leaked")
				}
			}
		})
	}
}
