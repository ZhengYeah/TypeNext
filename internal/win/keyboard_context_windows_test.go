//go:build windows && amd64

package win

import (
	"testing"
	"typenext/internal/core"
)

func TestKeyboardLayoutRejectsCJKWithoutAnIMMContext(t *testing.T) {
	for _, layout := range []uintptr{0x08040804, 0x04040404, 0x04110411, 0x04120412} {
		if !keyboardLayoutMayCompose(layout) {
			t.Fatalf("CJK layout %#x could be mistaken for committed Latin text", layout)
		}
	}
	for _, layout := range []uintptr{0x04090409, 0x04070407, 0x040c040c} {
		if keyboardLayoutMayCompose(layout) {
			t.Fatalf("non-IME layout %#x was rejected", layout)
		}
	}
}

func TestHookStateAppliesCurrentModifierBeforeTranslation(t *testing.T) {
	var state keyboardHookState
	state.apply(0xa0, true)
	if got := keyboardStateModifiers(state.keys); got != core.ModShift {
		t.Fatalf("Shift keydown saw pre-event modifiers: %x", got)
	}
	state.apply(0xa1, true)
	state.apply(0xa0, false)
	if state.keys[0x10]&0x80 == 0 {
		t.Fatal("releasing one Shift erased the other held Shift")
	}
	state.apply(0xa1, false)
	if got := keyboardStateModifiers(state.keys); got != 0 {
		t.Fatalf("Shift keyup did not clear aggregate modifier: %x", got)
	}
	state.apply(0xa3, true)
	state.apply(0xa5, true)
	if got := keyboardStateModifiers(state.keys); got != core.ModCtrl|core.ModAlt {
		t.Fatalf("right-side Ctrl+Alt state incorrect: %x", got)
	}
}

func TestHookCapsLockTogglesOncePerPress(t *testing.T) {
	var state keyboardHookState
	state.apply(0x14, true)
	state.apply(0x14, true)
	state.apply('A', true)
	if state.keys[0x14]&1 != 1 {
		t.Fatal("repeated Caps Lock keydown changed the toggle twice")
	}
	state.apply(0x14, false)
	state.apply(0x14, true)
	if state.keys[0x14]&1 != 0 {
		t.Fatal("second Caps Lock press did not disable the toggle")
	}
}

func TestKeyboardDecoderRejectsUnobservedEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		vk   uint32
		mods uint32
	}{
		{"paste", 'V', core.ModCtrl},
		{"cut", 'X', core.ModCtrl},
		{"undo", 'Z', core.ModCtrl},
		{"redo", 'Y', core.ModCtrl},
		{"shift cut", 0x2e, core.ModShift},
		{"shift paste", 0x2d, core.ModShift},
		{"word movement", 0x25, core.ModCtrl},
		{"submit or newline", 0x0d, 0},
		{"tab focus", 0x09, 0},
		{"soft-wrap movement", 0x26, 0},
		{"visual Home", 0x24, 0},
		{"visual End", 0x23, 0},
		{"IME", 0xe5, 0},
		{"Unicode packet", 0xe7, 0},
		{"AltGr", 'E', core.ModCtrl | core.ModAlt},
		{"function key", 0x70, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := decodeKeyboardEdit(keyboardContextEvent{VK: tc.vk, Modifiers: tc.mods}, func() (string, bool) {
				t.Fatal("unknown edit reached character translation")
				return "a", true
			})
			if ok {
				t.Fatal("application-specific edit was treated as observed text")
			}
		})
	}
}

func TestKeyboardDecoderPreservesSelectionAndTranslatedUnicode(t *testing.T) {
	edit, ok := decodeKeyboardEdit(keyboardContextEvent{VK: 0x25, Modifiers: core.ModShift}, nil)
	if !ok || edit.Kind != core.EditLeft || !edit.Shift {
		t.Fatalf("Shift+Left lost selection state: %+v, %v", edit, ok)
	}
	edit, ok = decodeKeyboardEdit(keyboardContextEvent{VK: 'A', Modifiers: core.ModCtrl}, nil)
	if !ok || edit.Kind != core.EditSelectAll {
		t.Fatalf("Ctrl+A did not preserve known selection: %+v, %v", edit, ok)
	}
	edit, ok = decodeKeyboardEdit(keyboardContextEvent{VK: 0x24, Modifiers: core.ModCtrl | core.ModShift}, nil)
	if !ok || edit.Kind != core.EditHome || !edit.Ctrl || !edit.Shift {
		t.Fatalf("Ctrl+Shift+Home lost document-boundary selection: %+v, %v", edit, ok)
	}
	edit, ok = decodeKeyboardEdit(keyboardContextEvent{VK: 'E'}, func() (string, bool) { return "é", true })
	if !ok || edit.Kind != core.EditInsert || edit.Text != "é" {
		t.Fatalf("translated text was not preserved: %+v, %v", edit, ok)
	}
	for _, text := range []string{"", "\x00", "\r", "\ufffd"} {
		if _, ok := decodeKeyboardEdit(keyboardContextEvent{VK: 'A'}, func() (string, bool) { return text, true }); ok {
			t.Fatalf("unsafe translated text accepted: %q", text)
		}
	}
	if _, ok := decodeKeyboardEdit(keyboardContextEvent{VK: 'A'}, func() (string, bool) { return "", false }); ok {
		t.Fatal("dead-key translation was accepted as committed text")
	}
}
