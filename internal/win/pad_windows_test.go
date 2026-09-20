//go:build windows && amd64

package win

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// Exercise the real Windows provider using only hidden, owned sample controls.
// A separate pumping UI thread is required for calls from the COM worker.
func TestPadAccessibilityText(t *testing.T) {
	edit, update := testPadControl(t)
	w, err := newUIA()
	if err != nil {
		t.Fatal(err)
	}
	defer close(w.jobs)
	tests := []struct {
		name, text     string
		start, end     uintptr
		prefix, suffix string
		selected       bool
	}{
		{"middle", "before caret after", 12, 12, "caret", " afte", false},
		{"end", "before caret after", 18, 18, "after", "", false},
		{"empty", "", 0, 0, "", "", false},
		{"unicode", "😀abc🦊終", 5, 5, "😀abc", "🦊終", false},
		{"multiline", "first\r\nsecond", 6, 6, "irst\r", "secon", false},
		{"selection", "before caret after", 7, 12, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update(tt.text, tt.start, tt.end)
			type result struct {
				prefix, suffix string
				selected       bool
				err            error
			}
			reply := make(chan result, 1)
			w.jobs <- func(a *comObject) {
				prefix, suffix, selected, err := readTestPadRanges(a, edit)
				reply <- result{prefix, suffix, selected, err}
			}
			select {
			case got := <-reply:
				if got.err != nil {
					t.Fatal(got.err)
				}
				if got.prefix != tt.prefix || got.suffix != tt.suffix || got.selected != tt.selected {
					t.Errorf("got prefix=%q suffix=%q selected=%v; want prefix=%q suffix=%q selected=%v", got.prefix, got.suffix, got.selected, tt.prefix, tt.suffix, tt.selected)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("test pad accessibility provider timed out")
			}
		})
	}
}

func testPadControl(t *testing.T) (uintptr, func(string, uintptr, uintptr)) {
	t.Helper()
	type initialized struct {
		handle uintptr
		err    error
	}
	ready := make(chan initialized, 1)
	jobs := make(chan func(uintptr))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		inst, _, _ := pGetModuleHandle.Call(0)
		parent, _, err := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("STATIC"))), 0, 0x00cf0000, 0, 0, 400, 200, 0, 0, inst, 0)
		if parent == 0 {
			ready <- initialized{err: fmt.Errorf("create hidden test parent: %w", err)}
			return
		}
		defer pDestroyWindow.Call(parent)
		edit, err := createPadEdit(parent, inst, 350, 150, "")
		if err == nil {
			mode, _, _ := pSendMessage.Call(edit, 0x45a, 0, 0) // EM_GETTEXTMODE
			if mode&1 == 0 {
				err = fmt.Errorf("test pad is not in plain-text mode: %d", mode)
			}
		}
		ready <- initialized{edit, err}
		if err != nil {
			return
		}
		peek := user32.NewProc("PeekMessageW")
		for {
			select {
			case <-stop:
				return
			case f := <-jobs:
				f(edit)
			default:
			}
			var m msg
			for {
				n, _, _ := peek.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if n == 0 {
					break
				}
				pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("test pad UI thread did not stop")
		}
	})
	var init initialized
	select {
	case init = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("test pad UI thread did not initialize")
	}
	if init.err != nil {
		t.Fatal(init.err)
	}
	return init.handle, func(text string, start, end uintptr) {
		updated := make(chan struct{})
		jobs <- func(edit uintptr) {
			pSetWindowText.Call(edit, uintptr(unsafe.Pointer(u16(text))))
			pSendMessage.Call(edit, 0xb1, start, end) // EM_SETSEL uses UTF-16 offsets.
			close(updated)
		}
		<-updated
	}
}

func readTestPadRanges(a *comObject, handle uintptr) (prefix, suffix string, selected bool, err error) {
	var el *comObject
	hr := comCall(a, 6, handle, uintptr(unsafe.Pointer(&el))) // ElementFromHandle
	if failed(hr) || el == nil {
		return "", "", false, fmt.Errorf("test pad ElementFromHandle: 0x%08x", uint32(hr))
	}
	defer release(el)
	pat, err := pattern(el, 10014, iidText)
	if err != nil {
		return "", "", false, err
	}
	defer release(pat)
	var selections *comObject
	if hr := comCall(pat, 5, uintptr(unsafe.Pointer(&selections))); failed(hr) || selections == nil {
		return "", "", false, fmt.Errorf("test pad GetSelection: 0x%08x", uint32(hr))
	}
	defer release(selections)
	count, err := scalar(selections, 3)
	if err != nil || count != 1 {
		return "", "", false, fmt.Errorf("test pad selection count=%d: %v", count, err)
	}
	var caret *comObject
	if hr := comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret))); failed(hr) || caret == nil {
		return "", "", false, fmt.Errorf("test pad selection range: 0x%08x", uint32(hr))
	}
	defer release(caret)
	var compare int32
	if hr := comCall(caret, 5, 0, uintptr(unsafe.Pointer(caret)), 1, uintptr(unsafe.Pointer(&compare))); failed(hr) {
		return "", "", false, fmt.Errorf("test pad CompareEndpoints: 0x%08x", uint32(hr))
	}
	if compare != 0 {
		return "", "", true, nil
	}
	prefix, err = readCaretSide(context.Background(), caret, 0, 5)
	if err == nil {
		suffix, err = readCaretSide(context.Background(), caret, 1, 5)
	}
	return prefix, suffix, false, err
}
