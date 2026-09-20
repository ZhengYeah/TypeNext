//go:build windows && amd64

package uia

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	padRichEdit       = syscall.NewLazyDLL("msftedit.dll")
	pGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pSendMessage      = user32.NewProc("SendMessageW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
)

type point struct{ X, Y int32 }

type msg struct {
	Window         uintptr
	Message        uint32
	_              uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// Own the native fixture here so UIA tests do not depend on the application UI.
func createTestPadEdit(parent, instance uintptr) (uintptr, error) {
	if err := padRichEdit.Load(); err != nil {
		return 0, fmt.Errorf("load test editor: %w", err)
	}
	edit, _, err := pCreateWindowEx.Call(0x200, uintptr(unsafe.Pointer(u16("RICHEDIT50W"))), 0, 0x503110c4, 12, 12, 350, 150, parent, 600, instance, 0)
	if edit == 0 {
		return 0, fmt.Errorf("create test editor: %w", err)
	}
	// EM_SETTEXTMODE requires an empty control; TM_PLAINTEXT keeps offsets stable.
	if result, _, _ := pSendMessage.Call(edit, 0x400+89, 1, 0); result != 0 {
		pDestroyWindow.Call(edit)
		return 0, fmt.Errorf("configure plain-text test editor: 0x%x", result)
	}
	return edit, nil
}
