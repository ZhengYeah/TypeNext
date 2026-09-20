//go:build windows && amd64

package win

import (
	"time"
	"typenext/internal/core"
)

// Shift is part of normal typing. Command modifiers still cancel pending
// suggestions, but must not arm another request merely because a shortcut uses
// a letter. No character contents are retained by this activity classifier.
func automaticTypingKey(vk, modifiers uint32) bool {
	if modifiers&(core.ModCtrl|core.ModAlt|core.ModWin) != 0 {
		return false
	}
	switch {
	case vk == 0x08, vk == 0x0d, vk == 0x20, vk == 0x2e: // Backspace, Enter, Space, Delete
		return true
	case vk >= 0x30 && vk <= 0x5a: // digits and letters
		return true
	case vk >= 0x60 && vk <= 0x6f: // numpad digits and operators
		return true
	case vk >= 0xba && vk <= 0xc0, vk >= 0xdb && vk <= 0xdf: // punctuation
		return true
	case vk == 0xe2, vk == 0xe5, vk == 0xe7: // OEM 102, IME ProcessKey, Unicode Packet
		return true
	}
	return false
}

func (a *app) keyboardActivity(vk, modifiers uint32, window uintptr) {
	// Associate the input with its window immediately. Otherwise the next
	// foreground poll can discard typing that began just after an app switch.
	if window != a.lastForeground && a.worker != nil {
		a.worker.ResetTracking()
	}
	a.lastForeground = window
	a.invalidate(window != 0 && automaticTypingKey(vk, modifiers))
}

func (a *app) observeForeground(window uintptr) {
	if window != a.lastForeground {
		a.lastForeground = window
		if a.worker != nil {
			a.worker.ResetTracking()
		}
		a.invalidate(false)
	}
}

func (a *app) automaticDue(now time.Time, modifiers bool) bool {
	return a.enabled && a.apiWindow == 0 && a.autoArmed && !a.running &&
		!modifiers && a.cfg.AutomaticDue(now, a.lastRemoteRequest) &&
		now.Sub(a.lastActivity) >= time.Duration(a.cfg.DebounceMS)*time.Millisecond
}
