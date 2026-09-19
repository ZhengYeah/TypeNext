//go:build windows && amd64

package win

import (
	"fmt"
	"syscall"
	"unsafe"
)

var padRichEdit = syscall.NewLazyDLL("msftedit.dll")

func createPadEdit(parent, instance uintptr, width, height int, text string) (uintptr, error) {
	if err := padRichEdit.Load(); err != nil {
		return 0, fmt.Errorf("load test pad editor: %w", err)
	}
	edit, _, err := pCreateWindowEx.Call(0x200, uintptr(unsafe.Pointer(u16("RICHEDIT50W"))), 0, 0x503110c4, 12, 12, uintptr(width), uintptr(height), parent, 600, instance, 0)
	if edit == 0 {
		return 0, fmt.Errorf("create test pad editor: %w", err)
	}
	// EM_SETTEXTMODE requires an empty control.
	// Keep typing and pasted content plain text, including when the clipboard contains rich text or objects.
	if result, _, _ := pSendMessage.Call(edit, 0x400+89, 1, 0); result != 0 { // TM_PLAINTEXT
		pDestroyWindow.Call(edit)
		return 0, fmt.Errorf("configure plain-text test pad: 0x%x", result)
	}
	if ok, _, err := pSetWindowText.Call(edit, uintptr(unsafe.Pointer(u16(text)))); ok == 0 {
		pDestroyWindow.Call(edit)
		return 0, fmt.Errorf("set test pad text: %w", err)
	}
	return edit, nil
}
