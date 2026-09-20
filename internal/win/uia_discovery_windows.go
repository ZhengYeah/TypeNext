//go:build windows && amd64

package win

import (
	"errors"
	"fmt"
	"typenext/internal/core"
	"unicode/utf16"
	"unsafe"
)

const (
	maxProviderParents = 3
	maxProviderDepth   = 3
	maxProviderNodes   = 24
	maxBoundaryParents = 16
	maxFallbackValue   = 8192 // UTF-16 units; never accept a truncated value as complete.
)

var (
	iidValue          = guid{0xa94cd8b1, 0x0844, 0x4cd6, [8]byte{0x9d, 0x2d, 0x64, 0x05, 0x37, 0xab, 0x39, 0xe9}}
	iidLegacy         = guid{0x828055ad, 0x355b, 0x4435, [8]byte{0x86, 0xd5, 0x3b, 0x51, 0xc1, 0x4a, 0x9b, 0x1b}}
	errNoTextProvider = errors.New("no associated text provider with a reliable caret")
)

// Only this typed failure allows keyboard fallback. Protection, selection,
// read-only, app approval and focus failures must never become this error.
// Value is optional reconciliation input, never a guessed prefix/caret.
type contextUnavailableError struct {
	Target     core.TextContext
	Value      string
	ValueKnown bool
	Reason     string
}

func (e *contextUnavailableError) Error() string { return e.Reason }

type uiaFocusProbe struct {
	a, focused, walker *comObject
	target             core.TextContext
	control            int32
	trackingAllowed    bool
}

func (p *uiaFocusProbe) close() { release(p.focused); release(p.walker) }

func safeElement(el *comObject, requireFocus bool) error {
	// Never read Name/Value/Text until these properties are established.
	password, err := scalar(el, 35)
	if err != nil || password != 0 {
		return errors.New("password/protected field: text access disabled")
	}
	enabled, err := scalar(el, 28)
	if err != nil || enabled == 0 {
		return errors.New("textbox is not enabled")
	}
	if requireFocus {
		focused, err := scalar(el, 26)
		if err != nil || focused == 0 {
			return errors.New("textbox does not have keyboard focus")
		}
	}
	return nil
}

func writableElement(el *comObject) error {
	if pat, err := pattern(el, 10002, iidValue); err == nil {
		readonly, e := scalar(pat, 5)
		release(pat)
		if e != nil || readonly != 0 {
			return errors.New("read-only text or unknown editing permission: completion disabled")
		}
	}
	if pat, err := pattern(el, 10018, iidLegacy); err == nil {
		state, e := scalar(pat, 11)
		release(pat)
		if e != nil || uint32(state)&(0x20000000|0x40|0x1) != 0 { // protected, read-only, unavailable
			return errors.New("protected/read-only legacy field: completion disabled")
		}
	}
	return nil
}

// Editable capabilities take precedence over ControlType. These explicit UI
// action controls are unsuitable only when no text interface is exposed.
func trackingControlAllowed(control int32, textCapability bool) bool {
	if textCapability {
		return true
	}
	switch control {
	case 50000, 50002, 50005, 50006, 50007, 50008, 50009, 50010,
		50011, 50012, 50013, 50014, 50015, 50016, 50017, 50018,
		50019, 50021, 50022, 50023, 50024, 50032,
		50034, 50035, 50036, 50037, 50038, 50039, 50040:
		return false
	}
	return true // Includes Custom, Pane, Text and other nonstandard editors.
}

func textCapability(el *comObject) bool {
	for _, item := range []struct {
		id  int
		iid guid
	}{{10024, iidText2}, {10014, iidText}, {10002, iidValue}} {
		if pat, err := pattern(el, item.id, item.iid); err == nil {
			release(pat)
			return true
		}
	}
	if pat, err := pattern(el, 10018, iidLegacy); err == nil {
		role, e := scalar(pat, 10)
		release(pat)
		return e == nil && role == 42 // ROLE_SYSTEM_TEXT
	}
	return false
}

func walkElement(walker, el *comObject, slot int) *comObject {
	if walker == nil {
		return nil
	}
	var next *comObject
	if failed(comCall(walker, slot, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(&next)))) {
		release(next)
		return nil
	}
	return next
}

// Raw ancestors are used only to prove window ownership, not to inspect text.
// Browser renderer process IDs need not equal the foreground process ID.
func withinForeground(walker, el *comObject, window uintptr) bool {
	current := el
	owned := false
	defer func() {
		if owned {
			release(current)
		}
	}()
	for depth := 0; depth <= maxBoundaryParents; depth++ {
		var handle uintptr
		if failed(comCall(current, 36, uintptr(unsafe.Pointer(&handle)))) {
			return false
		}
		if handle != 0 {
			root, _, _ := pPPTGetAncestor.Call(handle, 2) // GA_ROOT
			return root == window
		}
		if depth == maxBoundaryParents {
			break
		}
		next := walkElement(walker, current, 3)
		if next == nil {
			return false
		}
		if owned {
			release(current)
		}
		current, owned = next, true
	}
	return false
}

func focusedProbe(a *comObject, c core.Config, padWindow uintptr) (_ *uiaFocusProbe, err error) {
	window := foreground()
	_, process, err := processOf(window)
	if err != nil {
		return nil, err
	}
	if window != padWindow && !c.Allows(process) {
		return nil, fmt.Errorf("%s is not approved; add its executable name in Settings before reading it", process)
	}
	if composing(window) {
		return nil, errors.New("finish the current IME composition first")
	}
	p := &uiaFocusProbe{a: a}
	defer func() {
		if err != nil {
			p.close()
		}
	}()
	if failed(comCall(a, 8, uintptr(unsafe.Pointer(&p.focused)))) || p.focused == nil {
		return nil, errors.New("no accessible focused textbox")
	}
	if err = safeElement(p.focused, true); err != nil {
		return nil, err
	}
	if err = writableElement(p.focused); err != nil {
		return nil, err
	}
	control, e := scalar(p.focused, 21)
	if e != nil {
		return nil, e
	}
	p.control = control
	p.trackingAllowed = trackingControlAllowed(control, textCapability(p.focused))
	id, err := focusID(p.focused)
	if err != nil {
		return nil, err
	}
	// IUIAutomation::get_RawViewWalker is slot 16.
	comCall(a, 16, uintptr(unsafe.Pointer(&p.walker)))
	if !withinForeground(p.walker, p.focused, window) {
		return nil, errors.New("focused element does not belong to the foreground window")
	}
	p.target = core.TextContext{Window: uint64(window), Process: process, FocusID: id, ProviderID: id, Source: "uia-unavailable", State: core.StateUnknown}
	p.position(p.focused, nil)
	if err = p.recheck(); err != nil {
		return nil, err
	}
	return p, nil
}

// Called by the keyboard worker before admitting an observation. It reads
// properties only; text is obtained later by readContext when needed.
func probeTrackingTarget(a *comObject, c core.Config, padWindow uintptr) (core.TextContext, error) {
	p, err := focusedProbe(a, c, padWindow)
	if err != nil {
		return core.TextContext{}, err
	}
	defer p.close()
	if !p.trackingAllowed {
		return core.TextContext{}, errors.New("focused control has no editable text capability")
	}
	visited := 0
	if err := p.checkDescendantProtection(p.focused, 1, &visited); err != nil {
		return core.TextContext{}, err
	}
	if err := p.recheck(); err != nil {
		return core.TextContext{}, err
	}
	return p.target, nil
}

func (p *uiaFocusProbe) recheck() error {
	if foreground() != uintptr(p.target.Window) {
		return errors.New("focus changed while reading; try again")
	}
	var current *comObject
	if failed(comCall(p.a, 8, uintptr(unsafe.Pointer(&current)))) || current == nil {
		release(current)
		return errors.New("focus disappeared")
	}
	defer release(current)
	id, err := focusID(current)
	if err != nil || id != p.target.FocusID {
		return errors.New("textbox changed while reading; try again")
	}
	if err := safeElement(current, true); err != nil {
		return err
	}
	if composing(uintptr(p.target.Window)) {
		return errors.New("finish the current IME composition first")
	}
	return nil
}

func (p *uiaFocusProbe) position(el, caret *comObject) {
	x, y, h, ok := nativeCaret(uintptr(p.target.Window))
	source := "native caret"
	if !ok && caret != nil {
		if box, yes := rangeRect(caret); yes {
			x, y, h, ok = box.Left, box.Bottom, box.Bottom-box.Top, true
			source = "UIA caret range"
		}
		if !ok {
			if r, err := cloneRange(caret); err == nil {
				if moveEnd(r, 0, -1) == nil {
					if box, yes := rangeRect(r); yes {
						x, y, h, ok = box.Right, box.Bottom, box.Bottom-box.Top, true
						source = "UIA preceding glyph"
					}
				}
				release(r)
			}
		}
	}
	if !ok {
		var box rect
		if !failed(comCall(el, 43, uintptr(unsafe.Pointer(&box)))) && box.Right > box.Left && box.Bottom > box.Top {
			x, y, h, ok = box.Left+12, box.Top+28, 20, true
			source = "field-corner positioning"
		}
	}
	if ok {
		p.target.X, p.target.Y, p.target.CaretHeight, p.target.PositionSource = x, y, h, source
	}
}

func (p *uiaFocusProbe) read(c core.Config, identity *caretIdentity) (core.TextContext, error) {
	result, err := p.readProvider(p.focused, true, c, identity)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, errNoTextProvider) {
		return core.TextContext{}, err
	}
	// A wrapper's IsPassword=false must not override a protected inner editor.
	// Exact focused text reads above already established their own safe caret.
	protectedVisited := 0
	if err := p.checkDescendantProtection(p.focused, 1, &protectedVisited); err != nil {
		return core.TextContext{}, err
	}
	// Nearby ancestors can host the focused editor's pattern. Never scan their
	// descendants: that would include sibling fields outside the focus subtree.
	parentsVisited := 0
	parent := walkElement(p.walker, p.focused, 3)
	for depth := 1; parent != nil && depth <= maxProviderParents; depth++ {
		parentsVisited++
		if !withinForeground(p.walker, parent, uintptr(p.target.Window)) {
			release(parent)
			parent = nil
			break
		}
		if e := safeElement(parent, false); e != nil {
			release(parent)
			return core.TextContext{}, e
		}
		result, e := p.readProvider(parent, false, c, identity)
		if e == nil {
			release(parent)
			return result, nil
		}
		if !errors.Is(e, errNoTextProvider) {
			release(parent)
			return core.TextContext{}, e
		}
		next := walkElement(p.walker, parent, 3)
		release(parent)
		parent = next
	}
	release(parent)
	visited := 0
	result, err = p.readDescendants(p.focused, 1, &visited, c, identity)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, errNoTextProvider) {
		return core.TextContext{}, err
	}
	if !p.trackingAllowed {
		return core.TextContext{}, errors.New("focused control has no editable text capability")
	}
	value, known, source, err := focusedValue(p.focused)
	if err != nil {
		return core.TextContext{}, err
	}
	diagnostic := capabilityDiagnostic(p.focused, p.control, parentsVisited+visited)
	if err = p.recheck(); err != nil {
		return core.TextContext{}, err
	}
	if p.target.CaretHeight <= 0 {
		return core.TextContext{}, errors.New("textbox location unavailable")
	}
	if known {
		p.target.Source = source
	}
	return core.TextContext{}, &contextUnavailableError{Target: p.target, Value: value, ValueKnown: known, Reason: diagnostic}
}

func (p *uiaFocusProbe) readDescendants(el *comObject, depth int, visited *int, c core.Config, identity *caretIdentity) (core.TextContext, error) {
	if depth > maxProviderDepth || *visited >= maxProviderNodes {
		return core.TextContext{}, errNoTextProvider
	}
	child := walkElement(p.walker, el, 4)
	for child != nil && *visited < maxProviderNodes {
		*visited++
		// A protected/unavailable branch is pruned without reading its patterns
		// or any descendants. Other child providers can still be considered.
		if safeElement(child, false) == nil && withinForeground(p.walker, child, uintptr(p.target.Window)) {
			result, err := p.readProvider(child, false, c, identity)
			if err == nil {
				release(child)
				return result, nil
			}
			if !errors.Is(err, errNoTextProvider) {
				release(child)
				return core.TextContext{}, err
			}
			result, err = p.readDescendants(child, depth+1, visited, c, identity)
			if err == nil {
				release(child)
				return result, nil
			}
			if !errors.Is(err, errNoTextProvider) {
				release(child)
				return core.TextContext{}, err
			}
		}
		next := walkElement(p.walker, child, 6)
		release(child)
		child = next
	}
	release(child)
	return core.TextContext{}, errNoTextProvider
}

// A nearby provider must prove that it owns the active caret; a collapsed but
// stale selection in another editor is not sufficient. The focused provider can
// use TextPattern alone, since keyboard focus already establishes ownership.
func (p *uiaFocusProbe) readProvider(el *comObject, original bool, c core.Config, identity *caretIdentity) (core.TextContext, error) {
	if err := safeElement(el, original); err != nil {
		return core.TextContext{}, err
	}
	pat, e2 := pattern(el, 10024, iidText2)
	isText2 := e2 == nil
	if !isText2 {
		pat, e2 = pattern(el, 10014, iidText)
	}
	if e2 != nil {
		return core.TextContext{}, errNoTextProvider
	}
	defer release(pat)
	var activeCaret *comObject
	active := int32(0)
	if isText2 {
		if failed(comCall(pat, 10, uintptr(unsafe.Pointer(&active)), uintptr(unsafe.Pointer(&activeCaret)))) {
			active = 0
		}
	}
	defer func() { release(activeCaret) }()
	focused, _ := scalar(el, 26)
	if !original && focused == 0 && (active == 0 || activeCaret == nil) {
		return core.TextContext{}, errNoTextProvider
	}
	if err := writableElement(el); err != nil {
		return core.TextContext{}, err
	}
	var selections *comObject
	if failed(comCall(pat, 5, uintptr(unsafe.Pointer(&selections)))) || selections == nil {
		release(selections)
		return core.TextContext{}, errNoTextProvider
	}
	defer release(selections)
	count, err := scalar(selections, 3)
	if err != nil || count == 0 {
		return core.TextContext{}, errNoTextProvider
	}
	if count != 1 {
		return core.TextContext{}, errors.New("place one caret in the textbox without selecting text")
	}
	var caret *comObject
	if failed(comCall(selections, 4, 0, uintptr(unsafe.Pointer(&caret)))) || caret == nil {
		release(caret)
		return core.TextContext{}, errNoTextProvider
	}
	defer release(caret)
	var compare int32
	if failed(comCall(caret, 5, 0, uintptr(unsafe.Pointer(caret)), 1, uintptr(unsafe.Pointer(&compare)))) {
		return core.TextContext{}, errNoTextProvider
	}
	if compare != 0 {
		return core.TextContext{}, errors.New("selected text is not replaced; clear the selection first")
	}
	if !original && focused == 0 && !sameRangeEndpoints(caret, activeCaret) {
		return core.TextContext{}, errNoTextProvider
	}
	source := "uia-text"
	if isText2 && active != 0 && activeCaret != nil && sameRangeEndpoints(caret, activeCaret) {
		source = "uia-text2"
	}
	if rangeReadonly(caret) {
		return core.TextContext{}, errors.New("read-only text: completion disabled")
	}
	before, err := cloneRange(caret)
	if err != nil {
		return core.TextContext{}, err
	}
	defer release(before)
	after, err := cloneRange(caret)
	if err != nil {
		return core.TextContext{}, err
	}
	defer release(after)
	if err = moveEnd(before, 0, -c.PrefixChars); err != nil {
		return core.TextContext{}, errNoTextProvider
	}
	if err = moveEnd(after, 1, c.SuffixChars); err != nil {
		return core.TextContext{}, errNoTextProvider
	}
	prefix, err := rangeText(before, c.PrefixChars*2+4)
	if err != nil {
		return core.TextContext{}, errNoTextProvider
	}
	suffix, err := rangeText(after, c.SuffixChars*2+4)
	if err != nil {
		return core.TextContext{}, errNoTextProvider
	}
	id, err := focusID(el)
	if err != nil {
		return core.TextContext{}, err
	}
	caretID, err := identity.identifyInPattern(uintptr(p.target.Window), p.target.FocusID+"/provider:"+id, pat, caret)
	if err != nil {
		return core.TextContext{}, err
	}
	p.position(el, caret)
	if p.target.CaretHeight <= 0 {
		return core.TextContext{}, errors.New("textbox location unavailable")
	}
	knownBefore, knownAfter := knownRangeBoundaries(pat, before, after)
	if err = p.recheck(); err != nil {
		return core.TextContext{}, err
	}
	result := p.target
	result.ProviderID, result.CaretID, result.Source = id, caretID, source
	result.Prefix, result.Suffix = core.Tail(prefix, c.PrefixChars), core.Head(suffix, c.SuffixChars)
	result.State, result.Confidence = core.StateSynchronized, 1
	result.KnownBefore, result.KnownAfter = knownBefore, knownAfter
	result.KnownBefore = result.KnownBefore && len([]rune(prefix)) <= c.PrefixChars
	result.KnownAfter = result.KnownAfter && len([]rune(suffix)) <= c.SuffixChars
	return result, nil
}

func boundedPatternValue(pat *comObject, slot int) (string, bool) {
	var value *uint16
	if failed(comCall(pat, slot, uintptr(unsafe.Pointer(&value)))) {
		if value != nil {
			pSysFreeString.Call(uintptr(unsafe.Pointer(value)))
		}
		return "", false
	}
	if value == nil {
		return "", true
	}
	defer pSysFreeString.Call(uintptr(unsafe.Pointer(value)))
	n, _, _ := pSysStringLen.Call(uintptr(unsafe.Pointer(value)))
	if n > maxFallbackValue {
		return "", false
	}
	return string(utf16.Decode(unsafe.Slice(value, int(n)))), true
}

func focusedValue(el *comObject) (string, bool, string, error) {
	if err := safeElement(el, true); err != nil {
		return "", false, "", err
	}
	if err := writableElement(el); err != nil {
		return "", false, "", err
	}
	if pat, err := pattern(el, 10002, iidValue); err == nil {
		defer release(pat)
		value, known := boundedPatternValue(pat, 4)
		if known {
			return value, true, "uia-value", nil
		}
	}
	if pat, err := pattern(el, 10018, iidLegacy); err == nil {
		defer release(pat)
		// MSAA values from sliders or other non-text controls are not context.
		role, e := scalar(pat, 10)
		if e == nil && role == 42 {
			value, known := boundedPatternValue(pat, 8)
			return value, known, "uia-legacy", nil
		}
	}
	return "", false, "", nil
}

// Only proven document boundaries permit shadow-editor whole-field operations.
// A bounded snapshot can be exact around the caret while omitting other text.
func knownRangeBoundaries(pat, before, after *comObject) (bool, bool) {
	var document *comObject
	if failed(comCall(pat, 7, uintptr(unsafe.Pointer(&document)))) || document == nil {
		release(document)
		return false, false
	}
	defer release(document)
	var start, end int32
	startOK := !failed(comCall(before, 5, 0, uintptr(unsafe.Pointer(document)), 0, uintptr(unsafe.Pointer(&start)))) && start == 0
	endOK := !failed(comCall(after, 5, 1, uintptr(unsafe.Pointer(document)), 1, uintptr(unsafe.Pointer(&end)))) && end == 0
	return startOK, endOK
}

// Pattern availability and numeric ControlType are safe metadata. Names and
// accessible descriptions are deliberately excluded because they may contain text.
func capabilityDiagnostic(el *comObject, control int32, searched int) string {
	capabilities := [4]bool{}
	for i, item := range []struct {
		id  int
		iid guid
	}{{10024, iidText2}, {10014, iidText}, {10002, iidValue}, {10018, iidLegacy}} {
		if pat, err := pattern(el, item.id, item.iid); err == nil {
			capabilities[i] = true
			release(pat)
		}
	}
	return fmt.Sprintf("UIA ControlType=%d; TextPattern2=%t TextPattern=%t Value=%t Legacy=%t; searched %d nearby nodes; no verified caret, keyboard context required", control, capabilities[0], capabilities[1], capabilities[2], capabilities[3], searched)
}

// Wrapper controls sometimes omit a password flag that their actual inner
// editor exposes. This metadata-only scan runs before admitting keyboard text,
// as well as before attempting an alternate provider for a wrapper.
func (p *uiaFocusProbe) checkDescendantProtection(el *comObject, depth int, visited *int) error {
	if depth > maxProviderDepth || *visited >= maxProviderNodes {
		return nil
	}
	child := walkElement(p.walker, el, 4)
	for child != nil && *visited < maxProviderNodes {
		*visited++
		if withinForeground(p.walker, child, uintptr(p.target.Window)) {
			descend, err := safeDescendantBranch(child)
			if err != nil {
				release(child)
				return err
			}
			if descend {
				if err := p.checkDescendantProtection(child, depth+1, visited); err != nil {
					release(child)
					return err
				}
			}
		}
		next := walkElement(p.walker, child, 6)
		release(child)
		child = next
	}
	release(child)
	return nil
}

func safeDescendantBranch(el *comObject) (bool, error) {
	password, err := scalar(el, 35)
	if err == nil && password != 0 {
		return false, errors.New("protected descendant editor: keyboard context disabled")
	}
	focused, focusErr := scalar(el, 26)
	if err != nil {
		if focusErr == nil && focused != 0 {
			return false, errors.New("focused descendant protection is unknown: keyboard context disabled")
		}
		return false, nil // Do not inspect patterns or descend into an unknown branch.
	}
	if pat, err := pattern(el, 10018, iidLegacy); err == nil {
		state, stateErr := scalar(pat, 11)
		release(pat)
		if stateErr == nil && uint32(state)&0x20000000 != 0 {
			return false, errors.New("protected legacy descendant: keyboard context disabled")
		}
		if focused != 0 && (stateErr != nil || uint32(state)&(0x40|0x1) != 0) {
			return false, errors.New("focused descendant is read-only or unavailable: keyboard context disabled")
		}
	}
	if focused != 0 {
		if err := safeElement(el, true); err != nil {
			return false, err
		}
		if err := writableElement(el); err != nil {
			return false, err
		}
	}
	return true, nil
}
