//go:build windows && amd64

package win

import (
	"fmt"
	"unsafe"
)

const (
	settingsClientWidth = 752
	statusBackground    = 0xECE9E6 // RGB #E6E9EC
	statusForeground    = 0x62554C // RGB #4C5562
	statusSurfaceTag    = 0x544E5354
	windowUserData      = ^uintptr(20) // GWLP_USERDATA (-21)
)

var (
	pAdjustWindowRectEx = user32.NewProc("AdjustWindowRectEx")
	pSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtr   = user32.NewProc("GetWindowLongPtrW")
)

// All framed windows share a client width. Windows supplies the caption and border sizes.
func (a *app) framedWindowBounds(style, extended uintptr, clientHeight int) (rect, error) {
	bounds := rect{Right: int32(a.s(settingsClientWidth)), Bottom: int32(a.s(clientHeight))}
	if ok, _, err := pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&bounds)), style, 0, extended); ok == 0 {
		return rect{}, fmt.Errorf("size window: %v", err)
	}
	return bounds, nil
}

func (a *app) createSettingsWindow(title string, x, y, clientHeight int, owner uintptr) (uintptr, error) {
	const style, extended = 0x00CA0000, 0x00010000
	bounds, err := a.framedWindowBounds(style, extended, clientHeight)
	if err != nil {
		return 0, err
	}
	w, _, err := pCreateWindowEx.Call(extended, uintptr(unsafe.Pointer(u16("TypeNextWindow"))), uintptr(unsafe.Pointer(u16(title))), style,
		uintptr(x), uintptr(y), uintptr(bounds.Right-bounds.Left), uintptr(bounds.Bottom-bounds.Top), owner, 0, a.instance, 0)
	if w == 0 {
		return 0, fmt.Errorf("create settings window: %v", err)
	}
	return w, nil
}

func (a *app) separatorIn(parent uintptr, x, y, w int) {
	a.controlIn(parent, "STATIC", "", 0, x, y, w, 2, 0x10)
}

func (a *app) sectionLabel(parent uintptr, text string, x, y, w, h int) {
	label := a.controlIn(parent, "STATIC", text, 0, x, y, w, h, 0)
	pSendMessage.Call(label, 0x30, a.boldFont, 1)
}

// Keep native accessible static text while giving each status surface padding.
// The marker belongs to the HWND, so destroying a dialog cannot leave
// stale handle entries that accidentally color controls in a later dialog.
func (a *app) statusText(parent uintptr, text string, id, x, y, w, h int) uintptr {
	// Padding strips do not overlap the text HWND.
	// Overlapping static siblings can repaint over one another during native window redraw/capture.
	for _, edge := range [][4]int{{x, y, w, 6}, {x, y + h - 6, w, 6}, {x, y + 6, 8, h - 12}, {x + w - 8, y + 6, 8, h - 12}} {
		panel := a.controlIn(parent, "STATIC", "", 0, edge[0], edge[1], edge[2], edge[3], 0)
		pSetWindowLongPtr.Call(panel, windowUserData, statusSurfaceTag)
	}
	label := a.controlIn(parent, "STATIC", text, id, x+8, y+6, w-16, h-12, 0x80) // SS_NOPREFIX
	pSetWindowLongPtr.Call(label, windowUserData, statusSurfaceTag)
	pSendMessage.Call(label, 0x30, a.smallFont, 1)
	return label
}
