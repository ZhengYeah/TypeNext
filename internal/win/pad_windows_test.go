//go:build windows && amd64

package win

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func TestCreatePadEdit(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	inst, _, _ := pGetModuleHandle.Call(0)
	parent, _, err := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("STATIC"))), 0, 0x00cf0000, 0, 0, 400, 200, 0, 0, inst, 0)
	if parent == 0 {
		t.Fatalf("create hidden test parent: %v", err)
	}
	defer pDestroyWindow.Call(parent)
	const sample = "TypeNext sample: hello 世界"
	edit, err := createPadEdit(parent, inst, 350, 150, sample)
	if err != nil {
		t.Fatal(err)
	}
	mode, _, _ := pSendMessage.Call(edit, 0x45a, 0, 0) // EM_GETTEXTMODE
	if mode&1 == 0 {                                   // TM_PLAINTEXT
		t.Fatalf("test pad is not in plain-text mode: %d", mode)
	}
	var buffer [128]uint16
	n, _, err := pGetWindowText.Call(edit, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 {
		t.Fatalf("read initial test pad text: %v", err)
	}
	if got := syscall.UTF16ToString(buffer[:]); got != sample {
		t.Errorf("initial text = %q, want %q", got, sample)
	}
}
