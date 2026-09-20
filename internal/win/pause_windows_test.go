//go:build windows && amd64

package win

import (
	"strings"
	"testing"
	"typenext/internal/core"
)

func TestPauseControlsShareStateAndCancelPendingSuggestion(t *testing.T) {
	a := newModelProfileTestApp(t)
	previousApp := currentApp
	currentApp = a
	t.Cleanup(func() { currentApp = previousApp })
	if got := windowText(a.controls[idPause]); got != "Pause" {
		t.Fatalf("initial pause button = %q", got)
	}

	for _, action := range []struct {
		name    string
		message uint32
		wp, lp  uintptr
		paused  bool
	}{
		{"pause button", 0x111, idPause, a.controls[idPause], true},
		{"resume hotkey", 0x312, 3, 0, false},
		{"pause tray action", 0x111, idPause, 0, true},
		{"resume button", 0x111, idPause, a.controls[idPause], false},
	} {
		canceled := false
		a.cancel = func() { canceled = true }
		a.running = true
		a.snapshot = &core.TextContext{Window: uint64(a.window)}
		a.suggestion = "pending suggestion"
		a.candidateReady = true
		a.autoArmed = true
		a.overlayText = a.suggestion
		revision := a.revision.Load()

		windowProc(a.window, action.message, action.wp, action.lp)

		if a.enabled == action.paused {
			t.Fatalf("enabled = %v after %s", a.enabled, action.name)
		}
		wantButton, wantStatus := "Pause", "resumed"
		if action.paused {
			wantButton, wantStatus = "Resume", "paused"
		}
		if got := windowText(a.controls[idPause]); got != wantButton {
			t.Errorf("%s: pause button = %q, want %q", action.name, got, wantButton)
		}
		if got := windowText(a.statusLabel); !strings.Contains(got, wantStatus) {
			t.Errorf("%s: status = %q, want %q", action.name, got, wantStatus)
		}
		if !canceled || a.cancel != nil || a.running || a.snapshot != nil || a.suggestion != "" || a.candidateReady || a.autoArmed || a.overlayText != "" {
			t.Fatalf("%s: toggling pause must cancel and clear the pending suggestion", action.name)
		}
		a.updateRequest(revision, a.window, a.window, func() {
			t.Errorf("%s: a stale request callback was accepted after toggling pause", action.name)
		})
		if action.paused {
			pausedRevision := a.revision.Load()
			a.request(true)
			if a.running || a.cancel != nil || a.revision.Load() != pausedRevision {
				t.Fatalf("%s: manual request started work while paused", action.name)
			}
			if got := windowText(a.statusLabel); !strings.Contains(got, "Resume") {
				t.Errorf("%s: paused request does not explain how to resume: %q", action.name, got)
			}
		}
	}
}
