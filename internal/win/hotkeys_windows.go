//go:build windows && amd64

package win

import (
	"fmt"
	"strings"
	"syscall"
	"typenext/internal/core"
	"unsafe"
)

type windowsHotkeyRegistrar struct{ window uintptr }

func (r windowsHotkeyRegistrar) Register(id int, key core.Hotkey) error {
	ok, _, err := pRegisterHotKey.Call(r.window, uintptr(id), uintptr(key.Modifiers|0x4000), uintptr(key.VK))
	if ok != 0 {
		return nil
	}
	if err == syscall.Errno(1409) {
		return fmt.Errorf("already registered by Windows or another app (1409)")
	}
	return fmt.Errorf("Windows could not register this shortcut: %v", err)
}
func (r windowsHotkeyRegistrar) Unregister(id int) error {
	ok, _, err := pUnregisterHotKey.Call(r.window, uintptr(id))
	if ok != 0 || err == syscall.Errno(1419) {
		return nil
	}
	return err
}

func currentModifiers() uint32 {
	var mods uint32
	if keyDown(0x11) {
		mods |= core.ModCtrl
	}
	if keyDown(0x12) {
		mods |= core.ModAlt
	}
	if keyDown(0x10) {
		mods |= core.ModShift
	}
	if keyDown(0x5b) || keyDown(0x5c) {
		mods |= core.ModWin
	}
	return mods
}

// Called only on the Windows UI thread, including from input-hook callbacks.
func (a *app) applyHotkeys() {
	bindings, err := a.cfg.HotkeyBindings()
	if err != nil {
		a.setStatus(err.Error())
		return
	}
	a.hotkeyStates = a.hotkeys.Apply(bindings)
	a.refreshHotkeyUI()
}
func (a *app) hotkeyWarning() string {
	var failed []string
	for _, st := range a.hotkeyStates {
		if st.Err != nil {
			failed = append(failed, st.Binding.Name+" ("+st.Binding.Key.String()+")")
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return "Unavailable shortcut(s): " + strings.Join(failed, ", ") + ". Open Shortcuts to change or disable them."
}
func (a *app) hotkeyLabel(id int) string {
	if a.hotkeys != nil {
		if key := a.hotkeys.Active(id); key.Enabled() {
			return key.String()
		}
	}
	for _, st := range a.hotkeyStates {
		if st.Binding.ID == id && st.Err != nil {
			return "unavailable"
		}
	}
	return "off"
}
func (a *app) acceptHint() string {
	var options []string
	if a.cfg.AcceptTab {
		options = append(options, "Tab")
	}
	if a.hotkeys != nil {
		if key := a.hotkeys.Active(core.HotkeyAccept); key.Enabled() {
			options = append(options, key.String())
		}
	}
	if len(options) == 0 {
		return "No accept key: configure Shortcuts or enable Tab in settings"
	}
	return strings.Join(options, " or ") + " to insert · Esc to dismiss"
}
func (a *app) refreshHotkeyUI() {
	if a.shortcutLabel != 0 {
		s := fmt.Sprintf("Suggest: %s     Accept: %s     Pause: %s     Esc: dismiss", a.hotkeyLabel(1), a.hotkeyLabel(2), a.hotkeyLabel(3))
		pSetWindowText.Call(a.shortcutLabel, uintptr(unsafe.Pointer(u16(s))))
	}
	if a.pad != 0 {
		pSetWindowText.Call(a.pad, uintptr(unsafe.Pointer(u16("TypeNext test pad — suggest: "+a.hotkeyLabel(1)))))
	}
	if a.hotkeyWindow == 0 {
		return
	}
	for i, st := range a.hotkeyStates {
		text := "Disabled"
		if st.Active.Enabled() {
			text = "Active: " + st.Active.String()
		}
		if st.Err != nil {
			text = "Unavailable: " + st.Err.Error()
			if st.Active.Enabled() {
				text += "; keeping " + st.Active.String()
			}
		}
		pSetWindowText.Call(a.controls[ctrlHotkeyStatus1+i], uintptr(unsafe.Pointer(u16(text))))
	}
}

func (a *app) openShortcuts() {
	a.showSettings()
	if a.hotkeyWindow != 0 {
		pShowWindow.Call(a.hotkeyWindow, 9)
		pSetForegroundWindow.Call(a.hotkeyWindow)
		return
	}
	var err error
	a.hotkeyWindow, _, err = pCreateWindowEx.Call(0x00010000, uintptr(unsafe.Pointer(u16("TypeNextWindow"))), uintptr(unsafe.Pointer(u16("TypeNext — Shortcuts"))), 0x00CA0000, 130, 90, uintptr(a.s(696)), uintptr(a.s(558)), a.window, 0, a.instance, 0)
	if a.hotkeyWindow == 0 {
		a.setStatus(fmt.Sprintf("Could not open shortcuts: %v", err))
		return
	}
	control := func(class, text string, id, x, y, w, h int, style uintptr) uintptr {
		return a.controlIn(a.hotkeyWindow, class, text, id, x, y, w, h, style)
	}
	heading := control("STATIC", "Keyboard shortcuts", 0, 24, 18, 620, 32, 0)
	pSendMessage.Call(heading, 0x30, a.heading, 1)
	control("STATIC", "Type a name such as Ctrl+Shift+F9; do not press the shortcut here. Use None to disable it. Click Apply to save and activate your choices.", 0, 24, 58, 620, 44, 0)
	for i, field := range []struct{ label, value string }{
		{"Suggest", a.cfg.SuggestHotkey}, {"Accept", a.cfg.AcceptHotkey}, {"Pause / resume", a.cfg.PauseHotkey},
	} {
		y := 112 + i*82
		control("STATIC", field.label, 0, 24, y+3, 152, 25, 0)
		control("EDIT", field.value, ctrlHotkeySuggest+i, 180, y, 466, 28, 0x10080)
		status := control("STATIC", "", ctrlHotkeyStatus1+i, 180, y+34, 466, 42, 0)
		pSendMessage.Call(status, 0x30, a.smallFont, 1)
	}
	control("STATIC", "Include Ctrl or Alt. Examples: Ctrl+Shift+F9, Ctrl+Alt+Space, Alt+Shift+N. Conflicts do not close TypeNext or override another application's shortcut.", 0, 24, 366, 622, 46, 0)
	control("BUTTON", "Apply shortcuts", idHotkeyApply, 24, 426, 152, 32, 0x10000)
	control("BUTTON", "Reset fields to defaults", idHotkeyDefaults, 188, 426, 226, 32, 0x10000)
	control("BUTTON", "Close", idHotkeyClose, 544, 426, 102, 32, 0x10000)
	message := control("STATIC", "Your other settings are preserved.", ctrlHotkeyMessage, 24, 468, 622, 40, 0)
	pSendMessage.Call(message, 0x30, a.smallFont, 1)
	a.refreshHotkeyUI()
	pShowWindow.Call(a.hotkeyWindow, 5)
	pSetForegroundWindow.Call(a.hotkeyWindow)
	pSetFocus.Call(a.controls[ctrlHotkeySuggest])
}
func (a *app) closeShortcuts() {
	if a.hotkeyWindow != 0 {
		pDestroyWindow.Call(a.hotkeyWindow)
		a.hotkeyWindow = 0
	}
}
func (a *app) resetShortcutFields() {
	c := core.DefaultConfig()
	for i, value := range []string{c.SuggestHotkey, c.AcceptHotkey, c.PauseHotkey} {
		pSetWindowText.Call(a.controls[ctrlHotkeySuggest+i], uintptr(unsafe.Pointer(u16(value))))
	}
	pSetWindowText.Call(a.controls[ctrlHotkeyMessage], uintptr(unsafe.Pointer(u16("Default values filled in. Click Apply to activate them."))))
}
func (a *app) saveShortcuts() {
	message := func(s string) { pSetWindowText.Call(a.controls[ctrlHotkeyMessage], uintptr(unsafe.Pointer(u16(s)))) }
	c := a.cfg
	c.SuggestHotkey = windowText(a.controls[ctrlHotkeySuggest])
	c.AcceptHotkey = windowText(a.controls[ctrlHotkeyAccept])
	c.PauseHotkey = windowText(a.controls[ctrlHotkeyPause])
	bindings, err := c.HotkeyBindings()
	if err != nil {
		message(err.Error())
		return
	}
	c.SuggestHotkey, c.AcceptHotkey, c.PauseHotkey = bindings[0].Key.String(), bindings[1].Key.String(), bindings[2].Key.String()
	if err = core.SaveConfig(a.configPath, c); err != nil {
		message("Could not save: " + err.Error())
		return
	}
	a.invalidate(false)
	a.cfg = c
	a.applyHotkeys()
	for i, value := range []string{c.SuggestHotkey, c.AcceptHotkey, c.PauseHotkey} {
		pSetWindowText.Call(a.controls[ctrlHotkeySuggest+i], uintptr(unsafe.Pointer(u16(value))))
	}
	text := "Saved. Active shortcuts are shown above; no restart is required."
	if warning := a.hotkeyWarning(); warning != "" {
		text = "Saved, but a shortcut is unavailable. Change the marked entry and click Apply again."
	}
	message(text)
	a.setStatus(text)
}
