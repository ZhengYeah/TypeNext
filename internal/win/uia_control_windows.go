//go:build windows && amd64

package win

import (
	"errors"
	"fmt"
	"strings"
)

// A role only makes an element eligible for further inspection. Callers must
// still require a focused, enabled, non-password element with TextPattern and a
// reliable collapsed selection before reading text.
func textControlAllowed(control int32) bool {
	switch control {
	case 50004, 50030: // Edit, Document
		return true
	case 50020, 50025, 50033: // Text, Custom, Pane
		// Qt can report an editable field as Text when its native virtual
		// keyboard is disabled. These roles need explicit writability below.
		return true
	default:
		return false
	}
}

func validateTextEditability(control int32, readOnly, known bool) error {
	if !textControlAllowed(control) {
		return focusedTextControlError("", control)
	}
	if known && readOnly {
		return errors.New("read-only text: completion disabled")
	}
	if control != 50004 && control != 50030 && !known {
		return errors.New("focused custom text control does not confirm that it is editable; no text was read")
	}
	return nil
}

func focusedTextControlError(process string, control int32) error {
	if control == 50032 && (strings.EqualFold(process, "weixin.exe") || strings.EqualFold(process, "wechat.exe")) {
		return errors.New("Weixin exposes only its outer window. Try Settings > General > 读屏优化模式 (Screen reader optimization), then refocus the message box; no text was read")
	}
	return fmt.Errorf("focused control is not a supported accessible text editor (control type %d); no text was read", control)
}
