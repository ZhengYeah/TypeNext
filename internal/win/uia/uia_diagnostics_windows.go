//go:build windows && amd64

package uia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"typenext/internal/core"
	"unicode"
	"unicode/utf16"
	"unsafe"
)

// Inspection deliberately excludes Name, Value, ProviderDescription, and
// GetText: these can contain document contents even on non-edit controls.
type uiaInspection struct{ lines []string }

func (r *uiaInspection) add(label, value string) {
	r.lines = append(r.lines, label+": "+value)
}

func (r *uiaInspection) finish(err error) (string, error) {
	if err != nil {
		r.add("Stopped at", err.Error())
	}
	r.lines = append(r.lines, "", "Metadata only. No field text, names, or values were read, saved, or sent to the model.")
	return strings.Join(r.lines, "\n"), err
}

func (w *Worker) Inspect(ctx context.Context, c core.Config, padWindow uintptr) (string, error) {
	type result struct {
		report string
		err    error
	}
	reply := make(chan result, 1)
	job := func(a *comObject) {
		report, err := w.inspectUIAContext(ctx, a, c, padWindow)
		reply <- result{report, err}
	}
	select {
	case w.jobs <- job:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case r := <-reply:
		return r.report, r.err
	case <-ctx.Done():
		return "", errors.New("accessibility inspection timed out; the provider may not be responding")
	}
}

func (w *Worker) inspectUIAContext(ctx context.Context, a *comObject, c core.Config, padWindow uintptr) (string, error) {
	r := &uiaInspection{}
	if err := ctx.Err(); err != nil {
		return r.finish(err)
	}
	window := w.host.Foreground()
	_, process, err := w.host.ProcessOf(window)
	if err != nil {
		return r.finish(fmt.Errorf("foreground process: %w", err))
	}
	r.add("Application", process)
	if window != padWindow && !c.Allows(process) {
		return r.finish(fmt.Errorf("application approval: %s is not approved; add its executable name in Settings", process))
	}
	if w.host.Composing(window) {
		return r.finish(errors.New("IME composition: finish the current composition first"))
	}
	if strings.EqualFold(process, "powerpnt.exe") {
		r.add("Adapter", "PowerPoint object model")
		r.add("Result", "Dedicated adapter selected; slide text and selection were not accessed by this inspection")
		return r.finish(nil)
	}
	r.add("Adapter", "UI Automation")
	var el *comObject
	hr := comCall(a, 8, uintptr(unsafe.Pointer(&el)))
	if failed(hr) || el == nil {
		release(el)
		return r.finish(fmt.Errorf("GetFocusedElement: unavailable (0x%08x)", uint32(hr)))
	}
	defer release(el)
	if err = inspectUIAElement(ctx, a, el, r); err != nil {
		return r.finish(err)
	}
	if w.host.Foreground() != window {
		return r.finish(errors.New("focus verification: foreground window changed during inspection"))
	}
	r.add("Result", "Caret and editability checks passed; text reading and insertion were not exercised")
	return r.finish(nil)
}

func inspectUIAElement(ctx context.Context, a, el *comObject, r *uiaInspection) error {
	if err := inspectElementMetadata(ctx, el, r); err != nil {
		return err
	}
	target, err := resolveTextTarget(ctx, a, el)
	if err != nil {
		return fmt.Errorf("text target resolution: %w", err)
	}
	defer target.close()
	r.add("Text source", target.via)
	if target.boundary != nil {
		r.add("Read scope", "TextChild enclosing range")
	} else {
		r.add("Read scope", "Focused control")
	}
	selection, err := scalar(target.pattern, 8) // IUIAutomationTextPattern::get_SupportedTextSelection
	if err != nil {
		r.add("SupportedTextSelection", err.Error())
	} else {
		names := map[int32]string{0: "None", 1: "Single", 2: "Multiple"}
		r.add("SupportedTextSelection", fmt.Sprintf("%s (%d)", names[selection], selection))
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	caret, err := collapsedSelection(target.pattern)
	if err != nil {
		return fmt.Errorf("TextPattern.GetSelection / collapsed caret: %w", err)
	}
	defer release(caret)
	r.add("Selection", "One collapsed range")
	if err = rangeWithinBoundary(caret, target.boundary); err != nil {
		return fmt.Errorf("caret scope verification: %w", err)
	}
	source, err := caretEditability(a, el, caret)
	if err != nil {
		return fmt.Errorf("caret editability: %w", err)
	}
	r.add("Editability evidence", source)
	if err = ctx.Err(); err != nil {
		return err
	}
	var current *comObject
	hr := comCall(a, 8, uintptr(unsafe.Pointer(&current)))
	defer release(current)
	if failed(hr) || current == nil {
		return fmt.Errorf("GetFocusedElement verification: unavailable (0x%08x)", uint32(hr))
	}
	if err = verifyTextTarget(ctx, a, current, target, caret); err != nil {
		return fmt.Errorf("focus and caret verification: %w", err)
	}
	return ctx.Err()
}

func inspectElementMetadata(ctx context.Context, el *comObject, r *uiaInspection) error {
	// Do not request string properties or patterns when protection is unknown.
	for _, flag := range []struct {
		name string
		slot int
		want bool
	}{
		{"CurrentIsPassword", 35, false},
		{"CurrentHasKeyboardFocus", 26, true},
		{"CurrentIsEnabled", 28, true},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := scalar(el, flag.slot)
		if err != nil {
			r.add(flag.name, "Unknown")
			return fmt.Errorf("%s: %w", flag.name, err)
		}
		r.add(flag.name, fmt.Sprint(value != 0))
		if (value != 0) != flag.want {
			return fmt.Errorf("%s: control does not meet accessibility requirements", flag.name)
		}
	}
	for _, property := range []struct {
		name string
		slot int
	}{
		{"Provider process ID", 20},
		{"Control type", 21},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := scalar(el, property.slot)
		if err != nil {
			r.add(property.name, err.Error())
		} else {
			r.add(property.name, fmt.Sprint(value))
		}
	}
	for _, property := range []struct {
		name string
		slot int
	}{
		{"Framework", 40},
		{"Class", 30},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.add(property.name, diagnosticMetadataString(el, property.slot))
	}
	// Availability properties are metadata only. Never query Value.Value.
	// IDs: learn.microsoft.com/windows/win32/winauto/uiauto-control-pattern-availability-propids
	for _, property := range []struct {
		name string
		id   int
	}{
		{"TextPattern available", 30040},
		{"TextPattern2 available", 30119},
		{"TextChildPattern available", 30136},
		{"ValuePattern available", 30043},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.add(property.name, diagnosticPatternAvailable(el, property.id))
	}
	return ctx.Err()
}

func diagnosticPatternAvailable(el *comObject, id int) string {
	var v variant
	// GetCurrentPropertyValueEx(ignoreDefaultValue=true) distinguishes missing
	// properties from an explicit false value, without requesting any pattern.
	hr := comCall(el, 11, uintptr(id), 1, uintptr(unsafe.Pointer(&v)))
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	if failed(hr) {
		return fmt.Sprintf("Unavailable (0x%08x)", uint32(hr))
	}
	if v.VT != 11 {
		return "Unknown (provider did not return a boolean)"
	}
	return fmt.Sprint(*(*int16)(unsafe.Pointer(&v.Data[0])) != 0)
}

func diagnosticMetadataString(el *comObject, slot int) string {
	// The allowlist prevents accidental use for Name or ProviderDescription.
	if slot != 30 && slot != 40 {
		return "Unavailable (not a metadata property)"
	}
	var b *uint16
	hr := comCall(el, slot, uintptr(unsafe.Pointer(&b)))
	if b != nil {
		defer pSysFreeString.Call(uintptr(unsafe.Pointer(b)))
	}
	if failed(hr) {
		return fmt.Sprintf("Unavailable (0x%08x)", uint32(hr))
	}
	if b == nil {
		return "(empty)"
	}
	n, _, _ := pSysStringLen.Call(uintptr(unsafe.Pointer(b)))
	truncated := n > 256
	if truncated {
		n = 256
	}
	value := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, string(utf16.Decode(unsafe.Slice(b, int(n)))))
	if truncated {
		value += "..."
	}
	return value
}
