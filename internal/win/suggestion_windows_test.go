//go:build windows && amd64

package win

import (
	"context"
	"testing"
	"typenext/internal/core"
)

func TestRequestUpdatesCannotFlashAfterFocusChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{requestID: 7, running: true, cancel: cancel}
	a.revision.Store(3)
	a.updateRequest(7, 3, 42, 42, func() {
		a.snapshot = &core.TextContext{Window: 42}
		a.overlayText = "generating"
	})
	if a.snapshot == nil || a.overlayText == "" {
		t.Fatal("current capture was not displayed")
	}

	// The model finishes after focus moves but before the foreground timer
	// fires. Neither its result nor an already queued partial may be displayed.
	a.updateRequest(7, 3, 42, 99, func() {
		t.Fatal("result from the previous foreground window was displayed")
	})
	a.updateRequest(7, 3, 42, 42, func() {
		t.Fatal("a queued update revived the canceled request")
	})
	if ctx.Err() != context.Canceled || a.running || a.candidateReady || a.snapshot != nil || a.overlayText != "" {
		t.Fatal("focus change did not cancel and clear the previous request")
	}
}

func TestSupersededRequestCannotCancelCurrentSuggestion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{requestID: 8, running: true, cancel: cancel}
	a.revision.Store(4)
	a.updateRequest(8, 4, 99, 99, func() {
		a.running = false
		a.candidateReady = true
		a.suggestion = "current result"
	})
	// An old completion/error can arrive after the replacement became ready.
	a.updateRequest(7, 3, 42, 99, func() {
		t.Fatal("a superseded callback was applied")
	})
	if ctx.Err() != nil || !a.candidateReady || a.suggestion != "current result" || a.requestID != 8 {
		t.Fatal("the old callback disturbed the current suggestion")
	}
}

func TestRequestUpdateRejectsInputRevisionAndMissingWindow(t *testing.T) {
	for _, tc := range []struct {
		name           string
		revision       uint64
		window, active uintptr
	}{
		{"input changed", 4, 42, 42},
		{"no foreground", 3, 42, 0},
		{"no request window", 3, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &app{requestID: 7, running: true, cancel: cancel}
			a.revision.Store(tc.revision)
			a.updateRequest(7, 3, tc.window, tc.active, func() {
				t.Fatal("an invalid request was displayed")
			})
			if ctx.Err() != context.Canceled || a.running {
				t.Fatal("the invalid request was left running")
			}
		})
	}
}

func TestAcceptedTabRepeatsStayConsumedUntilRelease(t *testing.T) {
	a := &app{tabAcceptHeld: true}
	for _, message := range []uintptr{0x100, 0x104, 0x100} {
		if !a.consumeAcceptedTab(0x09, message) {
			t.Fatal("accepted Tab repeat would reach the editor and cancel insertion")
		}
	}
	if a.consumeAcceptedTab('A', 0x100) {
		t.Fatal("acceptance swallowed unrelated typing")
	}
	// Even canceled insertion must finish swallowing its original key press.
	a.invalidate(false)
	if !a.consumeAcceptedTab(0x09, 0x100) || !a.consumeAcceptedTab(0x09, 0x101) {
		t.Fatal("acceptance lost ownership of Tab before release")
	}
	if a.tabAcceptHeld || a.consumeAcceptedTab(0x09, 0x100) || a.consumeAcceptedTab(0x09, 0x101) {
		t.Fatal("ordinary Tab was swallowed after the accepting press ended")
	}
}
