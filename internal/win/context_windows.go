//go:build windows && amd64

package win

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"typenext/internal/core"
)

// contextWorker serializes UIA reads and keyboard reconstruction on the existing
// COM thread. Hooks only enqueue bounded metadata; they never call a provider.
// mu makes enqueue order and input revisions one transaction, and protects
// invalidation against an in-flight provider returning an obsolete snapshot.
type contextWorker struct {
	uia            *uiaWorker
	mu             sync.Mutex
	shadow         *core.ShadowEditor
	cfg            core.Config
	pad            uintptr
	enabled        bool
	epoch, input   uint64
	polling        bool
	lastPoll       time.Time
	admitted       core.TextContext
	admittedNative uintptr
	deadKeyPending bool
}

func newContextWorker(uia *uiaWorker) *contextWorker {
	return &contextWorker{uia: uia, shadow: core.NewShadowEditor(12000)}
}

func (w *contextWorker) ResetTracking() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epoch++
	w.input++
	w.shadow.Reset()
	w.admitted = core.TextContext{}
	w.admittedNative = 0
	w.deadKeyPending = false
}

func (w *contextWorker) InvalidateTracking() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epoch++
	w.input++
	w.shadow.Invalidate()
	w.admitted = core.TextContext{}
	w.admittedNative = 0
}

func sameNativeKeyboardTarget(e keyboardContextEvent) bool {
	if e.Window == 0 || e.Focus == 0 || foreground() != e.Window {
		return false
	}
	g, ok := guiInfo(e.Window)
	thread, _, _ := pGetWindowThreadProcessId.Call(e.Window, 0)
	layout, _, _ := pGetKeyboardLayout.Call(thread)
	return ok && g.Focus == e.Focus && uint32(thread) == e.Thread && layout == e.Layout
}

func (w *contextWorker) ObserveKey(e keyboardContextEvent) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled || !w.cfg.KeyboardTracking {
		return
	}
	w.input++
	epoch, cfg, pad := w.epoch, w.cfg, w.pad
	admittedID := w.admitted.FocusID
	if w.admitted.Window != uint64(e.Window) || w.admittedNative != e.Focus {
		admittedID = ""
	}
	job := func(a *comObject) {
		w.mu.Lock()
		current := epoch == w.epoch && w.enabled
		w.mu.Unlock()
		if !current {
			return
		}
		if time.Since(e.At) > time.Second || !sameNativeKeyboardTarget(e) {
			w.InvalidateTracking()
			return
		}
		target, err := probeTrackingTarget(a, cfg, pad)
		// Never translate a key until the live focused field passes admission.
		if err != nil || target.Window != uint64(e.Window) || !sameNativeKeyboardTarget(e) || composing(e.Window) {
			w.InvalidateTracking()
			return
		}
		edit, trustworthy, deadKey := translateKeyboardEvent(e)
		w.mu.Lock()
		defer w.mu.Unlock()
		if epoch != w.epoch || !w.enabled {
			return
		}
		w.shadow.Focus(target.Window, target.FocusID)
		w.admitted, w.admittedNative = target, e.Focus
		quarantined := w.suppressDeadKey(edit, trustworthy, deadKey)
		// Bind the event to the field admitted before it occurred. An event
		// cannot be retroactively assigned to a newly focused UIA descendant.
		if admittedID == "" || admittedID != target.FocusID || quarantined {
			w.shadow.Invalidate()
			return
		}
		if !trustworthy {
			w.shadow.Invalidate()
			return
		}
		w.shadow.Apply(edit)
	}
	select {
	case w.uia.jobs <- job:
	default:
		// A slow/hung provider must not stall the input hook or preserve a
		// plausible-looking buffer after losing even one edit.
		w.epoch++
		w.shadow.Invalidate()
	}
}

// Called under mu. The dead-key buffer belongs to the target thread, so a
// UIA refresh or navigation does not prove that its next glyph is uncomposed.
func (w *contextWorker) suppressDeadKey(edit core.ShadowEdit, trustworthy, deadKey bool) bool {
	if deadKey {
		w.deadKeyPending = true
	}
	if !w.deadKeyPending {
		return false
	}
	if !deadKey && trustworthy && edit.Kind == core.EditInsert {
		w.deadKeyPending = false
	}
	return true
}

// Polling replaces the shadow only when no input arrived during the read. It
// also discovers focus changes inside the same top-level window.
func (w *contextWorker) PollFocus(cfg core.Config, pad uintptr, enabled bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cfg, w.pad, w.enabled = cfg, pad, enabled
	if !enabled || !cfg.KeyboardTracking {
		w.epoch++
		w.shadow.Reset()
		w.admitted = core.TextContext{}
		return
	}
	if w.polling || time.Since(w.lastPoll) < 300*time.Millisecond {
		return
	}
	epoch, input := w.epoch, w.input
	w.polling = true
	job := func(a *comObject) {
		w.mu.Lock()
		if epoch != w.epoch || !w.enabled {
			w.polling = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		snapshot, err := readContext(a, cfg, pad, &w.uia.caret)
		snapshot = normalizedContext(snapshot)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.polling = false
		w.lastPoll = time.Now()
		if epoch != w.epoch || input != w.input || !w.enabled {
			return
		}
		if err == nil {
			w.admitLocked(snapshot)
			w.shadow.Focus(snapshot.Window, snapshot.FocusID)
			w.shadow.Reconcile(snapshot)
			return
		}
		var unavailable *contextUnavailableError
		if errors.As(err, &unavailable) {
			w.admitLocked(unavailable.Target)
			w.shadow.Focus(unavailable.Target.Window, unavailable.Target.FocusID)
			return
		}
		// Protected, readonly, selected, unapproved, or changed fields are not
		// reasons to fall back. Their history is discarded.
		w.shadow.Reset()
		w.admitted = core.TextContext{}
	}
	select {
	case w.uia.jobs <- job:
	default:
		w.polling = false
		w.epoch++
		w.shadow.Invalidate()
	}
}

// Called under mu after a live provider check on the COM thread.
func (w *contextWorker) admitLocked(target core.TextContext) {
	if foreground() != uintptr(target.Window) {
		return
	}
	g, ok := guiInfo(uintptr(target.Window))
	if !ok || g.Focus == 0 {
		return
	}
	target.Prefix, target.Suffix = "", ""
	w.admitted, w.admittedNative = target, g.Focus
}

func (w *contextWorker) CaptureInitial(ctx context.Context, c core.Config, pad uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, pad, true)
}
func (w *contextWorker) Capture(ctx context.Context, c core.Config, pad uintptr) (core.TextContext, error) {
	return w.capture(ctx, c, pad, false)
}

func (w *contextWorker) capture(ctx context.Context, cfg core.Config, pad uintptr, initial bool) (core.TextContext, error) {
	type response struct {
		snapshot core.TextContext
		err      error
	}
	reply := make(chan response, 1)
	w.mu.Lock()
	epoch, input := w.epoch, w.input
	job := func(a *comObject) {
		if ctx.Err() != nil {
			reply <- response{err: ctx.Err()}
			return
		}
		if initial {
			w.uia.caret.close()
		}
		snapshot, err := w.readResolved(a, cfg, pad, epoch, input)
		reply <- response{snapshot, err}
	}
	select {
	case w.uia.jobs <- job:
		w.mu.Unlock()
	default:
		w.mu.Unlock()
		return core.TextContext{}, errors.New("text context reader is busy; try again after the application responds")
	}
	select {
	case r := <-reply:
		return r.snapshot, r.err
	case <-ctx.Done():
		return core.TextContext{}, ctx.Err()
	}
}

func (w *contextWorker) readResolved(a *comObject, cfg core.Config, pad uintptr, epoch, input uint64) (core.TextContext, error) {
	w.mu.Lock()
	current := epoch == w.epoch && input == w.input
	w.mu.Unlock()
	if !current {
		return core.TextContext{}, errors.New("input changed before context read; request again")
	}
	snapshot, err := readContext(a, cfg, pad, &w.uia.caret)
	return w.resolveRead(snapshot, err, cfg, epoch, input)
}

func normalizedContext(snapshot core.TextContext) core.TextContext {
	if snapshot.Source == "" && strings.HasPrefix(snapshot.PositionSource, "PowerPoint") {
		snapshot.Source = "powerpoint"
	}
	if snapshot.State == "" {
		snapshot.State, snapshot.Confidence = core.StateSynchronized, 1
	}
	return snapshot
}

func (w *contextWorker) resolveRead(snapshot core.TextContext, err error, cfg core.Config, epoch, input uint64) (core.TextContext, error) {
	snapshot = normalizedContext(snapshot)
	w.mu.Lock()
	defer w.mu.Unlock()
	if epoch != w.epoch || input != w.input {
		return core.TextContext{}, errors.New("input changed while reading context; request again")
	}
	if err == nil {
		if cfg.KeyboardTracking {
			w.admitLocked(snapshot)
			w.shadow.Focus(snapshot.Window, snapshot.FocusID)
			w.shadow.Reconcile(snapshot)
		}
		return snapshot, nil
	}
	var unavailable *contextUnavailableError
	if !cfg.KeyboardTracking || !errors.As(err, &unavailable) {
		w.shadow.Reset()
		w.admitted = core.TextContext{}
		return core.TextContext{}, err
	}
	target := unavailable.Target
	w.admitLocked(target)
	w.shadow.Focus(target.Window, target.FocusID)
	tracked, ok := w.shadow.Snapshot(cfg.PrefixChars, cfg.SuffixChars)
	if !ok {
		return core.TextContext{}, fmt.Errorf("%w; keyboard context is unknown or uncertain — type a few words to rebuild it", err)
	}
	tracked.State = core.StateTracked
	tracked.Confidence = 0.7
	tracked.Partial = true
	tracked.Process = target.Process
	tracked.X, tracked.Y, tracked.CaretHeight = target.X, target.Y, target.CaretHeight
	tracked.PositionSource = "Keyboard context / " + target.PositionSource
	// A Value/Legacy value supplies text but never a caret. Only a unique
	// observed tracked span may locate its own insertion point within it.
	if unavailable.ValueKnown {
		merged, valid := reconcileValue(tracked, unavailable.Value, cfg.PrefixChars, cfg.SuffixChars)
		if !valid {
			w.shadow.Invalidate()
			return core.TextContext{}, errors.New("accessible value disagrees with tracked typing; context discarded")
		}
		tracked = merged
		if target.Source == "uia-legacy" {
			tracked.Source = "uia-legacy+keyboard"
		}
	}
	return tracked, nil
}

func reconcileValue(tracked core.TextContext, value string, prefixLimit, suffixLimit int) (core.TextContext, bool) {
	span := tracked.Prefix + tracked.Suffix
	if span == "" {
		return core.TextContext{}, false
	}
	start := strings.Index(value, span)
	if start < 0 || start != strings.LastIndex(value, span) {
		return core.TextContext{}, false
	}
	caret := start + len(tracked.Prefix)
	tracked.Prefix = core.Tail(value[:caret], prefixLimit)
	tracked.Suffix = core.Head(value[caret:], suffixLimit)
	tracked.Source = "uia-value+keyboard"
	tracked.State = core.StateTracked
	tracked.Confidence = 0.8
	tracked.Partial = true // text is read, but the caret still comes from tracking
	tracked.KnownBefore = len([]rune(value[:caret])) <= prefixLimit
	tracked.KnownAfter = len([]rune(value[caret:])) <= suffixLimit
	return tracked, true
}

func (w *contextWorker) Insert(ctx context.Context, cfg core.Config, pad uintptr, expected core.TextContext, text string, revision *atomic.Uint64, expectedRevision uint64, acceptVK uint32) error {
	if !waitRelease(1200*time.Millisecond, uintptr(acceptVK)) {
		return errors.New("release the shortcut keys and try accepting again")
	}
	reply := make(chan error, 1)
	w.mu.Lock()
	epoch, input := w.epoch, w.input
	job := func(a *comObject) {
		if ctx.Err() != nil {
			reply <- ctx.Err()
			return
		}
		if revision.Load() != expectedRevision {
			reply <- errors.New("typing or focus changed; suggestion discarded")
			return
		}
		actual, err := w.readResolved(a, cfg, pad, epoch, input)
		if err != nil {
			reply <- err
			return
		}
		if actual.Fingerprint() != expected.Fingerprint() {
			reply <- errors.New("textbox/caret changed; request a new suggestion")
			return
		}
		if composing(uintptr(expected.Window)) {
			reply <- errors.New("finish the IME composition before accepting")
			return
		}
		w.mu.Lock()
		changed := epoch != w.epoch || input != w.input
		w.mu.Unlock()
		if changed || ctx.Err() != nil || revision.Load() != expectedRevision || foreground() != uintptr(expected.Window) || modifiersDown() {
			reply <- errors.New("input context changed; insertion cancelled")
			return
		}
		safe := core.CleanSuggestion(text, core.TextContext{}, cfg.MaxSuggestionChars)
		if strings.TrimSpace(safe) == "" {
			reply <- errors.New("empty suggestion")
			return
		}
		err = sendUnicode(safe)
		// SendInput confirms queuing, not an application's accepted text. Let
		// UIA resynchronize or rebuild from later typing, avoiding duplicates
		// and unverified text after a partial injection.
		w.InvalidateTracking()
		reply <- err
	}
	select {
	case w.uia.jobs <- job:
		w.mu.Unlock()
	default:
		w.mu.Unlock()
		return errors.New("text context reader is busy; insertion cancelled")
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
