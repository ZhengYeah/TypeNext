//go:build windows && amd64

package win

import (
	"time"
	"typenext/internal/core"
	"unicode"
	"unicode/utf16"
	"unsafe"
)

var (
	pGetKeyboardLayout = user32.NewProc("GetKeyboardLayout")
	pGetKeyState       = user32.NewProc("GetKeyState")
	pToUnicodeEx       = user32.NewProc("ToUnicodeEx")
	pImmIsIME          = imm32.NewProc("ImmIsIME")
)

const typeNextInputMarker = 0x54594e58

// Only native input metadata crosses the hook boundary. Translation and UIA
// admission run on the context worker, never inside a low-level callback.
type keyboardContextEvent struct {
	Window, Focus   uintptr
	Thread          uint32
	Layout          uintptr
	VK, Scan, Flags uint32
	Modifiers       uint32
	Keyboard        [256]byte
	At              time.Time
}

// The hook runs before Windows updates the asynchronous key state. Keep toggle
// transitions locally, and apply the current event after refreshing modifiers.
// No text or history is held in this object.
type keyboardHookState struct {
	keys        [256]byte
	initialized bool
}

func keyboardModifier(vk uint32) bool {
	return vk == 0x10 || vk == 0x11 || vk == 0x12 ||
		(vk >= 0xa0 && vk <= 0xa5) || vk == 0x5b || vk == 0x5c
}

func keyboardToggle(vk uint32) bool { return vk == 0x14 || vk == 0x90 || vk == 0x91 }

func (s *keyboardHookState) capture(k *keyboardHook, message uintptr) ([256]byte, uint32) {
	if !s.initialized {
		for _, vk := range []uintptr{0x14, 0x90, 0x91} {
			v, _, _ := pGetKeyState.Call(vk)
			s.keys[vk] = byte(v & 1)
			if keyDown(vk) {
				s.keys[vk] |= 0x80
			}
		}
		s.initialized = true
	}
	for _, vk := range []uintptr{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0x5b, 0x5c} {
		s.keys[vk] &^= 0x80
		if keyDown(vk) {
			s.keys[vk] |= 0x80
		}
	}
	vk := k.VK
	// KBDLLHOOKSTRUCT normally provides side-specific modifiers, but normalize
	// generic values too rather than erasing the current transition below.
	switch vk {
	case 0x10:
		vk = 0xa0
		if k.Scan == 0x36 {
			vk = 0xa1
		}
	case 0x11:
		vk = 0xa2
		if k.Flags&1 != 0 {
			vk = 0xa3
		}
	case 0x12:
		vk = 0xa4
		if k.Flags&1 != 0 {
			vk = 0xa5
		}
	}
	s.apply(vk, message == 0x100 || message == 0x104)
	return s.keys, keyboardStateModifiers(s.keys)
}

// apply is separate from the Win32 reads so low-level pre-event state and
// repeated Caps Lock presses can be tested without generating keyboard input.
func (s *keyboardHookState) apply(vk uint32, down bool) {
	if vk < uint32(len(s.keys)) {
		wasDown := s.keys[vk]&0x80 != 0
		if keyboardToggle(vk) && down && !wasDown {
			s.keys[vk] ^= 1
		}
		s.keys[vk] &^= 0x80
		if down {
			s.keys[vk] |= 0x80
		}
	}
	s.keys[0x10] = (s.keys[0xa0] | s.keys[0xa1]) & 0x80
	s.keys[0x11] = (s.keys[0xa2] | s.keys[0xa3]) & 0x80
	s.keys[0x12] = (s.keys[0xa4] | s.keys[0xa5]) & 0x80
}

func keyboardStateModifiers(keys [256]byte) uint32 {
	var mods uint32
	for _, pair := range []struct{ vk, mod uint32 }{
		{0x10, core.ModShift}, {0x11, core.ModCtrl}, {0x12, core.ModAlt},
		{0x5b, core.ModWin}, {0x5c, core.ModWin},
	} {
		if keys[pair.vk]&0x80 != 0 {
			mods |= pair.mod
		}
	}
	return mods
}

func (a *app) observeKeyboardContext(k *keyboardHook, keys [256]byte, mods uint32, window uintptr) {
	if a.worker == nil || !a.enabled || !a.cfg.KeyboardTracking || a.apiWindow != 0 ||
		window == 0 || window == a.window || window == a.hotkeyWindow || window == a.overlay {
		return
	}
	thread, _, _ := pGetWindowThreadProcessId.Call(window, 0)
	g := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := pGetGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&g)))
	if ok == 0 || g.Focus == 0 || thread == 0 {
		a.worker.InvalidateTracking()
		return
	}
	layout, _, _ := pGetKeyboardLayout.Call(thread)
	a.worker.ObserveKey(keyboardContextEvent{
		Window: window, Focus: g.Focus, Thread: uint32(thread), Layout: layout,
		VK: k.VK, Scan: k.Scan, Flags: k.Flags, Modifiers: mods,
		Keyboard: keys, At: time.Now(),
	})
}

// translateKeyboardEvent is called only after the worker has verified the
// current approved application, focused control, and non-password properties.
// IME keystrokes are not committed text. A UIA snapshot must resynchronize them.
func translateKeyboardEvent(e keyboardContextEvent) (core.ShadowEdit, bool, bool) {
	if e.Layout == 0 || e.VK >= 256 || e.Flags&0x10 != 0 {
		return core.ShadowEdit{}, false, false
	}
	ime, _, _ := pImmIsIME.Call(e.Layout)
	if ime != 0 || keyboardLayoutMayCompose(e.Layout) || composing(e.Window) {
		return core.ShadowEdit{}, false, false
	}
	deadKey := false
	edit, trustworthy := decodeKeyboardEdit(e, func() (string, bool) {
		var chars [8]uint16
		// Bit 2 leaves the keyboard's dead-key buffer untouched (Windows 10
		// 1607+), so observing text cannot alter the target application's input.
		n, _, _ := pToUnicodeEx.Call(uintptr(e.VK), uintptr(e.Scan),
			uintptr(unsafe.Pointer(&e.Keyboard[0])), uintptr(unsafe.Pointer(&chars[0])),
			uintptr(len(chars)), 4, e.Layout)
		count := int32(n)
		deadKey = count < 0
		if count <= 0 || int(count) > len(chars) {
			return "", false
		}
		return string(utf16.Decode(chars[:count])), true
	})
	return edit, trustworthy, deadKey
}

func keyboardLayoutMayCompose(layout uintptr) bool {
	// Modern TSF IMEs may not expose an IMM context or return true from
	// ImmIsIME. Fail closed for CJK input layouts, including their temporary
	// Latin mode; UIA can still read the application's committed text.
	primaryLanguage := uint16(layout) & 0x3ff
	return primaryLanguage == 0x04 || primaryLanguage == 0x11 || primaryLanguage == 0x12
}

func decodeKeyboardEdit(e keyboardContextEvent, text func() (string, bool)) (core.ShadowEdit, bool) {
	edit := core.ShadowEdit{Shift: e.Modifiers&core.ModShift != 0, Ctrl: e.Modifiers&core.ModCtrl != 0}
	if e.Modifiers&(core.ModAlt|core.ModWin) != 0 {
		return edit, false
	}
	if edit.Ctrl {
		switch {
		case e.VK == 'A' && !edit.Shift:
			edit.Kind = core.EditSelectAll
			return edit, true
		case e.VK == 0x24:
			edit.Kind = core.EditHome
			return edit, true
		case e.VK == 0x23:
			edit.Kind = core.EditEnd
			return edit, true
		}
		// Clipboard edits, word navigation/deletion, undo, redo, and arbitrary
		// editor shortcuts have application-specific effects. Do not guess.
		return edit, false
	}
	switch e.VK {
	case 0x08:
		edit.Kind = core.EditBackspace
	case 0x2e:
		// Shift+Delete is Cut, not forward deletion.
		if edit.Shift {
			return edit, false
		}
		edit.Kind = core.EditDelete
	case 0x25:
		edit.Kind = core.EditLeft
	case 0x27:
		edit.Kind = core.EditRight
	case 0x09, 0x0d, 0x1b, 0x21, 0x22, 0x24, 0x23, 0x26, 0x28, 0x2d, 0xe5, 0xe7:
		// Enter may submit, Tab may change focus, and vertical movement may
		// follow soft wrapping. Plain Home/End may use visual lines or smart
		// indentation. None has a generic shadow-editor interpretation.
		return edit, false
	default:
		if keyboardModifier(e.VK) || keyboardToggle(e.VK) || e.VK >= 0x70 && e.VK <= 0x87 {
			return edit, false
		}
		value, ok := text()
		if !ok || value == "" {
			return edit, false
		}
		for _, r := range value {
			if unicode.IsControl(r) || r == unicode.ReplacementChar {
				return edit, false
			}
		}
		edit.Kind, edit.Text = core.EditInsert, value
	}
	return edit, true
}
