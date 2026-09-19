//go:build windows && amd64

package win

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"typenext/internal/core"
	"unsafe"
)

// COM vtable slots follow Microsoft's UIAutomationClient.h; see docs/SOURCES.md.
// Only the focused element is inspected. No parent/document tree is traversed.
type comObject struct{ VTable *[96]uintptr }

//go:uintptrescapes
func comCall(o *comObject, slot int, args ...uintptr) uintptr {
	if o == nil {
		return 0x80004003
	}
	argv := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	hr, _, _ := syscall.SyscallN(o.VTable[slot], argv...)
	runtime.KeepAlive(o)
	return hr
}
func failed(hr uintptr) bool { return int32(hr) < 0 }
func release(o *comObject) {
	if o != nil {
		comCall(o, 2)
	}
}
func scalar(o *comObject, slot int) (int32, error) {
	var v int32
	hr := comCall(o, slot, uintptr(unsafe.Pointer(&v)))
	if failed(hr) {
		return 0, fmt.Errorf("accessibility property unavailable (0x%08x)", uint32(hr))
	}
	return v, nil
}
func pattern(o *comObject, id int, iid guid) (*comObject, error) {
	var out *comObject
	// GetCurrentPatternAs returns the requested interface, not merely IUnknown.
	hr := comCall(o, 14, uintptr(id), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	if failed(hr) || out == nil {
		return nil, errors.New("this textbox does not expose the required accessibility text pattern")
	}
	return out, nil
}

var (
	clsidUIA = guid{0xff48dba4, 0x60ef, 0x4201, [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidUIA   = guid{0x30cbe57d, 0xd9d0, 0x452a, [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
	iidText  = guid{0x32eba289, 0x3583, 0x42c9, [8]byte{0x9c, 0x59, 0x3b, 0x6d, 0x9a, 0x1e, 0x9b, 0x6a}}
	iidText2 = guid{0x506a921a, 0xfcc9, 0x409f, [8]byte{0xb2, 0x3b, 0x37, 0xeb, 0x74, 0x10, 0x68, 0x72}}
)

type variant struct {
	VT, R1, R2, R3 uint16
	Data           [16]byte
}

func rangeReadonly(r *comObject) bool {
	var v variant
	hr := comCall(r, 9, 40015, uintptr(unsafe.Pointer(&v))) // UIA_IsReadOnlyAttributeId
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	return !failed(hr) && v.VT == 11 && (*(*int16)(unsafe.Pointer(&v.Data[0]))) != 0
}
func bstrString(b *uint16) string {
	if b == nil {
		return ""
	}
	defer pSysFreeString.Call(uintptr(unsafe.Pointer(b)))
	n, _, _ := pSysStringLen.Call(uintptr(unsafe.Pointer(b)))
	if n > 32768 {
		n = 32768
	}
	return syscall.UTF16ToString(unsafe.Slice(b, int(n)))
}
func rangeText(r *comObject, max int) (string, error) {
	var b *uint16
	hr := comCall(r, 12, uintptr(max), uintptr(unsafe.Pointer(&b)))
	if failed(hr) {
		return "", errors.New("textbox text could not be read")
	}
	return bstrString(b), nil
}
func cloneRange(r *comObject) (*comObject, error) {
	var c *comObject
	if failed(comCall(r, 3, uintptr(unsafe.Pointer(&c)))) || c == nil {
		return nil, errors.New("could not clone the caret range")
	}
	return c, nil
}
func moveEnd(r *comObject, end int, count int) error {
	var moved int32
	if failed(comCall(r, 14, uintptr(end), 0, uintptr(int64(count)), uintptr(unsafe.Pointer(&moved)))) {
		return errors.New("textbox does not support reading around the caret")
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
	if failed(comCall(el, 4, uintptr(unsafe.Pointer(&sa)))) || sa == 0 {
		return "", errors.New("textbox has no stable accessibility identity")
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

type uiaWorker struct {
	jobs           chan func(*comObject)
	initialization error
	caret          caretIdentity // accessed only on the dedicated COM thread
}

func newUIA() (*uiaWorker, error) {
	w := &uiaWorker{jobs: make(chan func(*comObject), 1)}
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
		var automation *comObject
		hr, _, _ = pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidUIA)), 0, 1, uintptr(unsafe.Pointer(&iidUIA)), uintptr(unsafe.Pointer(&automation)))
		if failed(hr) || automation == nil {
			ready <- errors.New("Windows UI Automation is unavailable")
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

func (w *uiaWorker) Capture(ctx context.Context, c core.Config, padWindow uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, padWindow, false)
}

// A new request must not inherit a stale provider range from a previous one.
// Verification and insertion retain the baseline established by this capture.
func (w *uiaWorker) CaptureInitial(ctx context.Context, c core.Config, padWindow uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, padWindow, true)
}

func (w *uiaWorker) capture(ctx context.Context, c core.Config, padWindow uintptr, initial bool) (core.TextContext, error) {
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
		t, e := readContext(a, c, padWindow, &w.caret)
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

func readContext(a *comObject, c core.Config, padWindow uintptr, identity *caretIdentity) (core.TextContext, error) {
	var result core.TextContext
	window := foreground()
	_, process, err := processOf(window)
	if err != nil {
		return result, err
	}
	if window != padWindow && !c.Allows(process) {
		return result, fmt.Errorf("%s is not approved; add its executable name in Settings before reading it", process)
	}
	if composing(window) {
		return result, errors.New("finish the current IME composition first")
	}
	// Slide text is exposed by PowerPoint's native object model, rather than a standard UIA Edit.
	// The adapter remains behind the same executable approval.
	if strings.EqualFold(process, "powerpnt.exe") {
		return readPowerPointContext(c, window, process)
	}
	var el *comObject
	if failed(comCall(a, 8, uintptr(unsafe.Pointer(&el)))) || el == nil {
		return result, errors.New("no accessible focused textbox")
	}
	defer release(el)
	// Do not ask for Name/Value before checking password protection.
	password, err := scalar(el, 35)
	if err != nil || password != 0 {
		return result, errors.New("password/protected field: text access disabled")
	}
	focused, err := scalar(el, 26)
	if err != nil || focused == 0 {
		return result, errors.New("textbox does not have keyboard focus")
	}
	enabled, err := scalar(el, 28)
	if err != nil || enabled == 0 {
		return result, errors.New("textbox is not enabled")
	}
	control, err := scalar(el, 21)
	if err != nil || (control != 50004 && control != 50030) {
		return result, errors.New("focused control is not an accessible Edit or Document; no text was read")
	}
	pid, err := scalar(el, 20)
	if err != nil {
		return result, err
	}
	// Some browser accessibility nodes belong to a renderer process.
	// The visible foreground executable is the allowlist boundary; focus is rechecked below.
	_ = pid
	id, err := focusID(el)
	if err != nil {
		return result, err
	}
	pat, err := pattern(el, 10014, iidText)
	if err != nil {
		return result, err
	}
	defer release(pat)
	var selections *comObject
	if failed(comCall(pat, 5, uintptr(unsafe.Pointer(&selections)))) || selections == nil {
		return result, errors.New("textbox does not expose a reliable selection/caret; no end-of-text guess is made")
	}
	defer release(selections)
	count, err := scalar(selections, 3)
	if err != nil || count != 1 {
		return result, errors.New("place one caret in the textbox without selecting text")
	}
	var caret *comObject
	if failed(comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret)))) || caret == nil {
		return result, errors.New("caret range unavailable")
	}
	defer func() { release(caret) }()
	var compare int32
	if failed(comCall(caret, 5, 0, uintptr(unsafe.Pointer(caret)), 1, uintptr(unsafe.Pointer(&compare)))) || compare != 0 {
		return result, errors.New("selected text is not replaced; clear the selection first")
	}
	source := "TextPattern selection"
	// Prefer TextPattern2's active caret when available,
	// but still require a collapsed selection above so accepting can never replace selected text.
	if p2, e := pattern(el, 10024, iidText2); e == nil {
		var active int32
		var r2 *comObject
		hr := comCall(p2, 10, uintptr(unsafe.Pointer(&active)), uintptr(unsafe.Pointer(&r2)))
		if !failed(hr) && active != 0 && r2 != nil {
			// Both patterns must describe the same collapsed selection.
			// A stale provider caret must not move our read to another insertion point.
			if sameRangeEndpoints(caret, r2) {
				release(caret)
				caret = r2
				source = "TextPattern2 caret"
			} else {
				release(r2)
			}
		} else {
			release(r2)
		}
		release(p2)
	}
	if rangeReadonly(caret) {
		return result, errors.New("read-only text: completion disabled")
	}
	before, err := cloneRange(caret)
	if err != nil {
		return result, err
	}
	defer release(before)
	after, err := cloneRange(caret)
	if err != nil {
		return result, err
	}
	defer release(after)
	if err = moveEnd(before, 0, -c.PrefixChars); err != nil {
		return result, err
	}
	if err = moveEnd(after, 1, c.SuffixChars); err != nil {
		return result, err
	}
	prefix, err := rangeText(before, c.PrefixChars*2+4)
	if err != nil {
		return result, err
	}
	suffix, err := rangeText(after, c.SuffixChars*2+4)
	if err != nil {
		return result, err
	}
	x, y, h, ok := nativeCaret(window)
	if !ok {
		if r, yes := rangeRect(caret); yes {
			x, y, h, ok = r.Left, r.Bottom, r.Bottom-r.Top, true
		}
	}
	if !ok {
		// A zero-length UIA range often has no rectangle; try the preceding glyph.
		if r, e := cloneRange(caret); e == nil {
			if moveEnd(r, 0, -1) == nil {
				if box, yes := rangeRect(r); yes {
					x, y, h, ok = box.Right, box.Bottom, box.Bottom-box.Top, true
				}
			}
			release(r)
		}
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
	// Browser providers may replace their range objects when unrelated page content refreshes.
	// Prefer an exact offset derived entirely from this read;
	// keep retained-range comparison for providers or long documents that cannot establish a bounded offset.
	caretID, err := identity.identifyInPattern(window, id, pat, caret)
	if err != nil {
		return result, err
	}
	if foreground() != window {
		return result, errors.New("focus changed while reading; try again")
	}
	var current *comObject
	if failed(comCall(a, 8, uintptr(unsafe.Pointer(&current)))) || current == nil {
		return result, errors.New("focus disappeared")
	}
	currentID, e := focusID(current)
	release(current)
	if e != nil || currentID != id {
		return result, errors.New("textbox changed while reading; try again")
	}
	result = core.TextContext{Window: uint64(window), FocusID: id, CaretID: caretID, Process: process, Prefix: core.Tail(prefix, c.PrefixChars), Suffix: core.Head(suffix, c.SuffixChars), X: x, Y: y, CaretHeight: h, PositionSource: source}
	return result, nil
}

func (w *uiaWorker) Insert(ctx context.Context, c core.Config, padWindow uintptr, expected core.TextContext, text string, revision *atomic.Uint64, expectedRevision uint64, acceptVK uint32) error {
	if !waitRelease(1200*time.Millisecond, uintptr(acceptVK)) {
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
		actual, err := readContext(a, c, padWindow, &w.caret)
		if err != nil {
			reply <- err
			return
		}
		if actual.Fingerprint() != expected.Fingerprint() {
			reply <- errors.New("textbox/caret changed; request a new suggestion")
			return
		}
		if composing(uintptr(expected.Window)) {
			reply <- errors.New("finish the IME composition before accepting")
			return
		}
		if ctx.Err() != nil || revision.Load() != expectedRevision || foreground() != uintptr(expected.Window) || modifiersDown() {
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
		reply <- sendUnicode(safe)
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
