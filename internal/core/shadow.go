package core

import (
	"fmt"
	"sync"
	"unicode"
	"unicode/utf8"
)

// SyncState describes what the reader knows about the current insertion point.
type SyncState string

const (
	StateUnknown      SyncState = "unknown"
	StateSynchronized SyncState = "synchronized"
	StateTracked      SyncState = "tracked"
	StateUncertain    SyncState = "uncertain"
)

type ShadowEditKind uint8

const (
	EditUnknown ShadowEditKind = iota
	EditInsert
	EditBackspace
	EditDelete
	EditLeft
	EditRight
	EditHome
	EditEnd
	EditSelectAll
)

// ShadowEdit describes an observed edit, not a command for TypeNext to inject.
// Text must be committed text; IME composition and unrecognized shortcuts must
// invalidate the shadow instead of being translated into speculative characters.
type ShadowEdit struct {
	Kind  ShadowEditKind
	Text  string
	Shift bool
	Ctrl  bool
}

// ShadowEditor retains a bounded, in-memory region for only the current focus.
// Its offsets count runes. Operations that need missing text or ambiguous
// grapheme/word boundaries discard context instead of guessing.
type ShadowEditor struct {
	mu          sync.Mutex
	limit       int
	window      uint64
	focusID     string
	text        []rune
	caret       int
	anchor      int
	knownBefore bool
	knownAfter  bool
	state       SyncState
	confidence  float64
	version     uint64
	meta        TextContext
}

func NewShadowEditor(maxRunes int) *ShadowEditor {
	if maxRunes <= 0 {
		maxRunes = 10000
	}
	return &ShadowEditor{limit: maxRunes, state: StateUnknown}
}

// Focus discards the previous field, including when returning to an earlier one.
// Callers must supply the actual focused control identity, not just a window ID.
func (s *ShadowEditor) Focus(window uint64, focusID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window == window && s.focusID == focusID {
		return
	}
	s.window, s.focusID = window, focusID
	s.clearLocked(StateUnknown)
	s.meta = TextContext{}
}

// Reconcile replaces all tracked text with an authoritative, collapsed-caret
// snapshot. A stale result for another focused field is ignored. Call Focus
// before Reconcile, and discard reads spanning intervening keyboard events.
func (s *ShadowEditor) Reconcile(t TextContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window == 0 || s.focusID == "" || t.Window != s.window || t.FocusID != s.focusID {
		return
	}
	before, after := []rune(t.Prefix), []rune(t.Suffix)
	oldText, oldCaret, oldAnchor := s.text, s.caret, s.anchor
	oldBefore, oldAfter, oldState, oldMeta := s.knownBefore, s.knownAfter, s.state, s.meta
	s.text = append(before, after...)
	s.caret, s.anchor = len(before), len(before)
	s.knownBefore, s.knownAfter = t.KnownBefore, t.KnownAfter
	s.state, s.meta = StateSynchronized, t
	// Keep only metadata here. Retaining the provider's original strings would
	// bypass the bounded buffer and keep discarded context alive in memory.
	s.meta.Prefix, s.meta.Suffix = "", ""
	s.confidence = 1
	if t.State == StateTracked {
		s.state, s.confidence = StateTracked, 0.7
	}
	if t.Confidence > 0 && t.Confidence <= 1 {
		s.confidence = t.Confidence
	}
	s.boundLocked()
	// Compare the retained region after bounding so repeated captures do not
	// manufacture a new logical caret, even when the provider reads more text.
	unchanged := oldState == s.state && oldAnchor == oldCaret &&
		string(oldText[:oldCaret]) == string(s.text[:s.caret]) &&
		string(oldText[oldCaret:]) == string(s.text[s.caret:]) &&
		oldBefore == s.knownBefore && oldAfter == s.knownAfter &&
		oldMeta.CaretID == t.CaretID && oldMeta.ProviderID == t.ProviderID &&
		(t.CaretID != "" || oldMeta.X == t.X && oldMeta.Y == t.Y)
	if !unchanged {
		s.version++
	}
}

func (s *ShadowEditor) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Invalidate clears both text and position. Subsequent committed typing can
// establish a new partial prefix; it can never recover the discarded document.
func (s *ShadowEditor) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked(StateUncertain)
}

func (s *ShadowEditor) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window, s.focusID = 0, ""
	s.clearLocked(StateUnknown)
	s.meta = TextContext{}
}

func (s *ShadowEditor) clearLocked(state SyncState) {
	s.text = nil
	s.caret, s.anchor = 0, 0
	s.knownBefore, s.knownAfter = false, false
	s.state = state
	s.confidence = 0
	s.version++
}

// Apply returns whether a usable collapsed-caret context remains. Selection
// edits can succeed while returning false: completion is suspended until the
// selection collapses. Unknown operations always invalidate the context.
func (s *ShadowEditor) Apply(edit ShadowEdit) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window == 0 || s.focusID == "" {
		return false
	}
	if edit.Kind == EditUnknown {
		s.clearLocked(StateUncertain)
		return false
	}
	if edit.Kind == EditInsert {
		if edit.Text == "" {
			return s.usableLocked()
		}
		if !utf8.ValidString(edit.Text) {
			s.clearLocked(StateUncertain)
			return false
		}
		// UNKNOWN/UNCERTAIN has no old text. The just-observed committed
		// characters are a known suffix before the current caret.
		start, end := s.selectionLocked()
		insert := []rune(edit.Text)
		text := make([]rune, 0, start+len(insert)+len(s.text)-end)
		text = append(text, s.text[:start]...)
		text = append(text, insert...)
		text = append(text, s.text[end:]...)
		s.text = text
		s.caret, s.anchor = start+len(insert), start+len(insert)
	} else {
		if s.state == StateUnknown || s.state == StateUncertain {
			s.clearLocked(StateUncertain)
			return false
		}
		if !s.applyPositionLocked(edit) {
			s.clearLocked(StateUncertain)
			return false
		}
	}
	s.state = StateTracked
	s.confidence = 0.7
	s.version++
	s.boundLocked()
	return s.usableLocked()
}

func (s *ShadowEditor) selectionLocked() (int, int) {
	if s.anchor < s.caret {
		return s.anchor, s.caret
	}
	return s.caret, s.anchor
}

func (s *ShadowEditor) applyPositionLocked(e ShadowEdit) bool {
	start, end := s.selectionLocked()
	selected := start != end
	if e.Kind == EditSelectAll {
		if !s.knownBefore || !s.knownAfter {
			return false
		}
		s.anchor, s.caret = 0, len(s.text)
		return true
	}
	if e.Kind == EditBackspace || e.Kind == EditDelete {
		if e.Shift || e.Ctrl && selected {
			return false
		}
		if !selected {
			var ok bool
			if e.Kind == EditBackspace {
				start, ok = s.moveLocked(-1, e.Ctrl)
			} else {
				end, ok = s.moveLocked(1, e.Ctrl)
			}
			if !ok {
				return false
			}
		}
		s.text = append(s.text[:start], s.text[end:]...)
		s.caret, s.anchor = start, start
		return true
	}
	pos := s.caret
	var ok bool
	switch e.Kind {
	case EditLeft, EditRight:
		if selected && !e.Shift && !e.Ctrl {
			pos, ok = start, true
			if e.Kind == EditRight {
				pos = end
			}
		} else {
			if selected && e.Ctrl {
				return false
			}
			direction := -1
			if e.Kind == EditRight {
				direction = 1
			}
			pos, ok = s.moveLocked(direction, e.Ctrl)
		}
	case EditHome:
		pos, ok = 0, s.knownBefore
		if !e.Ctrl {
			for i := s.caret - 1; i >= 0; i-- {
				if s.text[i] == '\n' {
					pos, ok = i+1, true
					break
				}
			}
		}
	case EditEnd:
		pos, ok = len(s.text), s.knownAfter
		if !e.Ctrl {
			for i := s.caret; i < len(s.text); i++ {
				if s.text[i] == '\r' || s.text[i] == '\n' {
					pos, ok = i, true
					break
				}
			}
		}
	default:
		return false
	}
	if !ok {
		return false
	}
	s.caret = pos
	if !e.Shift {
		s.anchor = pos
	}
	return true
}

func (s *ShadowEditor) moveLocked(direction int, word bool) (int, bool) {
	pos := s.caret
	if pos == 0 && direction < 0 {
		return pos, s.knownBefore
	}
	if pos == len(s.text) && direction > 0 {
		return pos, s.knownAfter
	}
	if word {
		return s.wordBoundaryLocked(direction)
	}
	other := pos + direction
	start, end := pos, other
	if start > end {
		start, end = end, start
	}
	// Moving across an extended grapheme using rune offsets may disagree
	// with the target editor. Ask the authoritative reader to recover it.
	for i := max(0, start-1); i < min(len(s.text), end+1); i++ {
		if complexRune(s.text[i]) || s.text[i] == '\r' {
			return pos, false
		}
	}
	return other, true
}

// Word motion is supported only inside ordinary ASCII words separated by
// spaces. Punctuation, non-ASCII words, and missing document edges vary across
// editors and therefore cannot establish a trustworthy caret from keys alone.
func (s *ShadowEditor) wordBoundaryLocked(direction int) (int, bool) {
	i := s.caret
	isWord := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	if direction < 0 {
		for i > 0 && s.text[i-1] == ' ' {
			i--
		}
		for i > 0 && isWord(s.text[i-1]) {
			i--
		}
		if i == 0 {
			return i, s.knownBefore
		}
		return i, s.text[i-1] == ' ' && i < s.caret
	}
	for i < len(s.text) && isWord(s.text[i]) {
		i++
	}
	for i < len(s.text) && s.text[i] == ' ' {
		i++
	}
	if i == len(s.text) {
		return i, s.knownAfter
	}
	return i, isWord(s.text[i]) && i > s.caret
}

func complexRune(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) ||
		r == '\u200c' || r == '\u200d' || r >= 0x1f1e6 && r <= 0x1f1ff ||
		r >= 0x1f3fb && r <= 0x1f3ff || r >= 0x1100 && r <= 0x11ff ||
		r >= 0xa960 && r <= 0xa97f || r >= 0xd7b0 && r <= 0xd7ff ||
		r >= 0xe0020 && r <= 0xe007f
}

func (s *ShadowEditor) boundLocked() {
	if s.limit <= 0 {
		s.limit = 10000
	}
	if len(s.text) <= s.limit {
		return
	}
	before := min(s.caret, s.limit/2)
	after := min(len(s.text)-s.caret, s.limit-before)
	before = min(s.caret, s.limit-after)
	start, end := s.caret-before, s.caret+after
	if start > 0 {
		s.knownBefore = false
	}
	if end < len(s.text) {
		s.knownAfter = false
	}
	s.text = append([]rune(nil), s.text[start:end]...)
	s.caret -= start
	s.anchor -= start
}

func (s *ShadowEditor) usableLocked() bool {
	return s.window != 0 && s.focusID != "" && s.caret == s.anchor &&
		(s.state == StateTracked || s.state == StateSynchronized) &&
		(len(s.text) > 0 || s.knownBefore && s.knownAfter)
}

// Snapshot returns only local context and never reads the clipboard or writes
// history. Its CaretID is stable until an edit, focus change, or invalidation.
func (s *ShadowEditor) Snapshot(prefixLimit, suffixLimit int) (TextContext, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.usableLocked() {
		return TextContext{}, false
	}
	prefixLimit, suffixLimit = max(0, prefixLimit), max(0, suffixLimit)
	start, end := max(0, s.caret-prefixLimit), min(len(s.text), s.caret+suffixLimit)
	t := s.meta
	t.Window, t.FocusID = s.window, s.focusID
	t.Prefix, t.Suffix = string(s.text[start:s.caret]), string(s.text[s.caret:end])
	t.CaretID = fmt.Sprintf("shadow:%d", s.version)
	t.Source, t.ProviderID = "keyboard-history", "keyboard-history:"+s.focusID
	t.State = s.state
	t.KnownBefore, t.KnownAfter = s.knownBefore && start == 0, s.knownAfter && end == len(s.text)
	t.Partial = !t.KnownBefore || !t.KnownAfter
	t.Confidence = s.confidence
	return t, true
}
