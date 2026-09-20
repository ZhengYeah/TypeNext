//go:build windows && amd64

package win

import (
	"errors"
	"testing"
	"typenext/internal/core"
)

func seededContextWorker() *contextWorker {
	w := newContextWorker(&uiaWorker{jobs: make(chan func(*comObject), 1)})
	w.enabled = true
	w.cfg = core.DefaultConfig()
	w.shadow.Focus(42, "field-a")
	w.shadow.Apply(core.ShadowEdit{Kind: core.EditInsert, Text: "hello"})
	return w
}

func unavailableContext() error {
	return &contextUnavailableError{
		Target: core.TextContext{Window: 42, FocusID: "field-a", Process: "editor.exe", X: 10, Y: 20, CaretHeight: 16, PositionSource: "field corner"},
		Reason: "no UIA text provider",
	}
}

func TestContextFallbackRequiresCapabilityFailure(t *testing.T) {
	for _, reason := range []string{"password/protected field", "selected text", "unapproved process", "read-only text"} {
		t.Run(reason, func(t *testing.T) {
			w := seededContextWorker()
			if _, err := w.resolveRead(core.TextContext{}, errors.New(reason), w.cfg, 0, 0); err == nil {
				t.Fatal("unsafe failure became keyboard context")
			}
			if _, ok := w.shadow.Snapshot(100, 100); ok {
				t.Fatal("blocked read retained usable history")
			}
		})
	}
	w := seededContextWorker()
	got, err := w.resolveRead(core.TextContext{}, unavailableContext(), w.cfg, 0, 0)
	if err != nil || got.Prefix != "hello" || got.State != core.StateTracked || !got.Partial || got.Confidence >= 1 {
		t.Fatalf("tracked fallback: %+v, %v", got, err)
	}
}

func TestContextLateReadCannotRepopulateInvalidatedHistory(t *testing.T) {
	w := seededContextWorker()
	w.InvalidateTracking()
	exact := core.TextContext{Window: 42, FocusID: "field-a", Prefix: "old document", Source: "uia-text", State: core.StateSynchronized}
	if _, err := w.resolveRead(exact, nil, w.cfg, 0, 0); err == nil {
		t.Fatal("accepted stale in-flight snapshot")
	}
	if _, ok := w.shadow.Snapshot(100, 100); ok {
		t.Fatal("late read restored discarded text")
	}
}

func TestContextNewFieldCannotReuseOldHistory(t *testing.T) {
	w := seededContextWorker()
	failure := unavailableContext().(*contextUnavailableError)
	failure.Target.FocusID = "field-b"
	if _, err := w.resolveRead(core.TextContext{}, failure, w.cfg, 0, 0); err == nil {
		t.Fatal("reused field-a history in field-b")
	}
}

func TestContextExactReadResynchronizesSameField(t *testing.T) {
	w := seededContextWorker()
	snapshot := core.TextContext{Window: 42, FocusID: "field-a", ProviderID: "text-provider", CaretID: "offset:7", Prefix: "changed", Suffix: " text", State: core.StateSynchronized, Confidence: 1}
	got, err := w.resolveRead(snapshot, nil, w.cfg, 0, 0)
	if err != nil || got.Fingerprint() != snapshot.Fingerprint() {
		t.Fatalf("exact context: %+v %v", got, err)
	}
	shadow, ok := w.shadow.Snapshot(100, 100)
	if !ok || shadow.Prefix != snapshot.Prefix || shadow.Suffix != snapshot.Suffix {
		t.Fatalf("shadow not reconciled: %+v", shadow)
	}
}

func TestContextDisabledTrackingCannotFallback(t *testing.T) {
	w := seededContextWorker()
	w.cfg.KeyboardTracking = false
	if _, err := w.resolveRead(core.TextContext{}, unavailableContext(), w.cfg, 0, 0); err == nil {
		t.Fatal("disabled tracking supplied a context")
	}
}

func TestContextQueueOverflowDiscardsIncompleteHistory(t *testing.T) {
	w := seededContextWorker()
	w.uia.jobs <- func(*comObject) {}
	w.ObserveKey(keyboardContextEvent{})
	if _, ok := w.shadow.Snapshot(100, 100); ok {
		t.Fatal("lost key left plausible history")
	}
	if w.epoch == 0 {
		t.Fatal("queued old events can revive history")
	}
}

func TestValueReconciliationRequiresUniqueTrackedSpan(t *testing.T) {
	tracked := core.TextContext{Prefix: "naïve", Suffix: " café", CaretID: "shadow:1", ProviderID: "keyboard-history", State: core.StateTracked}
	got, ok := reconcileValue(tracked, "A naïve café end", 100, 100)
	if !ok || got.Prefix != "A naïve" || got.Suffix != " café end" || got.State != core.StateTracked || got.Confidence >= 1 {
		t.Fatalf("unexpected merge: %+v %v", got, ok)
	}
	for _, value := range []string{"changed text", "naïve café then naïve café"} {
		if _, ok := reconcileValue(tracked, value, 100, 100); ok {
			t.Fatalf("guessed caret in %q", value)
		}
	}
	got, ok = reconcileValue(tracked, "A naïve café end", 3, 2)
	if !ok || got.Prefix != "ïve" || got.Suffix != " c" || got.KnownBefore || got.KnownAfter {
		t.Fatalf("bounds incorrect: %+v", got)
	}
	if _, ok := reconcileValue(core.TextContext{}, "text", 100, 100); ok {
		t.Fatal("value alone invented a caret")
	}
}

func TestValueMismatchInvalidatesShadow(t *testing.T) {
	w := seededContextWorker()
	e := unavailableContext().(*contextUnavailableError)
	e.ValueKnown, e.Value = true, "different value"
	if _, err := w.resolveRead(core.TextContext{}, e, w.cfg, 0, 0); err == nil {
		t.Fatal("value mismatch accepted")
	}
	if _, ok := w.shadow.Snapshot(100, 100); ok {
		t.Fatal("mismatching shadow retained")
	}
}

func TestDeadKeyQuarantineSurvivesNavigationAndRefresh(t *testing.T) {
	w := seededContextWorker()
	if !w.suppressDeadKey(core.ShadowEdit{}, false, true) {
		t.Fatal("dead key was not quarantined")
	}
	if !w.suppressDeadKey(core.ShadowEdit{Kind: core.EditLeft}, true, false) || !w.deadKeyPending {
		t.Fatal("navigation consumed a pending dead key")
	}
	exact := core.TextContext{Window: 42, FocusID: "field-a", Prefix: "hello"}
	if _, err := w.resolveRead(exact, nil, w.cfg, 0, 0); err != nil {
		t.Fatal(err)
	}
	if !w.deadKeyPending {
		t.Fatal("UIA refresh consumed the target's pending dead key")
	}
	if !w.suppressDeadKey(core.ShadowEdit{Kind: core.EditInsert, Text: "e"}, true, false) {
		t.Fatal("unaccented guess was admitted")
	}
	if w.suppressDeadKey(core.ShadowEdit{Kind: core.EditInsert, Text: "x"}, true, false) {
		t.Fatal("ordinary typing did not recover after quarantined commit")
	}
}

func TestStaleQueuedReadsDoNotCallProviders(t *testing.T) {
	w := seededContextWorker()
	w.PollFocus(w.cfg, 0, true)
	job := <-w.uia.jobs
	w.ResetTracking()
	job(nil) // A stale poll must return before touching the COM object.
	if w.polling {
		t.Fatal("stale poll left polling blocked")
	}
	if _, err := w.readResolved(nil, w.cfg, 0, 0, 0); err == nil {
		t.Fatal("stale read accepted")
	}
}
