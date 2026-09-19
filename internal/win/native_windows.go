//go:build windows && amd64

// Package win implements the Windows UI using only operating-system APIs.
package win

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	ole32                      = syscall.NewLazyDLL("ole32.dll")
	oleaut32                   = syscall.NewLazyDLL("oleaut32.dll")
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	shell32                    = syscall.NewLazyDLL("shell32.dll")
	imm32                      = syscall.NewLazyDLL("imm32.dll")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pGetGUIThreadInfo          = user32.NewProc("GetGUIThreadInfo")
	pClientToScreen            = user32.NewProc("ClientToScreen")
	pGetAsyncKeyState          = user32.NewProc("GetAsyncKeyState")
	pSendInput                 = user32.NewProc("SendInput")
	pOpenProcess               = kernel32.NewProc("OpenProcess")
	pQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	pCloseHandle               = kernel32.NewProc("CloseHandle")
	pGetModuleHandle           = kernel32.NewProc("GetModuleHandleW")
	pCoInitializeEx            = ole32.NewProc("CoInitializeEx")
	pCoUninitialize            = ole32.NewProc("CoUninitialize")
	pCoCreateInstance          = ole32.NewProc("CoCreateInstance")
	pSafeArrayGetLBound        = oleaut32.NewProc("SafeArrayGetLBound")
	pSafeArrayGetUBound        = oleaut32.NewProc("SafeArrayGetUBound")
	pSafeArrayAccessData       = oleaut32.NewProc("SafeArrayAccessData")
	pSafeArrayUnaccessData     = oleaut32.NewProc("SafeArrayUnaccessData")
	pSafeArrayDestroy          = oleaut32.NewProc("SafeArrayDestroy")
	pSysStringLen              = oleaut32.NewProc("SysStringLen")
	pSysFreeString             = oleaut32.NewProc("SysFreeString")
	pVariantClear              = oleaut32.NewProc("VariantClear")
	pImmGetContext             = imm32.NewProc("ImmGetContext")
	pImmReleaseContext         = imm32.NewProc("ImmReleaseContext")
	pImmGetCompositionString   = imm32.NewProc("ImmGetCompositionStringW")
	pCreateWindowEx            = user32.NewProc("CreateWindowExW")
	pDefWindowProc             = user32.NewProc("DefWindowProcW")
	pRegisterClassEx           = user32.NewProc("RegisterClassExW")
	pShowWindow                = user32.NewProc("ShowWindow")
	pSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	pSetFocus                  = user32.NewProc("SetFocus")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pPostMessage               = user32.NewProc("PostMessageW")
	pSendMessage               = user32.NewProc("SendMessageW")
	pGetMessage                = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessage           = user32.NewProc("DispatchMessageW")
	pIsDialogMessage           = user32.NewProc("IsDialogMessageW")
	pRegisterHotKey            = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey          = user32.NewProc("UnregisterHotKey")
	pSetTimer                  = user32.NewProc("SetTimer")
	pKillTimer                 = user32.NewProc("KillTimer")
	pSetWindowText             = user32.NewProc("SetWindowTextW")
	pGetWindowText             = user32.NewProc("GetWindowTextW")
	pGetWindowTextLength       = user32.NewProc("GetWindowTextLengthW")
	pSetWindowsHookEx          = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx       = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx            = user32.NewProc("CallNextHookEx")
	pLoadCursor                = user32.NewProc("LoadCursorW")
	pMessageBox                = user32.NewProc("MessageBoxW")
	pGetClientRect             = user32.NewProc("GetClientRect")
	pMoveWindow                = user32.NewProc("MoveWindow")
	pSetWindowPos              = user32.NewProc("SetWindowPos")
	pGetCursorPos              = user32.NewProc("GetCursorPos")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pCreatePopupMenu           = user32.NewProc("CreatePopupMenu")
	pAppendMenu                = user32.NewProc("AppendMenuW")
	pTrackPopupMenu            = user32.NewProc("TrackPopupMenu")
	pDestroyMenu               = user32.NewProc("DestroyMenu")
	pShellNotifyIcon           = shell32.NewProc("Shell_NotifyIconW")
	pRegisterWindowMessage     = user32.NewProc("RegisterWindowMessageW")
	pBeginPaint                = user32.NewProc("BeginPaint")
	pEndPaint                  = user32.NewProc("EndPaint")
	pFillRect                  = user32.NewProc("FillRect")
	pDrawText                  = user32.NewProc("DrawTextW")
	pInvalidateRect            = user32.NewProc("InvalidateRect")
	pSetBkMode                 = gdi32.NewProc("SetBkMode")
	pSetBkColor                = gdi32.NewProc("SetBkColor")
	pSetTextColor              = gdi32.NewProc("SetTextColor")
	pCreateSolidBrush          = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject              = gdi32.NewProc("DeleteObject")
	pSelectObject              = gdi32.NewProc("SelectObject")
	pCreateFont                = gdi32.NewProc("CreateFontW")
	pMonitorFromRect           = user32.NewProc("MonitorFromRect")
	pGetMonitorInfo            = user32.NewProc("GetMonitorInfoW")
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type guiThreadInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	CaretRect                                          rect
}
type msg struct {
	Window         uintptr
	Message        uint32
	_              uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Rect               rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}
type keyboardHook struct {
	VK, Scan, Flags, Time uint32
	Extra                 uintptr
}
type input struct {
	Type        uint32
	_           uint32
	VK, Scan    uint16
	Flags, Time uint32
	_           uint32
	Extra       uintptr
	_           [8]byte // INPUT's union is 32 bytes on Windows x64.
}
type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

type notifyIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                guid
	BalloonIcon         uintptr
}

func u16(s string) *uint16    { p, _ := syscall.UTF16PtrFromString(s); return p }
func foreground() uintptr     { w, _, _ := pGetForegroundWindow.Call(); return w }
func keyDown(vk uintptr) bool { v, _, _ := pGetAsyncKeyState.Call(vk); return v&0x8000 != 0 }
func modifiersDown() bool {
	for _, k := range []uintptr{0x10, 0x11, 0x12, 0x5b, 0x5c} {
		if keyDown(k) {
			return true
		}
	}
	return false
}
func processOf(w uintptr) (uint32, string, error) {
	var pid uint32
	pGetWindowThreadProcessId.Call(w, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return 0, "", errors.New("no foreground application")
	}
	h, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return pid, "", errors.New("cannot inspect the foreground process; elevated apps are not supported")
	}
	defer pCloseHandle.Call(h)
	b := make([]uint16, 32768)
	n := uint32(len(b))
	ok, _, _ := pQueryFullProcessImageName.Call(h, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&n)))
	if ok == 0 {
		return pid, "", errors.New("cannot identify the foreground executable")
	}
	return pid, filepath.Base(syscall.UTF16ToString(b[:n])), nil
}
func guiInfo(w uintptr) (guiThreadInfo, bool) {
	thread, _, _ := pGetWindowThreadProcessId.Call(w, 0)
	g := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := pGetGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&g)))
	return g, ok != 0
}
func composing(w uintptr) bool {
	g, ok := guiInfo(w)
	if !ok || g.Focus == 0 {
		return false
	}
	imc, _, _ := pImmGetContext.Call(g.Focus)
	if imc == 0 {
		return false
	}
	defer pImmReleaseContext.Call(g.Focus, imc)
	n, _, _ := pImmGetCompositionString.Call(imc, 8, 0, 0)
	return int32(n) > 0
}
func nativeCaret(w uintptr) (int32, int32, int32, bool) {
	g, ok := guiInfo(w)
	if !ok || g.Caret == 0 {
		return 0, 0, 0, false
	}
	pos := point{g.CaretRect.Left, g.CaretRect.Bottom}
	okv, _, _ := pClientToScreen.Call(g.Caret, uintptr(unsafe.Pointer(&pos)))
	h := g.CaretRect.Bottom - g.CaretRect.Top
	return pos.X, pos.Y, h, okv != 0 && h > 0
}
func sendUnicode(text string) error {
	units := utf16.Encode([]rune(text))
	if len(units) == 0 {
		return nil
	}
	ins := make([]input, 0, len(units)*2)
	for _, u := range units {
		if u < 32 || u == 127 {
			return errors.New("refusing to inject a control character")
		}
		ins = append(ins, input{Type: 1, Scan: u, Flags: 0x4, Extra: 0x54594e58}, input{Type: 1, Scan: u, Flags: 0x6, Extra: 0x54594e58})
	}
	n, _, e := pSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(input{}))
	if n != uintptr(len(ins)) {
		return fmt.Errorf("Windows accepted %d/%d input events (error %v); do not retry blindly if text was partly inserted", n, len(ins), e)
	}
	return nil
}
func waitRelease(timeout time.Duration, acceptKeys ...uintptr) bool {
	pressed := func() bool {
		if modifiersDown() || keyDown(0x09) {
			return true
		}
		for _, key := range acceptKeys {
			if key != 0 && keyDown(key) {
				return true
			}
		}
		return false
	}
	end := time.Now().Add(timeout)
	for pressed() {
		if time.Now().After(end) {
			return false
		}
		time.Sleep(15 * time.Millisecond)
	}
	return true
}
