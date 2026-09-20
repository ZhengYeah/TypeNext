//go:build windows && amd64

package uia

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"typenext/internal/core"
	"unsafe"
)

// COM vtable slots follow Microsoft's UIAutomationClient.h; see docs/SOURCES.md.
// TextChild resolution uses an explicit provider relationship, not a tree search.
func scalar(o *comObject, slot int) (int32, error) {
	var v int32
	hr := comCall(o, slot, uintptr(unsafe.Pointer(&v)))
	if failed(hr) {
		return 0, fmt.Errorf("accessibility property unavailable (0x%08x)", uint32(hr))
	}
	return v, nil
}

type uiaPatternError struct {
	id int
	hr uintptr
}

func (e *uiaPatternError) Error() string {
	return fmt.Sprintf("GetCurrentPatternAs(%d) unavailable (0x%08x)", e.id, uint32(e.hr))
}

func patternUnavailable(err error) bool {
	var e *uiaPatternError
	if !errors.As(err, &e) {
		return false
	}
	return e.hr == 0 || uint32(e.hr) == 0x80004002 || uint32(e.hr) == 0x80040204 // E_NOINTERFACE, UIA_E_NOTSUPPORTED
}

func pattern(o *comObject, id int, iid guid) (*comObject, error) {
	var out *comObject
	// GetCurrentPatternAs returns the requested interface, not merely IUnknown.
	hr := comCall(o, 14, uintptr(id), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	if failed(hr) || out == nil {
		release(out)
		return nil, &uiaPatternError{id: id, hr: hr}
	}
	return out, nil
}

var (
	clsidUIA = guid{A: 0xff48dba4, B: 0x60ef, C: 0x4201, D: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidUIA   = guid{A: 0x30cbe57d, B: 0xd9d0, C: 0x452a, D: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
	iidText  = guid{A: 0x32eba289, B: 0x3583, C: 0x42c9, D: [8]byte{0x9c, 0x59, 0x3b, 0x6d, 0x9a, 0x1e, 0x9b, 0x6a}}
	iidText2 = guid{A: 0x506a921a, B: 0xfcc9, C: 0x409f, D: [8]byte{0xb2, 0x3b, 0x37, 0xeb, 0x74, 0x10, 0x68, 0x72}}
)

func cloneRange(r *comObject) (*comObject, error) {
	var c *comObject
	hr := comCall(r, 3, uintptr(unsafe.Pointer(&c)))
	if failed(hr) || c == nil {
		release(c)
		return nil, fmt.Errorf("TextRange.Clone unavailable (0x%08x)", uint32(hr))
	}
	return c, nil
}
func moveEnd(r *comObject, end int, count int) error {
	var moved int32
	hr := comCall(r, 14, uintptr(end), 0, uintptr(int64(count)), uintptr(unsafe.Pointer(&moved)))
	if failed(hr) {
		return fmt.Errorf("TextRange.MoveEndpointByUnit failed (0x%08x)", uint32(hr))
	}
	return nil
}
func safeArrayData(sa uintptr) (unsafe.Pointer, int, func(), error) {
	cleanup := func() { pSafeArrayDestroy.Call(sa) }
	var lo, hi int32
	a, _, _ := pSafeArrayGetLBound.Call(sa, 1, uintptr(unsafe.Pointer(&lo)))
	b, _, _ := pSafeArrayGetUBound.Call(sa, 1, uintptr(unsafe.Pointer(&hi)))
	if failed(a) || failed(b) || hi < lo || hi-lo > 65536 {
		cleanup()
		return nil, 0, func() {}, errors.New("invalid accessibility array")
	}
	var data unsafe.Pointer
	a, _, _ = pSafeArrayAccessData.Call(sa, uintptr(unsafe.Pointer(&data)))
	if failed(a) {
		cleanup()
		return nil, 0, func() {}, errors.New("inaccessible array")
	}
	return data, int(hi - lo + 1), func() { pSafeArrayUnaccessData.Call(sa); pSafeArrayDestroy.Call(sa) }, nil
}
func focusID(el *comObject) (string, error) {
	var sa uintptr
	hr := comCall(el, 4, uintptr(unsafe.Pointer(&sa)))
	if failed(hr) || sa == 0 {
		if sa != 0 {
			pSafeArrayDestroy.Call(sa)
		}
		return "", fmt.Errorf("GetRuntimeId unavailable (0x%08x)", uint32(hr))
	}
	p, n, done, e := safeArrayData(sa)
	if e != nil {
		return "", e
	}
	defer done()
	ids := unsafe.Slice((*int32)(p), n)
	return fmt.Sprint(ids), nil
}
func rangeRect(r *comObject) (rect, bool) {
	var sa uintptr
	if failed(comCall(r, 10, uintptr(unsafe.Pointer(&sa)))) || sa == 0 {
		return rect{}, false
	}
	p, n, done, e := safeArrayData(sa)
	if e != nil {
		return rect{}, false
	}
	defer done()
	if n < 4 || p == nil {
		return rect{}, false
	}
	v := unsafe.Slice((*float64)(p), n)
	if v[3] <= 0 || v[3] > 10000 {
		return rect{}, false
	}
	return rect{int32(v[0]), int32(v[1]), int32(v[0] + v[2]), int32(v[1] + v[3])}, true
}

// Worker serializes accessibility operations on a dedicated COM thread.
type Worker struct {
	jobs  chan func(*comObject)
	caret caretIdentity // accessed only on the dedicated COM thread
	host  Host
}

func newUIA(host Host) (*Worker, error) {
	w := &Worker{jobs: make(chan func(*comObject), 1), host: host}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := pCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED, off the UI thread.
		if failed(hr) {
			ready <- errors.New("cannot initialize Windows accessibility")
			return
		}
		defer pCoUninitialize.Call()
		automation, err := createUIAutomation(createAutomationClass)
		if err != nil {
			ready <- err
			return
		}
		defer release(automation)
		defer w.caret.close()
		ready <- nil
		for f := range w.jobs {
			f(automation)
		}
	}()
	if e := <-ready; e != nil {
		return nil, e
	}
	return w, nil
}

func (w *Worker) Capture(ctx context.Context, c core.Config, padWindow uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, padWindow, false)
}

// A new request must not inherit a stale provider range from a previous one.
// Verification and insertion retain the baseline established by this capture.
func (w *Worker) CaptureInitial(ctx context.Context, c core.Config, padWindow uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, padWindow, true)
}

func (w *Worker) capture(ctx context.Context, c core.Config, padWindow uintptr, initial bool) (core.TextContext, error) {
	type result struct {
		t core.TextContext
		e error
	}
	reply := make(chan result, 1)
	job := func(a *comObject) {
		if ctx.Err() != nil {
			reply <- result{e: ctx.Err()}
			return
		}
		if initial {
			w.caret.close()
		}
		t, e := w.readContext(ctx, a, c, padWindow, &w.caret, true)
		reply <- result{t, e}
	}
	select {
	case w.jobs <- job:
	case <-ctx.Done():
		return core.TextContext{}, ctx.Err()
	}
	select {
	case r := <-reply:
		return r.t, r.e
	case <-ctx.Done():
		return core.TextContext{}, errors.New("the app's accessibility provider did not respond; try another app or restart TypeNext")
	}
}

func (w *Worker) readContext(ctx context.Context, a *comObject, c core.Config, padWindow uintptr, identity *caretIdentity, includePosition bool) (core.TextContext, error) {
	var result core.TextContext
	if err := ctx.Err(); err != nil {
		return result, err
	}
	window := w.host.Foreground()
	_, process, err := w.host.ProcessOf(window)
	if err != nil {
		return result, err
	}
	if window != padWindow && !c.Allows(process) {
		return result, fmt.Errorf("%s is not approved; add its executable name in Settings before reading it", process)
	}
	if w.host.Composing(window) {
		return result, errors.New("finish the current IME composition first")
	}
	// Slide text is exposed by PowerPoint's native object model, rather than a standard UIA Edit.
	// The adapter remains behind the same executable approval.
	if strings.EqualFold(process, "powerpnt.exe") {
		return w.host.ReadPowerPoint(c, window, process)
	}
	var el *comObject
	if failed(comCall(a, 8, uintptr(unsafe.Pointer(&el)))) || el == nil {
		release(el)
		return result, errors.New("no accessible focused textbox")
	}
	defer release(el)
	target, err := resolveTextTarget(ctx, a, el)
	if err != nil {
		return result, err
	}
	defer target.close()
	pat, id := target.pattern, target.identity()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	caret, err := collapsedSelection(pat)
	if err != nil {
		return result, err
	}
	defer func() { release(caret) }()
	if err = rangeWithinBoundary(caret, target.boundary); err != nil {
		return result, err
	}
	source := target.via + " selection"
	// Prefer TextPattern2's active caret when available,
	// but still require a collapsed selection above so accepting can never replace selected text.
	if p2, e := pattern(target.owner, 10024, iidText2); e == nil {
		var active int32
		var r2 *comObject
		hr := comCall(p2, 10, uintptr(unsafe.Pointer(&active)), uintptr(unsafe.Pointer(&r2)))
		if !failed(hr) && active != 0 && r2 != nil {
			// Both patterns must describe the same collapsed selection.
			// A stale provider caret must not move our read to another insertion point.
			if sameRangeEndpoints(caret, r2) {
				release(caret)
				caret = r2
				source = target.via + " / TextPattern2 caret"
			} else {
				release(r2)
			}
		} else {
			release(r2)
		}
		release(p2)
	}
	if _, err = caretEditability(a, el, caret); err != nil {
		return result, err
	}
	prefix, err := readCaretSideWithin(ctx, caret, target.boundary, 0, c.PrefixChars)
	if err != nil {
		return result, err
	}
	suffix, err := readCaretSideWithin(ctx, caret, target.boundary, 1, c.SuffixChars)
	if err != nil {
		return result, err
	}
	var x, y, h int32
	if includePosition {
		var ok bool
		x, y, h, ok = w.host.NativeCaret(window)
		if !ok {
			if r, yes := rangeRect(caret); yes {
				x, y, h, ok = r.Left, r.Bottom, r.Bottom-r.Top, true
			}
		}
		if !ok {
			x, y, h, ok = adjacentCaretPositionWithin(ctx, caret, target.boundary)
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if !ok {
			var box rect
			if !failed(comCall(el, 43, uintptr(unsafe.Pointer(&box)))) && box.Right > box.Left {
				x, y, h = box.Left+12, box.Top+28, 20
				source += " (field-corner positioning)"
			} else {
				return result, errors.New("textbox location unavailable")
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if w.host.Foreground() != window {
		return result, errors.New("focus changed while reading; try again")
	}
	var current *comObject
	if failed(comCall(a, 8, uintptr(unsafe.Pointer(&current)))) || current == nil {
		release(current)
		return result, errors.New("focus disappeared")
	}
	defer release(current)
	// Re-resolve the same focused field and provider, including its boundary.
	if err = verifyTextTarget(ctx, a, current, target, caret); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// Browser providers may replace their range objects when unrelated page content refreshes.
	// Prefer an exact offset derived entirely from this read; retain a range only when needed.
	// Update identity only after selection validation.
	var caretID string
	if target.boundary == nil {
		caretID, err = identity.identifyInPattern(window, id, pat, caret)
	} else {
		// Retain a scoped caret instead of deriving an offset from the parent page.
		caretID, err = identity.identify(window, id, caret)
	}
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if w.host.Foreground() != window {
		return result, errors.New("focus changed while reading; try again")
	}
	result = core.TextContext{Window: uint64(window), FocusID: id, CaretID: caretID, Process: process, Prefix: prefix, Suffix: suffix, X: x, Y: y, CaretHeight: h, PositionSource: source}
	return result, nil
}

func (w *Worker) Insert(ctx context.Context, c core.Config, padWindow uintptr, expected core.TextContext, text string, revision *atomic.Uint64, expectedRevision uint64, acceptVK uint32) error {
	if !w.host.WaitRelease(1200*time.Millisecond, uintptr(acceptVK)) {
		return errors.New("release the shortcut keys and try accepting again")
	}
	reply := make(chan error, 1)
	job := func(a *comObject) {
		if ctx.Err() != nil {
			reply <- ctx.Err()
			return
		}
		if revision.Load() != expectedRevision {
			reply <- errors.New("typing or focus changed; suggestion discarded")
			return
		}
		// UIA insertion checks use logical caret identity; popup geometry is unused.
		actual, err := w.readContext(ctx, a, c, padWindow, &w.caret, false)
		if err != nil {
			reply <- err
			return
		}
		if actual.Fingerprint() != expected.Fingerprint() {
			reply <- errors.New("textbox/caret changed; request a new suggestion")
			return
		}
		if w.host.Composing(uintptr(expected.Window)) {
			reply <- errors.New("finish the IME composition before accepting")
			return
		}
		if ctx.Err() != nil || revision.Load() != expectedRevision || w.host.Foreground() != uintptr(expected.Window) || w.host.ModifiersDown() {
			reply <- errors.New("input context changed; insertion cancelled")
			return
		}
		safe := core.CleanSuggestion(text, core.TextContext{}, c.MaxSuggestionChars)
		if strings.TrimSpace(safe) == "" {
			reply <- errors.New("empty suggestion")
			return
		}
		// Read/check/inject cannot be atomic across arbitrary third-party apps.
		// This narrows the race; a native TSF edit session is needed to eliminate it.
		reply <- w.host.SendUnicode(safe)
	}
	select {
	case w.jobs <- job:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case e := <-reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
