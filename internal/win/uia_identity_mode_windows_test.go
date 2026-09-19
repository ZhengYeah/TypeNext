//go:build windows && amd64

package win

import "testing"

func TestCaretIdentityOffsetModeRecoversWithoutSwitchingScheme(t *testing.T) {
	var identity caretIdentity
	defer identity.close()
	first := newTestPositionProvider(0, 4)
	id, err := identity.identifyInPattern(7, "search", &first.comObject, &first.caret.comObject)
	if err != nil || id != "uia-offset:4" || !identity.offsetBased || identity.rangeRef != nil {
		t.Fatalf("offset baseline not established: id=%q err=%v", id, err)
	}

	first.failure = "document unavailable"
	if next, err := identity.identifyInPattern(7, "search", &first.comObject, &first.caret.comObject); err == nil || next != "" {
		t.Fatalf("temporary offset failure changed identity scheme: id=%q err=%v", next, err)
	}
	if !identity.offsetBased || identity.rangeRef != nil || identity.window != 7 || identity.focus != "search" {
		t.Fatal("temporary offset failure changed the request baseline")
	}

	// A browser can provide entirely new ranges after a page refresh. Recovery
	// must retain the logical identity even when those ranges cannot be compared
	// with any ranges supplied by the previous provider generation.
	fresh := newTestPositionProvider(1000, 4)
	next, err := identity.identifyInPattern(7, "search", &fresh.comObject, &fresh.caret.comObject)
	if err != nil || next != id || !identity.offsetBased || identity.rangeRef != nil {
		t.Fatalf("offset recovery changed identity: old=%q new=%q err=%v", id, next, err)
	}
	first.assertOwnership(t, 0, 4)
	fresh.assertOwnership(t, 1000, 4)
}

func TestCaretIdentityRetainedModeLastsUntilNewRequest(t *testing.T) {
	var identity caretIdentity
	defer identity.close()
	p := newTestPositionProvider(100, 4)
	p.failure = "document unavailable"
	id, err := identity.identifyInPattern(7, "search", &p.comObject, &p.caret.comObject)
	if err != nil || id != "uia:1" || identity.offsetBased || identity.rangeRef == nil {
		t.Fatalf("retained baseline not established: id=%q err=%v", id, err)
	}

	p.failure = ""
	next, err := identity.identifyInPattern(7, "search", &p.comObject, &p.caret.comObject)
	if err != nil || next != id || identity.offsetBased || identity.rangeRef == nil {
		t.Fatalf("available offset support changed an active request's scheme: old=%q new=%q err=%v", id, next, err)
	}

	identity.close() // CaptureInitial starts the next request this way.
	if identity.rangeRef != nil || identity.offsetBased {
		t.Fatal("new request retained the previous identity scheme")
	}
	next, err = identity.identifyInPattern(7, "search", &p.comObject, &p.caret.comObject)
	if err != nil || next != "uia-offset:4" || !identity.offsetBased || identity.rangeRef != nil {
		t.Fatalf("new request did not adopt available offset support: id=%q err=%v", next, err)
	}
	p.assertOwnership(t, 100, 4)
}

func TestCaretIdentityNewRequestClearsPermanentlyStaleRange(t *testing.T) {
	for _, offsetAvailable := range []bool{false, true} {
		name := "retained fallback"
		if offsetAvailable {
			name = "stable offset"
		}
		t.Run(name, func(t *testing.T) {
			var identity caretIdentity
			defer identity.close()
			old := newTestPositionProvider(0, 4)
			old.failure = "document unavailable"
			id, err := identity.identifyInPattern(7, "search", &old.comObject, &old.caret.comObject)
			if err != nil {
				t.Fatal(err)
			}
			retained := identity.rangeRef

			// The old provider's retained ranges permanently reject comparisons
			// against ranges from the replacement provider.
			fresh := newTestPositionProvider(1000, 4)
			if !offsetAvailable {
				fresh.failure = "document unavailable"
			}
			if next, err := identity.identifyInPattern(7, "search", &fresh.comObject, &fresh.caret.comObject); err == nil || next != "" {
				t.Fatalf("stale provider range was accepted: id=%q err=%v", next, err)
			}
			if identity.rangeRef != retained || identity.offsetBased {
				t.Fatal("failed verification replaced its retained baseline")
			}

			identity.close() // A new request may establish a fresh baseline.
			next, err := identity.identifyInPattern(7, "search", &fresh.comObject, &fresh.caret.comObject)
			want := "uia:2"
			if offsetAvailable {
				want = "uia-offset:4"
			}
			if err != nil || next != want || next == id || identity.offsetBased != offsetAvailable {
				t.Fatalf("new request inherited the stale range: old=%q new=%q err=%v", id, next, err)
			}
			identity.close()
			old.assertOwnership(t, 0, 4)
			fresh.assertOwnership(t, 1000, 4)
		})
	}
}
