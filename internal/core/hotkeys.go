package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	ModAlt        uint32 = 1
	ModCtrl       uint32 = 2
	ModShift      uint32 = 4
	ModWin        uint32 = 8
	HotkeySuggest        = 1
	HotkeyAccept         = 2
	HotkeyPause          = 3
)

// Hotkey is a normalized Windows virtual key plus modifiers.
// The zero value disables an action. MOD_NOREPEAT is a registration option, not part of identity.
type Hotkey struct{ Modifiers, VK uint32 }

func (h Hotkey) Enabled() bool { return h.VK != 0 }
func (h Hotkey) Matches(vk, modifiers uint32) bool {
	return h.Enabled() && h.VK == vk && h.Modifiers == modifiers
}

var namedKeys = map[string]uint32{
	"SPACE": 0x20, "LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28,
	"HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
	"INSERT": 0x2d, "DELETE": 0x2e,
}

func ParseHotkey(raw string) (Hotkey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") || strings.EqualFold(raw, "disabled") {
		return Hotkey{}, nil
	}
	parts := strings.Split(strings.ToUpper(raw), "+")
	var h Hotkey
	for _, part := range parts[:len(parts)-1] {
		var bit uint32
		switch strings.TrimSpace(part) {
		case "CTRL", "CONTROL":
			bit = ModCtrl
		case "ALT":
			bit = ModAlt
		case "SHIFT":
			bit = ModShift
		default:
			return h, errors.New("use Ctrl, Alt, and/or Shift modifiers; Windows-key shortcuts are not supported")
		}
		if h.Modifiers&bit != 0 {
			return h, errors.New("a shortcut cannot repeat a modifier")
		}
		h.Modifiers |= bit
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	switch {
	case len(key) == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')):
		h.VK = uint32(key[0])
	case strings.HasPrefix(key, "F"):
		n, err := strconv.Atoi(key[1:])
		if err == nil && n >= 1 && n <= 24 {
			h.VK = uint32(0x6f + n)
		}
	default:
		h.VK = namedKeys[key]
	}
	if h.VK == 0 {
		return h, errors.New("use a letter, digit, F1–F24 (not F12), Space, arrow, Home, End, PageUp, PageDown, Insert, or Delete")
	}
	if h.VK == 0x7b {
		return h, errors.New("F12 is reserved by Windows for debuggers; choose another key")
	}
	if h.Modifiers&(ModCtrl|ModAlt) == 0 {
		return h, errors.New("include Ctrl or Alt so ordinary typing is not captured")
	}
	if h.VK == 0x73 && h.Modifiers&ModAlt != 0 {
		return h, errors.New("Alt+F4 is reserved for closing windows; choose another key")
	}
	if h.VK == 0x2e && h.Modifiers&(ModCtrl|ModAlt) == (ModCtrl|ModAlt) {
		return h, errors.New("Ctrl+Alt+Delete is reserved by Windows; choose another key")
	}
	return h, nil
}

func (h Hotkey) String() string {
	if !h.Enabled() {
		return "None"
	}
	var parts []string
	if h.Modifiers&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if h.Modifiers&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if h.Modifiers&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	key := ""
	switch {
	case h.VK >= 0x70 && h.VK <= 0x87:
		key = fmt.Sprintf("F%d", h.VK-0x6f)
	case h.VK >= 'A' && h.VK <= 'Z' || h.VK >= '0' && h.VK <= '9':
		key = string(rune(h.VK))
	default:
		for name, vk := range namedKeys {
			if vk == h.VK {
				key = name[:1] + strings.ToLower(name[1:])
			}
		}
		if key == "Pageup" {
			key = "PageUp"
		}
		if key == "Pagedown" {
			key = "PageDown"
		}
	}
	return strings.Join(append(parts, key), "+")
}

type HotkeyBinding struct {
	ID   int
	Name string
	Key  Hotkey
}

func (c Config) HotkeyBindings() ([]HotkeyBinding, error) {
	var bindings []HotkeyBinding
	seen := map[Hotkey]string{}
	for i, v := range []struct{ name, raw string }{
		{"Suggest", c.SuggestHotkey}, {"Accept", c.AcceptHotkey}, {"Pause", c.PauseHotkey},
	} {
		h, err := ParseHotkey(v.raw)
		if err != nil {
			return nil, fmt.Errorf("%s shortcut: %w", v.name, err)
		}
		if h.Enabled() {
			if prev := seen[h]; prev != "" {
				return nil, fmt.Errorf("%s and %s cannot use the same shortcut (%s)", prev, v.name, h)
			}
			seen[h] = v.name
		}
		bindings = append(bindings, HotkeyBinding{ID: i + 1, Name: v.name, Key: h})
	}
	return bindings, nil
}

// The registrar is injected so conflict and reconfiguration behavior can be
// tested without claiming to execute Windows API calls on a non-Windows host.
type HotkeyRegistrar interface {
	Register(id int, key Hotkey) error
	Unregister(id int) error
}
type HotkeyStatus struct {
	Binding HotkeyBinding
	Active  Hotkey
	Err     error
}
type HotkeySet struct {
	registrar HotkeyRegistrar
	active    map[int]Hotkey
}

func NewHotkeySet(registrar HotkeyRegistrar) *HotkeySet {
	return &HotkeySet{registrar: registrar, active: map[int]Hotkey{}}
}
func (s *HotkeySet) Active(id int) Hotkey { return s.active[id] }
func (s *HotkeySet) Matches(vk, mods uint32) bool {
	for _, key := range s.active {
		if key.Matches(vk, mods) {
			return true
		}
	}
	return false
}

// Apply accepts validated bindings. Unchanged registrations are retained;
// changed ones are released before any registration (allowing key swaps).
// One conflict never prevents the other actions or the settings UI from working.
func (s *HotkeySet) Apply(bindings []HotkeyBinding) []HotkeyStatus {
	wanted := map[int]Hotkey{}
	for _, b := range bindings {
		wanted[b.ID] = b.Key
	}
	releaseErrors := map[int]error{}
	for id, old := range s.active {
		if old == wanted[id] {
			continue
		}
		if err := s.registrar.Unregister(id); err != nil {
			releaseErrors[id] = fmt.Errorf("could not release %s: %w", old, err)
		} else {
			delete(s.active, id)
		}
	}
	states := make([]HotkeyStatus, 0, len(bindings))
	for _, b := range bindings {
		st := HotkeyStatus{Binding: b, Active: s.active[b.ID], Err: releaseErrors[b.ID]}
		if st.Err == nil && b.Key.Enabled() && !st.Active.Enabled() {
			st.Err = s.registrar.Register(b.ID, b.Key)
			if st.Err == nil {
				s.active[b.ID] = b.Key
				st.Active = b.Key
			}
		}
		states = append(states, st)
	}
	return states
}
func (s *HotkeySet) Close() {
	for id := range s.active {
		if s.registrar.Unregister(id) == nil {
			delete(s.active, id)
		}
	}
}
