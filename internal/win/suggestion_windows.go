//go:build windows && amd64

package win

import (
	"context"
	"errors"
	"time"
	"typenext/internal/core"
)

var errSuggestionContextChanged = errors.New("textbox or caret changed during generation")

// Retry only unavailable reads, within the caller's existing deadline.
// A real context mismatch is final, and no retry makes another model request.
func verifySuggestionContext(ctx context.Context, expected core.TextContext, capture func(context.Context) (core.TextContext, error)) (core.TextContext, error) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return core.TextContext{}, ctx.Err()
		}
		var fresh core.TextContext
		fresh, err = capture(ctx)
		if ctx.Err() != nil {
			return core.TextContext{}, ctx.Err()
		}
		if err == nil {
			if fresh.Fingerprint() != expected.Fingerprint() {
				return core.TextContext{}, errSuggestionContextChanged
			}
			return fresh, nil
		}
		if attempt < 2 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return core.TextContext{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return core.TextContext{}, err
}

// Leave failures visible at the original anchor, with acceptance disabled.
// Invalidate first so queued streaming callbacks cannot revive the old result.
func (a *app) failSuggestion(snapshot core.TextContext, model, message string) {
	a.invalidate(false)
	footer := "Esc to dismiss"
	if key := a.hotkeyLabel(core.HotkeySuggest); key != "off" && key != "unavailable" {
		footer = key + " to retry · " + footer
	}
	a.showOverlay(snapshot, model, message, footer)
	a.setStatus(message)
}

func (a *app) dismissSuggestion() bool {
	if !a.candidateReady && !a.running && a.overlayText == "" {
		return false
	}
	a.invalidate(false)
	return true
}
