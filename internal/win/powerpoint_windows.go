//go:build windows && amd64

package win

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"syscall"
	"typenext/internal/core"
	"unicode/utf16"
	"unsafe"
)

var (
	pPPTAccessibleObjectFromWindow = syscall.NewLazyDLL("oleacc.dll").NewProc("AccessibleObjectFromWindow")
	pPPTGetClassName               = user32.NewProc("GetClassNameW")
	pPPTGetParent                  = user32.NewProc("GetParent")
	pPPTGetAncestor                = user32.NewProc("GetAncestor")
	pPPTGetWindowRect              = user32.NewProc("GetWindowRect")
	iidPPTDispatch                 = guid{A: 0x00020400, B: 0, C: 0, D: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

type pptDispatchParams struct {
	Args       *variant
	Named      *int32
	Count      uint32
	NamedCount uint32
}

type pptException struct {
	Code, Reserved  uint16
	Source          *uint16
	Description     *uint16
	HelpFile        *uint16
	HelpContext     uint32
	ReservedPointer uintptr
	DeferredFill    uintptr
	SCode           int32
}

func pptIntArg(n int32) variant {
	v := variant{VT: 3} // VT_I4
	*(*int32)(unsafe.Pointer(&v.Data[0])) = n
	return v
}

func pptFloatArg(n float32) variant {
	v := variant{VT: 4} // VT_R4
	*(*float32)(unsafe.Pointer(&v.Data[0])) = n
	return v
}

// pptInvoke is deliberately limited to getters and read-only methods.
// It never sets a property, selects text, activates a window, or uses the clipboard.
func pptInvoke(o *comObject, name string, flags uint16, args ...variant) (variant, error) {
	var result variant
	if flags != 1 && flags != 2 {
		return result, errors.New("unsupported PowerPoint operation")
	}
	member := u16(name)
	var emptyIID guid
	var id int32
	hr := comCall(o, 5, uintptr(unsafe.Pointer(&emptyIID)), uintptr(unsafe.Pointer(&member)), 1, 0x400, uintptr(unsafe.Pointer(&id)))
	if failed(hr) {
		return result, fmt.Errorf("PowerPoint does not expose %s (0x%08x)", name, uint32(hr))
	}
	// IDispatch takes positional arguments in reverse order.
	reversed := make([]variant, len(args))
	for i := range args {
		reversed[len(args)-1-i] = args[i]
	}
	params := pptDispatchParams{Count: uint32(len(reversed))}
	if len(reversed) > 0 {
		params.Args = &reversed[0]
	}
	var exception pptException
	var argError uint32
	hr = comCall(o, 6, uintptr(id), uintptr(unsafe.Pointer(&emptyIID)), 0x400, uintptr(flags), uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&result)), uintptr(unsafe.Pointer(&exception)), uintptr(unsafe.Pointer(&argError)))
	runtime.KeepAlive(reversed)
	for _, b := range []*uint16{exception.Source, exception.Description, exception.HelpFile} {
		if b != nil {
			pSysFreeString.Call(uintptr(unsafe.Pointer(b)))
		}
	}
	if failed(hr) {
		pVariantClear.Call(uintptr(unsafe.Pointer(&result)))
		return variant{}, fmt.Errorf("PowerPoint %s is unavailable (0x%08x)", name, uint32(hr))
	}
	return result, nil
}

func pptObject(o *comObject, name string, args ...int32) (*comObject, error) {
	flags := uint16(2)
	values := make([]variant, len(args))
	if len(args) > 0 {
		flags = 1
	}
	for i, n := range args {
		values[i] = pptIntArg(n)
	}
	v, err := pptInvoke(o, name, flags, values...)
	if err != nil {
		return nil, err
	}
	if v.VT == 9 { // VT_DISPATCH: transfer its reference to the caller.
		out := *(**comObject)(unsafe.Pointer(&v.Data[0]))
		if out != nil {
			return out, nil
		}
	}
	pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	return nil, fmt.Errorf("PowerPoint %s is not accessible", name)
}

func pptInteger(o *comObject, name string) (int32, error) {
	v, err := pptInvoke(o, name, 2)
	if err != nil {
		return 0, err
	}
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	switch v.VT {
	case 3, 22: // VT_I4, VT_INT
		return *(*int32)(unsafe.Pointer(&v.Data[0])), nil
	case 2, 11: // VT_I2, VT_BOOL
		return int32(*(*int16)(unsafe.Pointer(&v.Data[0]))), nil
	}
	return 0, fmt.Errorf("PowerPoint %s has an unsupported value", name)
}

func pptNumber(o *comObject, name string, args ...variant) (float64, error) {
	flags := uint16(2)
	if len(args) > 0 {
		flags = 1
	}
	v, err := pptInvoke(o, name, flags, args...)
	if err != nil {
		return 0, err
	}
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	var n float64
	switch v.VT {
	case 4:
		n = float64(*(*float32)(unsafe.Pointer(&v.Data[0])))
	case 5:
		n = *(*float64)(unsafe.Pointer(&v.Data[0]))
	case 3, 22:
		n = float64(*(*int32)(unsafe.Pointer(&v.Data[0])))
	default:
		return 0, fmt.Errorf("PowerPoint %s has an unsupported coordinate", name)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e7 {
		return 0, errors.New("PowerPoint returned an invalid coordinate")
	}
	return n, nil
}

// Only walk HWND ancestors of the actual keyboard focus.
// Enumerating arbitrary presentation panes could pick a stale selection when the user is in a ribbon,
// search box, dialog, another presentation, or slideshow.
func pptFocusedPane(window uintptr) (pane, focus uintptr, ok bool) {
	g, yes := guiInfo(window)
	if !yes || g.Focus == 0 {
		return 0, 0, false
	}
	pane = pptPaneFromFocus(window, g.Focus,
		func(w uintptr) uintptr { root, _, _ := pPPTGetAncestor.Call(w, 2); return root },
		func(w uintptr) uintptr { parent, _, _ := pPPTGetParent.Call(w); return parent },
		pptWindowClass)
	return pane, g.Focus, pane != 0
}

func pptWindowClass(window uintptr) string {
	var name [128]uint16
	if n, _, _ := pPPTGetClassName.Call(window, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name))); n == 0 {
		return "unknown"
	}
	return syscall.UTF16ToString(name[:])
}

func pptPaneFromFocus(window, focus uintptr, rootOf, parentOf func(uintptr) uintptr, classOf func(uintptr) string) uintptr {
	if window == 0 || focus == 0 || rootOf(focus) != window {
		return 0
	}
	for current, depth := focus, 0; current != 0 && current != window && depth < 32; depth++ {
		class := classOf(current)
		// Current desktop PowerPoint exposes its native DocumentWindow on
		// mdiClass. The Office 2000 API documentation names paneClassDC.
		if strings.EqualFold(class, "mdiClass") || strings.EqualFold(class, "paneClassDC") {
			return current
		}
		current = parentOf(current)
	}
	return 0
}

type pptCaret struct {
	rangeObject    *comObject
	frameRange     *comObject
	start          int32
	length         int32
	slideID        int32
	shapeID        int32
	presentationID [32]byte
}

func (c *pptCaret) close() {
	release(c.rangeObject)
	release(c.frameRange)
}

func (c *pptCaret) identity() string {
	return fmt.Sprintf("presentation:%x/slide:%d/shape:%d/start:%d", c.presentationID, c.slideID, c.shapeID, c.start)
}

// pptSelection checks state before reading any Text property.
// TextFrame's range object and Length are metadata; only bounded Characters(...).Text is read.
func pptSelection(document *comObject) (_ *pptCaret, err error) {
	active, err := pptInteger(document, "Active")
	if err != nil || active == 0 {
		return nil, errors.New("PowerPoint's presentation window is not active")
	}
	viewType, err := pptInteger(document, "ViewType")
	if err != nil || (viewType != 1 && viewType != 9) { // ppViewSlide / ppViewNormal
		return nil, errors.New("edit a slide textbox in PowerPoint's Normal view")
	}
	pane, err := pptObject(document, "ActivePane")
	if err != nil {
		return nil, err
	}
	defer release(pane)
	paneType, err := pptInteger(pane, "ViewType")
	if err != nil || paneType != 1 {
		return nil, errors.New("place the caret in a slide textbox, not the outline or notes pane")
	}
	presentation, err := pptObject(document, "Presentation")
	if err != nil {
		return nil, err
	}
	defer release(presentation)
	readOnly, err := pptInteger(presentation, "ReadOnly")
	if err != nil {
		return nil, fmt.Errorf("could not verify PowerPoint editing permissions: %w", err)
	}
	if readOnly != 0 {
		return nil, errors.New("PowerPoint reports this presentation is read-only; use an editable presentation")
	}
	// FullName is metadata, never presentation text. Hash it locally to prevent
	// identical slide/shape IDs in different presentations sharing a caret ID.
	fullName, err := pptString(presentation, "FullName", 32768)
	if err != nil || fullName == "" {
		return nil, errors.New("PowerPoint presentation has no stable identity")
	}
	selection, err := pptObject(document, "Selection")
	if err != nil {
		return nil, err
	}
	defer release(selection)
	kind, err := pptInteger(selection, "Type")
	if err != nil || kind != 3 { // ppSelectionText
		return nil, errors.New("place a text caret inside a PowerPoint textbox; selecting a shape is not enough")
	}
	selected, err := pptObject(selection, "TextRange")
	if err != nil {
		return nil, err
	}
	out := &pptCaret{rangeObject: selected, presentationID: sha256.Sum256([]byte(fullName))}
	defer func() {
		if err != nil {
			out.close()
		}
	}()
	selectedLength, err := pptInteger(selected, "Length")
	if err != nil || selectedLength != 0 {
		return nil, errors.New("selected text is not replaced; clear the PowerPoint selection first")
	}
	out.start, err = pptInteger(selected, "Start")
	if err != nil || out.start < 1 {
		return nil, errors.New("PowerPoint caret position is unavailable")
	}
	frame, err := pptObject(selected, "Parent")
	if err != nil {
		return nil, err
	}
	defer release(frame)
	out.frameRange, err = pptObject(frame, "TextRange")
	if err != nil {
		return nil, errors.New("this PowerPoint object does not expose an editable text frame")
	}
	out.length, err = pptInteger(out.frameRange, "Length")
	if err != nil || out.length < 0 || int64(out.start) > int64(out.length)+1 {
		return nil, errors.New("PowerPoint caret is outside its text frame")
	}
	shape, err := pptObject(frame, "Parent")
	if err != nil {
		return nil, err
	}
	defer release(shape)
	out.shapeID, err = pptInteger(shape, "Id")
	if err != nil || out.shapeID <= 0 {
		return nil, errors.New("PowerPoint textbox has no stable identity")
	}
	// Table cells, grouped shapes, charts and SmartArt can expose inner ranges
	// whose offsets are not unique within the selected shape.
	// Support ordinary textboxes, placeholders and autoshapes with one unambiguous text frame.
	shapeType, err := pptInteger(shape, "Type")
	if err != nil || (shapeType != 1 && shapeType != 14 && shapeType != 17) {
		return nil, errors.New("this PowerPoint object is unsupported; use a regular slide textbox")
	}
	shapes, err := pptObject(selection, "ShapeRange")
	if err != nil {
		return nil, err
	}
	defer release(shapes)
	shapeCount, err := pptInteger(shapes, "Count")
	if err != nil || shapeCount != 1 {
		return nil, errors.New("PowerPoint must have one active textbox")
	}
	selectedShape, err := pptObject(shapes, "Item", 1)
	if err != nil {
		return nil, err
	}
	defer release(selectedShape)
	selectedID, err := pptInteger(selectedShape, "Id")
	if err != nil || selectedID != out.shapeID {
		return nil, errors.New("PowerPoint selection does not identify one regular textbox")
	}
	selectedType, err := pptInteger(selectedShape, "Type")
	if err != nil || selectedType != shapeType {
		return nil, errors.New("PowerPoint selection belongs to an unsupported container")
	}
	view, err := pptObject(document, "View")
	if err != nil {
		return nil, err
	}
	defer release(view)
	slide, err := pptObject(view, "Slide")
	if err != nil {
		return nil, err
	}
	defer release(slide)
	out.slideID, err = pptInteger(slide, "SlideID")
	if err != nil || out.slideID <= 0 {
		return nil, errors.New("PowerPoint slide has no stable identity")
	}
	return out, nil
}

func pptTextSpan(frame *comObject, start, length int32) (string, error) {
	// Characters past the end can clamp to the final character.
	// Never ask for an empty range, otherwise an end-of-text suffix could repeat that character.
	if length == 0 {
		return "", nil
	}
	if start < 1 || length < 0 || length > 16000 {
		return "", errors.New("PowerPoint context range is invalid")
	}
	r, err := pptObject(frame, "Characters", start, length)
	if err != nil {
		return "", err
	}
	defer release(r)
	actualStart, err := pptInteger(r, "Start")
	if err != nil || actualStart != start {
		return "", errors.New("PowerPoint context range moved")
	}
	actualLength, err := pptInteger(r, "Length")
	if err != nil || actualLength != length {
		return "", errors.New("PowerPoint text changed while reading")
	}
	return pptString(r, "Text", int(length)*2)
}

func pptString(o *comObject, name string, maxUnits int) (string, error) {
	v, err := pptInvoke(o, name, 2)
	if err != nil {
		return "", err
	}
	defer pVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	if v.VT != 8 { // VT_BSTR
		return "", errors.New("PowerPoint context is not text")
	}
	b := *(**uint16)(unsafe.Pointer(&v.Data[0]))
	if b == nil {
		return "", nil
	}
	n, _, _ := pSysStringLen.Call(uintptr(unsafe.Pointer(b)))
	if n > uintptr(maxUnits) {
		return "", errors.New("PowerPoint returned more text than requested")
	}
	return string(utf16.Decode(unsafe.Slice(b, int(n)))), nil
}

func pptContextText(c *pptCaret, prefixLimit, suffixLimit int) (string, string, error) {
	if prefixLimit < 0 || prefixLimit > 8000 || suffixLimit < 0 || suffixLimit > 2000 {
		return "", "", errors.New("invalid PowerPoint context limits")
	}
	// Office offsets count UTF-16 characters.
	// Read enough for supplementary characters, then apply the same Unicode-rune caps as the UIA path.
	before := min(c.start-1, int32(prefixLimit*2))
	after := min(c.length-c.start+1, int32(suffixLimit*2))
	prefix, err := pptTextSpan(c.frameRange, c.start-before, before)
	if err != nil {
		return "", "", err
	}
	suffix, err := pptTextSpan(c.frameRange, c.start, after)
	if err != nil {
		return "", "", err
	}
	return core.Tail(prefix, prefixLimit), core.Head(suffix, suffixLimit), nil
}

func pptRangePosition(document, r *comObject) (int32, int32, int32, bool) {
	left, err := pptNumber(r, "BoundLeft")
	if err != nil {
		return 0, 0, 0, false
	}
	top, err := pptNumber(r, "BoundTop")
	if err != nil {
		return 0, 0, 0, false
	}
	height, err := pptNumber(r, "BoundHeight")
	if err != nil || height <= 0 || height > 10000 {
		return 0, 0, 0, false
	}
	x, ex := pptNumber(document, "PointsToScreenPixelsX", pptFloatArg(float32(left)))
	y, ey := pptNumber(document, "PointsToScreenPixelsY", pptFloatArg(float32(top+height)))
	yTop, et := pptNumber(document, "PointsToScreenPixelsY", pptFloatArg(float32(top)))
	if ex != nil || ey != nil || et != nil || y <= yTop || y-yTop > 10000 {
		return 0, 0, 0, false
	}
	return int32(math.Round(x)), int32(math.Round(y)), int32(math.Round(y - yTop)), true
}

func readPowerPointContext(c core.Config, window uintptr, process string) (core.TextContext, error) {
	var result core.TextContext
	if !strings.EqualFold(process, "powerpnt.exe") || !c.Allows(process) || foreground() != window {
		return result, errors.New("PowerPoint is not the approved foreground application")
	}
	pane, focus, ok := pptFocusedPane(window)
	if !ok {
		g, available := guiInfo(window)
		if !available || g.Focus == 0 {
			return result, errors.New("PowerPoint keyboard focus is unavailable; switch back to the slide text")
		}
		return result, fmt.Errorf("PowerPoint editing pane not recognized (focused window class: %s); use a slide textbox in Normal view", pptWindowClass(g.Focus))
	}
	var document *comObject
	hr, _, _ := pPPTAccessibleObjectFromWindow.Call(pane, 0xfffffff0, uintptr(unsafe.Pointer(&iidPPTDispatch)), uintptr(unsafe.Pointer(&document))) // OBJID_NATIVEOM
	if failed(hr) || document == nil {
		release(document)
		return result, errors.New("this PowerPoint pane does not expose its native text model")
	}
	defer release(document)
	caret, err := pptSelection(document)
	if err != nil {
		return result, err
	}
	defer caret.close()
	prefix, suffix, err := pptContextText(caret, c.PrefixChars, c.SuffixChars)
	if err != nil {
		return result, err
	}
	x, y, height, positioned := nativeCaret(window)
	source := "PowerPoint native caret"
	if !positioned {
		x, y, height, positioned = pptRangePosition(document, caret.rangeObject)
		source = "PowerPoint text range"
	}
	if !positioned {
		// Logical caret identity remains exact when PowerPoint omits the blinking caret rectangle;
		// only the visual anchor falls back to the focused pane.
		var box rect
		available, _, _ := pPPTGetWindowRect.Call(pane, uintptr(unsafe.Pointer(&box)))
		if available == 0 || box.Right <= box.Left || box.Bottom <= box.Top {
			return result, errors.New("PowerPoint caret location is unavailable")
		}
		x, y, height = box.Left+12, box.Top+28, 20
		source = "PowerPoint (pane-corner positioning)"
	}
	// Re-read the live selection, rather than trusting a retained TextRange after a COM call.
	// The same checks also run immediately before SendInput.
	fresh, err := pptSelection(document)
	if err != nil {
		return result, err
	}
	defer fresh.close()
	currentPane, currentFocus, currentOK := pptFocusedPane(window)
	if foreground() != window || !currentOK || currentPane != pane || currentFocus != focus || fresh.identity() != caret.identity() || fresh.length != caret.length || composing(window) {
		return result, errors.New("PowerPoint textbox/caret changed while reading; try again")
	}
	return core.TextContext{
		Window: uint64(window), FocusID: fmt.Sprintf("powerpoint:%x/pane:%x/focus:%x", window, pane, focus),
		CaretID: caret.identity(), Process: process, Prefix: prefix, Suffix: suffix,
		X: x, Y: y, CaretHeight: height, PositionSource: source,
	}, nil
}
