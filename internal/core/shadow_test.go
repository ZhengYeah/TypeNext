package core

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func focusedShadow() *ShadowEditor {
	s := NewShadowEditor(10000)
	s.Focus(42, "field")
	return s
}

func reconcileShadow(s *ShadowEditor, prefix, suffix string, before, after bool) {
	s.Reconcile(TextContext{
		Window: 42, FocusID: "field", ProviderID: "uia-provider", CaretID: "uia-caret",
		Prefix: prefix, Suffix: suffix, KnownBefore: before, KnownAfter: after,
		Source: "uia-text", State: StateSynchronized,
	})
}

func requireShadow(t *testing.T, s *ShadowEditor, prefix, suffix string) TextContext {
	t.Helper()
	c, ok := s.Snapshot(10000, 10000)
	if !ok || c.Prefix != prefix || c.Suffix != suffix {
		t.Fatalf("snapshot = (%q, %q, %v); want (%q, %q, true)", c.Prefix, c.Suffix, ok, prefix, suffix)
	}
	return c
}

func requireNoShadow(t *testing.T, s *ShadowEditor) {
	t.Helper()
	if c, ok := s.Snapshot(10000, 10000); ok {
		t.Fatalf("unexpected usable snapshot: %+v", c)
	}
}

func TestShadowUnknownFocusAccumulatesOnlyNewTyping(t *testing.T) {
	s := focusedShadow()
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "Hello 世界"})
	c := requireShadow(t, s, "Hello 世界", "")
	if c.State != StateTracked || !c.Partial || c.KnownBefore || c.KnownAfter || c.Confidence != 0.7 {
		t.Fatalf("typed partial context overclaims document knowledge: %+v", c)
	}
	s.Apply(ShadowEdit{Kind: EditBackspace})
	requireShadow(t, s, "Hello 世", "")
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "界"})
	requireShadow(t, s, "Hello 世界", "")
}

func TestShadowDiscardsHistoryOnEveryFocusChange(t *testing.T) {
	s := focusedShadow()
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "private field one"})
	s.Focus(42, "other-field")
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "second"})
	s.Focus(42, "field")
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "fresh"})
	requireShadow(t, s, "fresh", "")
	s.Focus(43, "field")
	requireNoShadow(t, s)
}

func TestShadowReconcileRejectsStaleFocusAndOverwritesTyping(t *testing.T) {
	s := focusedShadow()
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "wrong"})
	reconcileShadow(s, "actual ", "suffix", true, true)
	c := requireShadow(t, s, "actual ", "suffix")
	if c.Partial || c.State != StateSynchronized || c.Confidence != 1 {
		t.Fatalf("authoritative document was not synchronized: %+v", c)
	}
	s.Focus(42, "other-field")
	reconcileShadow(s, "stale", "", true, true)
	requireNoShadow(t, s)
}

func TestShadowSelectionReplacementAndDeletion(t *testing.T) {
	s := focusedShadow()
	reconcileShadow(s, "Hello world", "!", true, true)
	for range 5 {
		s.Apply(ShadowEdit{Kind: EditLeft, Shift: true})
	}
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "reader"})
	requireShadow(t, s, "Hello reader", "!")
	s.Apply(ShadowEdit{Kind: EditSelectAll})
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditBackspace})
	requireShadow(t, s, "", "")
}

func TestShadowArrowCollapsesSelectionWithoutExtraMove(t *testing.T) {
	s := focusedShadow()
	reconcileShadow(s, "abcd", "ef", true, true)
	s.Apply(ShadowEdit{Kind: EditLeft, Shift: true})
	s.Apply(ShadowEdit{Kind: EditLeft, Shift: true})
	s.Apply(ShadowEdit{Kind: EditLeft})
	requireShadow(t, s, "ab", "cdef")
	s.Apply(ShadowEdit{Kind: EditRight, Shift: true})
	s.Apply(ShadowEdit{Kind: EditRight, Shift: true})
	s.Apply(ShadowEdit{Kind: EditRight})
	requireShadow(t, s, "abcd", "ef")
}

func TestShadowUnknownBoundariesInvalidateNavigation(t *testing.T) {
	for _, edit := range []ShadowEdit{
		{Kind: EditRight}, {Kind: EditEnd}, {Kind: EditEnd, Ctrl: true}, {Kind: EditSelectAll},
	} {
		s := focusedShadow()
		s.Apply(ShadowEdit{Kind: EditInsert, Text: "partial"})
		if s.Apply(edit) {
			t.Fatalf("edit %+v treated unknown boundary as known", edit)
		}
		requireNoShadow(t, s)
		s.Apply(ShadowEdit{Kind: EditInsert, Text: "new"})
		requireShadow(t, s, "new", "")
	}
	s := focusedShadow()
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "x"})
	s.Apply(ShadowEdit{Kind: EditBackspace})
	requireNoShadow(t, s)
	s.Apply(ShadowEdit{Kind: EditBackspace})
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "fresh"})
	requireShadow(t, s, "fresh", "")
}

func TestShadowWordEditingAndAmbiguousPunctuation(t *testing.T) {
	s := focusedShadow()
	reconcileShadow(s, "hello world", " next", true, true)
	s.Apply(ShadowEdit{Kind: EditBackspace, Ctrl: true})
	requireShadow(t, s, "hello ", " next")
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "brave"})
	s.Apply(ShadowEdit{Kind: EditLeft, Ctrl: true})
	requireShadow(t, s, "hello ", "brave next")
	s.Apply(ShadowEdit{Kind: EditDelete, Ctrl: true})
	requireShadow(t, s, "hello ", "next")
	reconcileShadow(s, "file.path", "", true, true)
	s.Apply(ShadowEdit{Kind: EditLeft, Ctrl: true})
	requireNoShadow(t, s)
}

func TestShadowHomeEndRequireKnownLineOrDocumentEdges(t *testing.T) {
	s := focusedShadow()
	reconcileShadow(s, "first\nsecond", " last\nthird", false, false)
	s.Apply(ShadowEdit{Kind: EditHome})
	requireShadow(t, s, "first\n", "second last\nthird")
	s.Apply(ShadowEdit{Kind: EditEnd})
	requireShadow(t, s, "first\nsecond last", "\nthird")
	s.Apply(ShadowEdit{Kind: EditHome, Ctrl: true})
	requireNoShadow(t, s)
}

func TestShadowAmbiguousGraphemeEditsInvalidate(t *testing.T) {
	for _, text := range []string{"e\u0301", "👍🏽", "👩\u200d💻", "🇸🇬", "\u1100\u1161"} {
		for _, kind := range []ShadowEditKind{EditBackspace, EditLeft} {
			s := focusedShadow()
			s.Apply(ShadowEdit{Kind: EditInsert, Text: text})
			s.Apply(ShadowEdit{Kind: kind})
			requireNoShadow(t, s)
		}
	}
}

func TestShadowBoundedUnicodeContextAndBoundaryFlags(t *testing.T) {
	s := NewShadowEditor(8)
	s.Focus(42, "field")
	reconcileShadow(s, "中文测试前半部分", "后半部分测试文本", true, true)
	c, ok := s.Snapshot(3, 2)
	if !ok || !utf8.ValidString(c.Prefix+c.Suffix) || utf8.RuneCountInString(c.Prefix) != 3 || utf8.RuneCountInString(c.Suffix) != 2 {
		t.Fatalf("incorrect rune-bounded snapshot: %+v", c)
	}
	if c.KnownBefore || c.KnownAfter || !c.Partial {
		t.Fatalf("truncated boundaries must remain unknown: %+v", c)
	}
	c, _ = s.Snapshot(1000, 1000)
	if utf8.RuneCountInString(c.Prefix+c.Suffix) > 8 {
		t.Fatal("retained shadow exceeded bound")
	}
	s.Apply(ShadowEdit{Kind: EditInsert, Text: strings.Repeat("界", 1000)})
	c, _ = s.Snapshot(1000, 1000)
	if utf8.RuneCountInString(c.Prefix+c.Suffix) > 8 {
		t.Fatal("large insertion exceeded shadow bound")
	}
}

func TestShadowFingerprintStableUntilEditOrInvalidation(t *testing.T) {
	s := NewShadowEditor(8)
	s.Focus(42, "field")
	reconcileShadow(s, "long prefix", "long suffix", true, true)
	a, ok := s.Snapshot(4, 4)
	if !ok {
		t.Fatal("missing synchronized snapshot")
	}
	reconcileShadow(s, "long prefix", "long suffix", true, true)
	b, _ := s.Snapshot(4, 4)
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("unchanged bounded authoritative read changed fingerprint")
	}
	s.Apply(ShadowEdit{Kind: EditInsert, Text: "x"})
	s.Apply(ShadowEdit{Kind: EditBackspace})
	c, _ := s.Snapshot(4, 4)
	if b.CaretID == c.CaretID {
		t.Fatal("intervening edits must change caret identity even when text returns")
	}
	version := s.Version()
	s.Invalidate()
	if s.Version() <= version {
		t.Fatal("invalidation did not advance version")
	}
	requireNoShadow(t, s)
}

func TestShadowTrackedReconciliationDoesNotUpgradeConfidence(t *testing.T) {
	s := focusedShadow()
	s.Reconcile(TextContext{Window: 42, FocusID: "field", Prefix: "validated partial", State: StateTracked, Confidence: 0.75})
	c := requireShadow(t, s, "validated partial", "")
	if c.State != StateTracked || c.Confidence != 0.75 {
		t.Fatalf("tracked caret upgraded to authoritative: %+v", c)
	}
}

func TestShadowProviderAndLocalMetadataPrivacy(t *testing.T) {
	s := focusedShadow()
	reconcileShadow(s, "prefix", "suffix", true, true)
	c := requireShadow(t, s, "prefix", "suffix")
	other := c
	other.ProviderID = "different-provider"
	if c.Fingerprint() == other.Fingerprint() {
		t.Fatal("provider switch did not invalidate fingerprint")
	}
	if got := ContextJSON(c); got != `{"prefix":"prefix","suffix":"suffix"}` {
		t.Fatalf("local tracking metadata leaked into model context: %s", got)
	}
}

func TestShadowConcurrentSnapshotsAndEdits(t *testing.T) {
	s := focusedShadow()
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				s.Apply(ShadowEdit{Kind: EditInsert, Text: "a"})
				s.Snapshot(20, 20)
				s.Version()
			}
		}()
	}
	workers.Wait()
	c, ok := s.Snapshot(1000, 1000)
	if !ok || len(c.Prefix) != 400 {
		t.Fatalf("concurrent edits lost characters: %d, %v", len(c.Prefix), ok)
	}
	s.Reset()
	requireNoShadow(t, s)
	if s.Apply(ShadowEdit{Kind: EditInsert, Text: "unfocused"}) {
		t.Fatal("accepted typing without a focused control")
	}
}
