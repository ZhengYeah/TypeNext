//go:build windows && amd64

package win

import (
	"context"
	"testing"
	"time"
	"typenext/internal/core"
)

func automaticTestApp() *app {
	cfg := core.DefaultConfig()
	cfg.Auto = true
	cfg.DebounceMS = 600
	return &app{cfg: cfg, enabled: true}
}

func TestAutomaticTypingKeys(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key, mods uint32
		want      bool
	}{
		{"letter", 'A', 0, true},
		{"capital", 'A', core.ModShift, true},
		{"question mark", 0xbf, core.ModShift, true},
		{"number", '1', 0, true},
		{"numpad digit", 0x61, 0, true},
		{"numpad decimal", 0x6e, 0, true},
		{"backspace", 0x08, 0, true},
		{"delete", 0x2e, 0, true},
		{"enter or IME commit", 0x0d, 0, true},
		{"shift enter", 0x0d, core.ModShift, true},
		{"IME process key", 0xe5, 0, true},
		{"space", 0x20, 0, true},
		{"control shortcut", 'A', core.ModCtrl, false},
		{"alt shortcut", 'A', core.ModAlt, false},
		{"Windows shortcut", 'A', core.ModWin, false},
		{"control shift shortcut", 'A', core.ModCtrl | core.ModShift, false},
		{"navigation", 0x25, 0, false},
		{"escape", 0x1b, 0, false},
		{"tab", 0x09, 0, false},
		{"function key", 0x70, 0, false},
		{"modifier", 0x10, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := automaticTypingKey(tc.key, tc.mods); got != tc.want {
				t.Fatalf("automaticTypingKey(%#x, %#x) = %v; want %v", tc.key, tc.mods, got, tc.want)
			}
		})
	}
}

func TestAutomaticPauseSurvivesShiftedFinalKeyAndRelease(t *testing.T) {
	a := automaticTestApp()
	a.keyboardActivity('A', 0, 42)
	previousActivity := a.lastActivity
	a.keyboardActivity(0xbf, core.ModShift, 42)
	if !a.autoArmed || a.lastActivity.Before(previousActivity) {
		t.Fatal("shifted final punctuation discarded the typing pause")
	}
	due := a.lastActivity.Add(600 * time.Millisecond)
	if a.automaticDue(due.Add(-time.Millisecond), false) {
		t.Fatal("suggestion became due before the typing pause elapsed")
	}
	if a.automaticDue(due, true) || !a.autoArmed {
		t.Fatal("holding Shift should postpone the request without discarding it")
	}
	if !a.automaticDue(due, false) {
		t.Fatal("releasing Shift after the pause did not permit the pending request")
	}
}

func TestAutomaticTypingAfterWindowSwitchSurvivesFirstPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := automaticTestApp()
	a.lastForeground, a.running, a.cancel = 10, true, cancel
	a.snapshot = &core.TextContext{Window: 10}
	// The user switches apps and types before the next 150 ms foreground poll.
	a.keyboardActivity('A', 0, 42)
	activity, revision := a.lastActivity, a.revision.Load()
	a.observeForeground(42)
	if ctx.Err() != context.Canceled || a.snapshot != nil || a.running {
		t.Fatal("typing in the new window did not cancel the old request")
	}
	if a.revision.Load() != revision || !a.lastActivity.Equal(activity) || !a.automaticDue(activity.Add(600*time.Millisecond), false) {
		t.Fatal("the first foreground poll erased typing in the new window")
	}
	// A subsequent app switch without typing must still discard the pending arm.
	a.observeForeground(99)
	if a.autoArmed || a.automaticDue(activity.Add(time.Second), false) {
		t.Fatal("pending typing followed focus into an unrelated app")
	}
}

func TestAutomaticNonTypingActivityCancelsPendingPause(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key, mods uint32
		window    uintptr
	}{
		{"navigation", 0x25, 0, 42},
		{"command shortcut", 'S', core.ModCtrl, 42},
		{"missing foreground", 'A', 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := automaticTestApp()
			a.keyboardActivity('A', 0, 42)
			a.keyboardActivity(tc.key, tc.mods, tc.window)
			if a.autoArmed || a.automaticDue(a.lastActivity.Add(time.Second), false) {
				t.Fatal("non-typing activity left an automatic request armed")
			}
		})
	}
}

func TestAutomaticPauseHonorsAppAndRemoteGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*app)
	}{
		{"paused", func(a *app) { a.enabled = false }},
		{"API dialog", func(a *app) { a.apiWindow = 7 }},
		{"automatic disabled", func(a *app) { a.cfg.Auto = false }},
		{"existing request", func(a *app) { a.running = true }},
		{"remote automatic permission absent", func(a *app) {
			a.cfg.Provider, a.cfg.Endpoint, a.cfg.AllowRemote = "openai-compatible", "https://example.com/v1", true
			u, err := a.cfg.RequestURL()
			if err != nil {
				t.Fatal(err)
			}
			a.cfg.RemoteConsent = u.String()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := automaticTestApp()
			a.keyboardActivity('A', 0, 42)
			tc.change(a)
			if a.automaticDue(a.lastActivity.Add(time.Second), false) {
				t.Fatal("automatic request bypassed an application or permission gate")
			}
		})
	}
}
