package core

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHotkeyParsing(t *testing.T) {
	cases := []struct {
		raw, canonical string
		vk, mods       uint32
	}{
		{"Ctrl+Shift+F9", "Ctrl+Shift+F9", 0x78, ModCtrl | ModShift},
		{" control + alt + space ", "Ctrl+Alt+Space", 0x20, ModCtrl | ModAlt},
		{"shift+alt+n", "Alt+Shift+N", 'N', ModAlt | ModShift},
		{"Ctrl+Alt+Right", "Ctrl+Alt+Right", 0x27, ModCtrl | ModAlt},
		{"Ctrl+7", "Ctrl+7", '7', ModCtrl},
		{"Ctrl+F24", "Ctrl+F24", 0x87, ModCtrl},
		{"Alt+PageUp", "Alt+PageUp", 0x21, ModAlt},
		{"Ctrl+PageDown", "Ctrl+PageDown", 0x22, ModCtrl},
		{"Ctrl+Delete", "Ctrl+Delete", 0x2e, ModCtrl},
		{"", "None", 0, 0}, {"none", "None", 0, 0}, {"DISABLED", "None", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			h, err := ParseHotkey(tc.raw)
			if err != nil || h.VK != tc.vk || h.Modifiers != tc.mods || h.String() != tc.canonical {
				t.Fatalf("got %+v %s / %v", h, h.String(), err)
			}
			round, err := ParseHotkey(h.String())
			if err != nil || round != h {
				t.Fatalf("round-trip failed: %+v %v", round, err)
			}
		})
	}
}
func TestHotkeyRejectsUnsafeOrInvalidBindings(t *testing.T) {
	for _, raw := range []string{"A", "Shift+A", "F9", "Ctrl+F12", "Ctrl+F0", "Ctrl+F25", "Ctrl+", "Ctrl++A", "Ctrl+Ctrl+J", "Ctrl+Space+J", "Win+N", "Ctrl+Win+N", "Ctrl+Tab", "Ctrl+Enter", "Ctrl+Esc", "Alt+F4", "Ctrl+Alt+Delete", "Ctrl+Fbanana"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseHotkey(raw); err == nil {
				t.Fatal("invalid shortcut accepted")
			}
		})
	}
}
func TestHotkeyMatchesExactly(t *testing.T) {
	h, _ := ParseHotkey("Ctrl+Shift+F9")
	if !h.Matches(0x78, ModCtrl|ModShift) {
		t.Fatal("exact match missed")
	}
	for _, mods := range []uint32{0, ModCtrl, ModCtrl | ModShift | ModAlt, ModCtrl | ModShift | ModWin} {
		if h.Matches(0x78, mods) {
			t.Fatal("wrong modifiers matched")
		}
	}
	if h.Matches(0x79, h.Modifiers) || (Hotkey{}).Matches(0, 0) {
		t.Fatal("wrong or disabled key matched")
	}
}
func TestHotkeyConfigValidationAndMigration(t *testing.T) {
	c := DefaultConfig()
	c.AcceptHotkey = "shift+ctrl+f9"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "same shortcut") {
		t.Fatalf("duplicate not detected: %v", err)
	}
	c.AcceptHotkey = "none"
	c.SuggestHotkey = ""
	c.PauseHotkey = "disabled"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.SuggestHotkey = "Invalid"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid binding not detected")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	// v0.1.0 configurations do not contain hotkey fields. New defaults must
	// load without changing the user's model, endpoint, or application list.
	old := `{"provider":"ollama","model":"my-local-model","endpoint":"http://127.0.0.1:11435","allowed_apps":["typora.exe"],"automatic_suggestions":true}`
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuggestHotkey != "Ctrl+Shift+F9" || got.AcceptHotkey != "Ctrl+Shift+F10" || got.PauseHotkey != "Ctrl+Shift+F11" || got.Model != "my-local-model" || !got.Auto || !reflect.DeepEqual(got.AllowedApps, []string{"typora.exe"}) || got.Endpoint != "http://127.0.0.1:11435" {
		t.Fatalf("bad migration: %+v", got)
	}
	got.SuggestHotkey = "Alt+Shift+N"
	got.PauseHotkey = "None"
	if err := SaveConfig(path, got); err != nil {
		t.Fatal(err)
	}
	round, err := LoadConfig(path)
	if err != nil || !reflect.DeepEqual(round, got) {
		t.Fatalf("custom bindings not preserved: %+v %v", round, err)
	}
}

type fakeHotkeyRegistrar struct {
	occupied               map[Hotkey]bool
	owned                  map[int]Hotkey
	registers, unregisters []int
	failRelease            map[int]bool
}

func newFakeRegistrar() *fakeHotkeyRegistrar {
	return &fakeHotkeyRegistrar{occupied: map[Hotkey]bool{}, owned: map[int]Hotkey{}, failRelease: map[int]bool{}}
}
func (r *fakeHotkeyRegistrar) Register(id int, key Hotkey) error {
	r.registers = append(r.registers, id)
	if _, ok := r.owned[id]; ok {
		return errors.New("id already owned")
	}
	if r.occupied[key] {
		return errors.New("hotkey already registered (1409)")
	}
	for _, other := range r.owned {
		if key == other {
			return errors.New("own hotkey conflict")
		}
	}
	r.owned[id] = key
	return nil
}
func (r *fakeHotkeyRegistrar) Unregister(id int) error {
	r.unregisters = append(r.unregisters, id)
	if r.failRelease[id] {
		return errors.New("release failed")
	}
	delete(r.owned, id)
	return nil
}
func defaultBindings(t *testing.T) []HotkeyBinding {
	t.Helper()
	b, err := DefaultConfig().HotkeyBindings()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHotkeyConflictIsNonfatal(t *testing.T) {
	for _, blockedID := range []int{1, 2, 3} {
		t.Run(string(rune('0'+blockedID)), func(t *testing.T) {
			r := newFakeRegistrar()
			b := defaultBindings(t)
			r.occupied[b[blockedID-1].Key] = true
			s := NewHotkeySet(r)
			states := s.Apply(b)
			if len(r.registers) != 3 {
				t.Fatal("stopped at first conflict")
			}
			for _, st := range states {
				if st.Binding.ID == blockedID {
					if st.Err == nil || st.Active.Enabled() || s.Matches(st.Binding.Key.VK, st.Binding.Key.Modifiers) {
						t.Fatal("conflicting key treated as active")
					}
				} else if st.Err != nil || st.Active != st.Binding.Key {
					t.Fatalf("unaffected shortcut failed: %+v", st)
				}
			}
			s.Close()
			if len(r.owned) != 0 || len(r.unregisters) != 2 {
				t.Fatal("wrong cleanup")
			}
			if !r.occupied[b[blockedID-1].Key] {
				t.Fatal("other app's registration modified")
			}
		})
	}
}
func TestAllHotkeyConflictsLeaveManagerUsable(t *testing.T) {
	r := newFakeRegistrar()
	b := defaultBindings(t)
	for _, binding := range b {
		r.occupied[binding.Key] = true
	}
	s := NewHotkeySet(r)
	for _, st := range s.Apply(b) {
		if st.Err == nil || st.Active.Enabled() {
			t.Fatal("missing conflict")
		}
	}
	// Once another app releases one key, Apply retries it without a restart.
	delete(r.occupied, b[0].Key)
	if states := s.Apply(b); !states[0].Active.Enabled() || states[0].Err != nil {
		t.Fatal("could not recover")
	}
}
func TestHotkeyUnchangedApplyDoesNotRegisterTwice(t *testing.T) {
	r := newFakeRegistrar()
	s := NewHotkeySet(r)
	b := defaultBindings(t)
	s.Apply(b)
	for _, st := range s.Apply(b) {
		if st.Err != nil {
			t.Fatal(st.Err)
		}
	}
	if len(r.registers) != 3 || len(r.unregisters) != 0 {
		t.Fatal("unchanged bindings were re-registered")
	}
}
func TestHotkeySwapAndDisable(t *testing.T) {
	r := newFakeRegistrar()
	s := NewHotkeySet(r)
	b := defaultBindings(t)
	s.Apply(b)
	b[0].Key, b[1].Key = b[1].Key, b[0].Key
	b[2].Key = Hotkey{}
	for _, st := range s.Apply(b) {
		if st.Err != nil || st.Active != st.Binding.Key {
			t.Fatalf("bad swap/disable: %+v", st)
		}
	}
	if s.Active(3).Enabled() || len(r.owned) != 2 || len(r.unregisters) != 3 {
		t.Fatal("disabled key retained")
	}
	s.Close()
	s.Close()
	if len(r.owned) != 0 || len(r.unregisters) != 5 {
		t.Fatal("close not idempotent")
	}
}
func TestHotkeyReleaseFailureDoesNotForgetActiveKey(t *testing.T) {
	r := newFakeRegistrar()
	s := NewHotkeySet(r)
	b := defaultBindings(t)
	s.Apply(b)
	old := b[0].Key
	r.failRelease[1] = true
	b[0].Key, _ = ParseHotkey("Alt+Shift+N")
	states := s.Apply(b)
	if states[0].Err == nil || states[0].Active != old || s.Active(1) != old || len(r.registers) != 3 {
		t.Fatal("lost active binding or registered over it")
	}
	r.failRelease[1] = false
	states = s.Apply(b)
	if states[0].Err != nil || states[0].Active != b[0].Key {
		t.Fatal("release failure did not recover")
	}
}
