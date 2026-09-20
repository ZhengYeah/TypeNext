//go:build windows && amd64

package win

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"typenext/internal/core"
	"typenext/internal/win/uia"
	"unsafe"
)

const (
	windowBackground  = 0xFAF8F6
	wmApp             = 0x8000
	wmDispatch        = wmApp + 1
	wmTray            = wmApp + 2
	idSave            = 100
	idTest            = 101
	idInspect         = 102
	idHide            = 103
	idQuit            = 104
	idPad             = 105
	idPause           = 106
	idHotkeys         = 107
	idHotkeyApply     = 108
	idHotkeyDefaults  = 109
	idHotkeyClose     = 110
	idModelRemove     = 117
	ctrlHotkeySuggest = 211
	ctrlHotkeyAccept  = 212
	ctrlHotkeyPause   = 213
	ctrlHotkeyStatus1 = 214
	ctrlHotkeyMessage = 217
	ctrlSavedModel    = 201
	ctrlPause         = 204
	ctrlPrefix        = 205
	ctrlSuffix        = 206
	ctrlAuto          = 207
	ctrlTab           = 208
	ctrlAllowed       = 210
)

// GDI COLORREF values use 0xBBGGRR byte order.
const (
	overlayBackground = 0x3B291E // RGB #1E293B
	overlayBorder     = 0x8B7464 // RGB #64748B
	overlayAccent     = 0xFAA560 // RGB #60A5FA
	overlayForeground = 0xFCFAF8 // RGB #F8FAFC
	overlayMuted      = 0xE1D5CB // RGB #CBD5E1
)

type app struct {
	apiWindow         uintptr
	apiDraft          core.Config
	connectionLabel   uintptr
	modelDetailsLabel uintptr
	endpointLabel     uintptr
	lastRemoteRequest time.Time
	testRunning       bool

	window, overlay, pad, padEdit                        uintptr
	instance, font, heading, smallFont, brush            uintptr
	overlayBrush, overlayBorderBrush, overlayAccentBrush uintptr
	statusBrush                                          uintptr
	icon, smallIcon                                      uintptr
	cfg                                                  core.Config
	configPath                                           string
	controls                                             map[int]uintptr
	statusLabel                                          uintptr
	shortcutLabel, hotkeyWindow                          uintptr
	hotkeys                                              *core.HotkeySet
	hotkeyStates                                         []core.HotkeyStatus
	jobs                                                 chan func()
	worker                                               *uia.Worker
	client                                               *core.Client
	revision                                             atomic.Uint64
	cancel                                               context.CancelFunc
	snapshot                                             *core.TextContext
	suggestion                                           string
	candidateReady                                       bool
	tabAcceptHeld                                        bool
	running                                              bool
	enabled                                              bool
	lastActivity                                         time.Time
	autoArmed                                            bool
	lastForeground                                       uintptr
	overlayModel, overlayText, overlayFooter             string
	inspectAt                                            time.Time
	trayMessage                                          uint32
	scale                                                float64
}

var currentApp *app

func Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// All HWNDs and input hooks live on this OS thread.
	// Accessibility uses a dedicated MTA thread, and inference runs outside both of those threads.
	user32.NewProc("SetProcessDPIAware").Call()
	path, e := core.ConfigPath()
	if e != nil {
		return e
	}
	cfg, configErr := core.LoadConfig(path)
	cfg.EnsureModelProfiles()
	worker, e := newAccessibilityWorker()
	if e != nil {
		return e
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	a := &app{cfg: cfg, configPath: path, worker: worker, client: core.NewClient(), jobs: make(chan func(), 128), controls: map[int]uintptr{}, instance: inst, enabled: true, scale: 1}
	a.client.DecryptKey = unprotectAPIKey
	currentApp = a
	dpi := user32.NewProc("GetDpiForSystem")
	if dpi.Find() == nil {
		d, _, _ := dpi.Call()
		if d >= 96 {
			a.scale = float64(d) / 96
		}
	}
	a.font = a.makeFont(16, 400)
	a.heading = a.makeFont(25, 600)
	a.smallFont = a.makeFont(13, 400)
	a.brush, _, _ = pCreateSolidBrush.Call(windowBackground)
	a.overlayBrush, _, _ = pCreateSolidBrush.Call(overlayBackground)
	a.overlayBorderBrush, _, _ = pCreateSolidBrush.Call(overlayBorder)
	a.overlayAccentBrush, _, _ = pCreateSolidBrush.Call(overlayAccent)
	a.statusBrush, _, _ = pCreateSolidBrush.Call(statusBackground)
	defer pDeleteObject.Call(a.font)
	defer pDeleteObject.Call(a.heading)
	defer pDeleteObject.Call(a.smallFont)
	defer pDeleteObject.Call(a.brush)
	defer pDeleteObject.Call(a.overlayBrush)
	defer pDeleteObject.Call(a.overlayBorderBrush)
	defer pDeleteObject.Call(a.overlayAccentBrush)
	defer pDeleteObject.Call(a.statusBrush)
	cursor, _, _ := pLoadCursor.Call(0, 32512)
	a.icon, e = loadAppIcon(inst, false)
	if e != nil {
		return e
	}
	defer pDestroyIcon.Call(a.icon)
	a.smallIcon, e = loadAppIcon(inst, true)
	if e != nil {
		return e
	}
	defer pDestroyIcon.Call(a.smallIcon)
	proc := syscall.NewCallback(windowProc)
	className := u16("TypeNextWindow")
	wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Style: 3, Proc: proc, Instance: inst, Icon: a.icon, Cursor: cursor, Background: a.brush, ClassName: className, SmallIcon: a.smallIcon}
	if v, _, err := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); v == 0 {
		return fmt.Errorf("register window: %v", err)
	}
	a.window, e = a.createSettingsWindow("TypeNext — writing completion", 100, 70, 752, 784, 0)
	if a.window == 0 {
		return fmt.Errorf("create settings window: %v", e)
	}
	a.hotkeys = core.NewHotkeySet(windowsHotkeyRegistrar{window: a.window})
	defer a.hotkeys.Close()
	a.buildSettings()
	a.refreshModelProfiles()
	a.overlay, _, e = pCreateWindowEx.Call(0x08000088, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(u16("TypeNext suggestion"))), 0x80000000, 0, 0, uintptr(a.s(560)), uintptr(a.s(180)), 0, 0, inst, 0)
	if a.overlay == 0 {
		return fmt.Errorf("create suggestion window: %v", e)
	}
	a.trayMessageValue()
	a.addTray()
	defer a.removeTray()
	// A registration conflict is nonfatal.
	// Shortcuts can be changed even when all configured global keys are unavailable.
	a.applyHotkeys()
	keyboard, _, e := pSetWindowsHookEx.Call(13, syscall.NewCallback(keyboardProc), inst, 0)
	if keyboard == 0 {
		return fmt.Errorf("cannot install keyboard activity hook: %v", e)
	}
	defer pUnhookWindowsHookEx.Call(keyboard)
	mouse, _, e := pSetWindowsHookEx.Call(14, syscall.NewCallback(mouseProc), inst, 0)
	if mouse == 0 {
		return fmt.Errorf("cannot install mouse activity hook: %v", e)
	}
	defer pUnhookWindowsHookEx.Call(mouse)
	pSetTimer.Call(a.window, 1, 150, 0)
	defer pKillTimer.Call(a.window, 1)
	a.setStatus("Ready. Test model uses a fixed sample. " + a.connectionSummary())
	if warning := a.hotkeyWarning(); warning != "" {
		a.setStatus("TypeNext started. " + warning)
	}
	if configErr != nil {
		a.setStatus("Your configuration could not be loaded; safe defaults are active. " + configErr.Error())
	}
	pShowWindow.Call(a.window, 5)
	var m msg
	for {
		ret, _, e := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("message loop: %v", e)
		}
		if ret == 0 {
			break
		}
		// Provide normal keyboard navigation for settings without taking Tab away
		// from the test pad or another application.
		dialog := uintptr(0)
		if m.Window == a.window || a.isSettingsChild(m.Window) {
			dialog = a.window
		}
		if a.hotkeyWindow != 0 {
			child, _, _ := pIsChild.Call(a.hotkeyWindow, m.Window)
			if m.Window == a.hotkeyWindow || child != 0 {
				dialog = a.hotkeyWindow
			}
		}
		if a.apiWindow != 0 {
			child, _, _ := pIsChild.Call(a.apiWindow, m.Window)
			if m.Window == a.apiWindow || child != 0 {
				dialog = a.apiWindow
			}
		}
		if dialog != 0 {
			handled, _, _ := pIsDialogMessage.Call(dialog, uintptr(unsafe.Pointer(&m)))
			if handled != 0 {
				continue
			}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	a.invalidate(false)
	return nil
}

func (a *app) s(v int) int { return int(float64(v) * a.scale) }
func (a *app) makeFont(size, weight int) uintptr {
	f, _, _ := pCreateFont.Call(uintptr(int64(-a.s(size))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
	return f
}
func (a *app) isSettingsChild(w uintptr) bool {
	ok, _, _ := pIsChild.Call(a.window, w)
	return ok != 0
}

// Control coordinates use unscaled client-area pixels relative to the parent.
func (a *app) control(class, text string, id, x, y, w, h int, style uintptr) uintptr {
	return a.controlIn(a.window, class, text, id, x, y, w, h, style)
}
func (a *app) controlIn(parent uintptr, class, text string, id, x, y, w, h int, style uintptr) uintptr {
	ex := uintptr(0)
	if class == "EDIT" {
		ex = 0x200
	}
	child, _, _ := pCreateWindowEx.Call(ex, uintptr(unsafe.Pointer(u16(class))), uintptr(unsafe.Pointer(u16(text))), 0x50000000|style, uintptr(a.s(x)), uintptr(a.s(y)), uintptr(a.s(w)), uintptr(a.s(h)), parent, uintptr(id), a.instance, 0)
	pSendMessage.Call(child, 0x30, a.font, 1)
	if id != 0 {
		a.controls[id] = child
	}
	return child
}
func (a *app) label(text string, x, y, w, h int) uintptr {
	return a.control("STATIC", text, 0, x, y, w, h, 0)
}
func (a *app) button(text string, id, x, y, w int) {
	a.control("BUTTON", text, id, x, y, w, 32, 0x10000)
}
func (a *app) checkbox(text string, id, x, y, w int, checked bool) {
	h := a.control("BUTTON", text, id, x, y, w, 26, 0x10003)
	if checked {
		pSendMessage.Call(h, 0xf1, 1, 0)
	}
}
func (a *app) separator(x, y, w int) {
	a.separatorIn(a.window, x, y, w)
}

func (a *app) buildSettings() {
	h := a.label("TypeNext", 24, 24, 280, 32)
	pSendMessage.Call(h, 0x30, a.heading, 1)
	a.button("Shortcuts…", idHotkeys, 376, 24, 128)
	a.button("Pause", idPause, 520, 24, 96)
	a.button("Quit", idQuit, 632, 24, 96)
	a.refreshPauseUI()
	a.label("Writing completion  /  Local model or API  /  "+core.Version, 24, 64, 704, 22)
	a.separator(24, 96, 704)
	a.label("1  Connect a model", 24, 118, 704, 24)
	a.label("Saved model", 24, 156, 100, 24)
	a.control("COMBOBOX", "", ctrlSavedModel, 132, 152, 296, 240, 0x10003)
	a.button("Remove model", idModelRemove, 444, 150, 128)
	a.button("API settings…", idAPI, 588, 150, 140)
	a.label("Connection", 24, 196, 100, 24)
	// Separate single-line labels keep native ellipsis from collapsing the URL
	// onto the model/protocol line.
	a.modelDetailsLabel = a.control("STATIC", "", 0, 132, 196, 596, 20, 0x4080) // SS_ENDELLIPSIS | SS_NOPREFIX
	a.endpointLabel = a.control("STATIC", "", 0, 132, 220, 596, 20, 0x4080)
	a.connectionLabel = a.statusText(a.window, "", 0, 24, 248, 704, 44)
	a.label("2  Choose the interaction", 24, 308, 704, 24)
	a.checkbox("Automatic suggestions after a typing pause", ctrlAuto, 24, 340, 704, a.cfg.Auto)
	a.checkbox("Tab accepts a finished suggestion (recommended)", ctrlTab, 24, 372, 704, a.cfg.AcceptTab)
	// Three equal 224-pixel groups, separated by 16-pixel gutters.
	a.label("Pause (ms)", 24, 420, 100, 24)
	a.control("EDIT", strconv.Itoa(a.cfg.DebounceMS), ctrlPause, 132, 416, 116, 28, 0x12000)
	a.label("Before caret", 264, 420, 100, 24)
	a.control("EDIT", strconv.Itoa(a.cfg.PrefixChars), ctrlPrefix, 372, 416, 116, 28, 0x12000)
	a.label("After caret", 504, 420, 100, 24)
	a.control("EDIT", strconv.Itoa(a.cfg.SuffixChars), ctrlSuffix, 612, 416, 116, 28, 0x12000)
	a.label("3  Approve applications", 24, 464, 368, 24)
	h = a.control("STATIC", "One executable name per line", 0, 408, 468, 320, 20, 2) // SS_RIGHT
	pSendMessage.Call(h, 0x30, a.smallFont, 1)
	a.control("EDIT", strings.Join(a.cfg.AllowedApps, "\r\n"), ctrlAllowed, 24, 496, 704, 80, 0x211044)
	h = a.label("Only approved apps are read. Password fields are skipped. No clipboard, OCR, or text logs.", 24, 588, 704, 20)
	pSendMessage.Call(h, 0x30, a.smallFont, 1)
	a.separator(24, 624, 704)
	a.button("Save settings", idSave, 24, 640, 128)
	a.button("Test model", idTest, 168, 640, 128)
	a.button("Open test pad", idPad, 312, 640, 128)
	a.button("Inspect in 3s", idInspect, 456, 640, 128)
	a.button("Hide to tray", idHide, 600, 640, 128)
	a.statusLabel = a.statusText(a.window, "", 0, 24, 688, 704, 44)
	a.shortcutLabel = a.statusText(a.window, "", 0, 24, 732, 704, 28)
}
func windowText(w uintptr) string {
	n, _, _ := pGetWindowTextLength.Call(w)
	if n > 10000 {
		n = 10000
	}
	b := make([]uint16, int(n)+1)
	pGetWindowText.Call(w, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	return syscall.UTF16ToString(b)
}
func checked(w uintptr) bool { v, _, _ := pSendMessage.Call(w, 0xf0, 0, 0); return v == 1 }
func (a *app) readSettings() (core.Config, error) {
	c := a.cfg.Clone()
	var e error
	for _, field := range []struct {
		id  int
		dst *int
	}{{ctrlPause, &c.DebounceMS}, {ctrlPrefix, &c.PrefixChars}, {ctrlSuffix, &c.SuffixChars}} {
		*field.dst, e = strconv.Atoi(strings.TrimSpace(windowText(a.controls[field.id])))
		if e != nil {
			return c, errors.New("pause and context lengths must be whole numbers")
		}
	}
	c.Auto = checked(a.controls[ctrlAuto])
	c.AcceptTab = checked(a.controls[ctrlTab])
	c.AllowedApps = nil
	for _, line := range strings.FieldsFunc(windowText(a.controls[ctrlAllowed]), func(r rune) bool { return r == '\r' || r == '\n' || r == ',' || r == ';' }) {
		if s := strings.ToLower(strings.TrimSpace(line)); s != "" {
			c.AllowedApps = append(c.AllowedApps, s)
		}
	}
	return c, nil
}
func (a *app) save() bool {
	c, e := a.readSettings()
	if e != nil {
		a.setStatus(e.Error())
		return false
	}
	if e = c.Validate(); e != nil {
		a.setStatus(e.Error())
		return false
	}
	if c.HasModelConnection() {
		if !a.approveRemote(&c, a.window) {
			return false
		}
		if e = c.SaveModelProfile(c.ActiveModelProfile); e != nil {
			a.setStatus(e.Error())
			return false
		}
	}
	if e = core.SaveConfig(a.configPath, c); e != nil {
		a.setStatus("Settings could not be saved: " + e.Error())
		return false
	}
	a.invalidate(false)
	a.cfg = c
	a.applyHotkeys()
	a.refreshConnectionUI()
	a.setStatus("Settings saved. " + a.connectionSummary())
	return true
}
func (a *app) setStatus(s string) {
	pSetWindowText.Call(a.statusLabel, uintptr(unsafe.Pointer(u16(s))))
}
func (a *app) post(f func()) {
	// Never block the Windows hook/message thread. Background operations can
	// wait briefly for the UI to drain; the bounded queue caps retained text.
	a.jobs <- f
	pPostMessage.Call(a.window, wmDispatch, 0, 0)
}

// Every stage of a request passes through the same UI-thread guard. In
// particular, a completed capture must not briefly show an old-window preview
// while waiting for the foreground timer to notice that focus changed.
func (a *app) postRequest(revision uint64, window uintptr, update func()) {
	a.post(func() { a.updateRequest(revision, window, foreground(), update) })
}

func (a *app) updateRequest(revision uint64, window, activeWindow uintptr, update func()) {
	// Every invalidation advances the revision. Ignore superseded callbacks
	// before checking focus so they cannot cancel a newer request.
	if a.revision.Load() != revision {
		return
	}
	if window == 0 || activeWindow != window {
		a.invalidate(false)
		return
	}
	update()
}

func (a *app) invalidate(arm bool) {
	a.revision.Add(1)
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.running = false
	a.snapshot = nil
	a.suggestion = ""
	a.candidateReady = false
	a.autoArmed = arm
	a.lastActivity = time.Now()
	a.hideOverlay()
}
func (a *app) request(manual bool) {
	if a.apiWindow != 0 {
		return
	} // Never complete from an API-key dialog.
	if err := a.cfg.CheckConsent(); err != nil {
		if manual {
			a.notify(err.Error())
		}
		return
	}
	if !manual && !a.cfg.AutomaticAllowed() {
		return
	}
	if !a.enabled {
		if manual {
			a.notify("TypeNext is paused. Click Resume in the main window, use the tray action, or press your Pause shortcut.")
		}
		return
	}
	a.invalidate(false)
	window := foreground()
	if window == 0 {
		a.setStatus("Focus a textbox before requesting a continuation.")
		return
	}
	a.lastForeground = window
	rev := a.revision.Load()
	cfg := a.cfg
	pad := a.pad
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.running = true
	if cfg.IsRemote() {
		a.lastRemoteRequest = time.Now()
	}
	a.setStatus("Reading the focused textbox…")
	go func() {
		readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
		snapshot, e := a.worker.CaptureInitial(readCtx, cfg, pad)
		readCancel()
		if e != nil {
			a.postRequest(rev, window, func() {
				a.running = false
				a.setStatus(e.Error())
				if manual {
					a.notify(e.Error())
				}
			})
			return
		}
		// Capture runs off-thread and can finish after focus moved,
		// before the UI timer or a queued hook callback has invalidated this request.
		// Never send context from that new window to the model.
		if ctx.Err() != nil || a.revision.Load() != rev || uintptr(snapshot.Window) != window || foreground() != window {
			a.postRequest(rev, window, func() { a.invalidate(false) })
			return
		}
		if strings.TrimSpace(snapshot.Prefix) == "" {
			a.postRequest(rev, window, func() {
				a.running = false
				a.setStatus("Type a few words before requesting a continuation.")
			})
			return
		}
		a.postRequest(rev, window, func() {
			a.snapshot = &snapshot
			title, detail := "Generating locally…", "The first request may need to load the model."
			if cfg.IsRemote() {
				u, _ := cfg.RequestURL()
				title, detail = "Generating via API…", "Sending approved context to "+u.Host
			}
			a.showOverlay(snapshot, cfg.Model, title+"\n"+detail, "Esc to cancel · never inserted automatically")
		})
		last := time.Time{}
		result, e := a.client.Complete(ctx, cfg, snapshot, func(partial string) {
			if partial == "" || time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			a.postRequest(rev, window, func() {
				a.showOverlay(snapshot, cfg.Model, partial, "Wait for completion · Esc to dismiss")
			})
		})
		if e != nil {
			a.postRequest(rev, window, func() {
				if errors.Is(e, context.Canceled) {
					a.invalidate(false)
					return
				}
				a.failSuggestion(snapshot, cfg.Model, "Could not finish the suggestion: "+e.Error())
			})
			return
		}
		// A second read catches edits/focus changes that did not generate a key event.
		a.postRequest(rev, window, func() {
			a.showOverlay(snapshot, cfg.Model, result, "Checking textbox · Esc to dismiss")
		})
		checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
		fresh, e := verifySuggestionContext(checkCtx, snapshot, func(ctx context.Context) (core.TextContext, error) {
			return a.worker.Capture(ctx, cfg, pad)
		})
		checkCancel()
		a.postRequest(rev, window, func() {
			a.running = false
			if e != nil {
				a.failSuggestion(snapshot, cfg.Model, "Could not verify the suggestion: "+e.Error())
				return
			}
			a.snapshot = &fresh
			a.suggestion = result
			a.candidateReady = true
			footer := a.acceptHint()
			a.showOverlay(fresh, cfg.Model, result, footer)
			a.setStatus(fmt.Sprintf("Suggestion ready for %s. %d characters before / %d after the caret.", fresh.Process, len([]rune(fresh.Prefix)), len([]rune(fresh.Suffix))))
		})
	}()
}
func (a *app) accept() {
	if !a.candidateReady || a.snapshot == nil {
		return
	}
	snapshot, text, cfg, pad := *a.snapshot, a.suggestion, a.cfg, a.pad
	acceptVK := a.hotkeys.Active(core.HotkeyAccept).VK
	if foreground() != uintptr(snapshot.Window) {
		a.invalidate(false)
		return
	}
	rev := a.revision.Load()
	a.candidateReady = false
	a.hideOverlay()
	a.running = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel = cancel
	go func() {
		defer cancel()
		e := a.worker.Insert(ctx, cfg, pad, snapshot, text, &a.revision, rev, acceptVK)
		a.post(func() {
			if a.revision.Load() == rev {
				a.invalidate(false)
				if e != nil {
					a.setStatus(e.Error())
					a.notify(e.Error())
				} else {
					a.setStatus("Continuation inserted. TypeNext did not press Enter or send a message.")
				}
			}
		})
	}()
}
func (a *app) toggle() {
	a.enabled = !a.enabled
	a.invalidate(false)
	a.refreshPauseUI()
	if a.enabled {
		a.notify("TypeNext resumed. Only approved textboxes can be read.")
	} else {
		a.notify("TypeNext paused. No textbox is being read.")
	}
}

func (a *app) refreshPauseUI() {
	if button := a.controls[idPause]; button != 0 {
		text := "Resume"
		if a.enabled {
			text = "Pause"
		}
		setControlText(button, text)
	}
}
func (a *app) tick() {
	fg := foreground()
	a.observeForeground(fg)
	if !a.inspectAt.IsZero() && time.Now().After(a.inspectAt) {
		a.inspectAt = time.Time{}
		a.inspect()
	}
	if a.automaticDue(time.Now(), false) && !modifiersDown() {
		_, process, e := processOf(fg)
		if e == nil && (fg == a.pad || a.cfg.Allows(process)) {
			// Pausing during composition must not consume the only automatic attempt.
			// Keep it armed until the IME has committed its text.
			if composing(fg) {
				return
			}
			a.autoArmed = false
			a.request(false)
		} else {
			a.autoArmed = false
		}
	}
}
func (a *app) testModel() {
	if a.testRunning {
		a.setStatus("A model test is already running.")
		return
	}
	if !a.save() {
		return
	}
	a.startModelTest()
}
func (a *app) startModelTest() {
	if a.testRunning {
		a.apiMessage("An API test is already running.")
		return
	}
	if err := a.cfg.CheckConsent(); err != nil {
		a.setStatus(err.Error())
		a.apiMessage(err.Error())
		return
	}
	a.testRunning = true
	message := "Testing with a fixed sample sentence; no app text is read. API usage may be charged."
	a.setStatus(message)
	a.apiMessage(message)
	cfg := a.cfg.Clone()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
		result, err := a.client.Complete(ctx, cfg, core.TextContext{Prefix: "The main advantage of clear writing is "}, nil)
		a.post(func() {
			a.testRunning = false
			oldURL, _ := cfg.RequestURL()
			newURL, _ := a.cfg.RequestURL()
			if newURL == nil || oldURL == nil || newURL.String() != oldURL.String() || cfg.Model != a.cfg.Model || cfg.ActiveModelProfile != a.cfg.ActiveModelProfile {
				a.apiMessage("Test finished for previous settings. Test the current connection separately.")
				return
			}
			text := "API responded: " + result
			if err != nil {
				text = "Test failed: " + err.Error()
			}
			a.setStatus(text)
			a.apiMessage(text)
		})
	}()
}
func (a *app) inspect() {
	cfg, pad := a.cfg, a.pad
	a.invalidate(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		detail, e := a.worker.Inspect(ctx, cfg, pad)
		a.post(func() {
			if e != nil {
				a.setStatus(e.Error())
				if detail == "" {
					detail = e.Error()
				}
			}
			pMessageBox.Call(a.window, uintptr(unsafe.Pointer(u16(detail))), uintptr(unsafe.Pointer(u16("TypeNext — textbox inspection"))), 0x40)
		})
	}()
}
func (a *app) openPad() {
	if a.pad != 0 {
		pShowWindow.Call(a.pad, 5)
		pSetForegroundWindow.Call(a.pad)
		pSetFocus.Call(a.padEdit)
		return
	}
	var err error
	a.pad, _, err = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("TypeNextWindow"))), uintptr(unsafe.Pointer(u16("TypeNext test pad — suggest: "+a.hotkeyLabel(core.HotkeySuggest)))), 0x00CF0000, 150, 100, uintptr(a.s(800)), uintptr(a.s(460)), 0, 0, a.instance, 0)
	if a.pad == 0 {
		a.setStatus(fmt.Sprintf("Cannot open test pad: %v", err))
		return
	}
	sample := "This is existing text in the editor. TypeNext should read it even though it was not typed through TypeNext.\r\n\r\nDifferential privacy provides a mathematical definition of privacy. One important consideration is "
	a.padEdit, err = createPadEdit(a.pad, a.instance, a.s(754), a.s(375), sample)
	if err != nil {
		pDestroyWindow.Call(a.pad)
		a.pad = 0
		a.setStatus(fmt.Sprintf("Cannot open test pad: %v", err))
		return
	}
	pSendMessage.Call(a.padEdit, 0x30, a.font, 1)
	pShowWindow.Call(a.pad, 5)
	pSetForegroundWindow.Call(a.pad)
	pSetFocus.Call(a.padEdit)
	pSendMessage.Call(a.padEdit, 0xb1, ^uintptr(0), ^uintptr(0)) // EM_SETSEL: caret at end.
}

func windowProc(w uintptr, m uint32, wp, lp uintptr) uintptr {
	a := currentApp
	if a == nil {
		v, _, _ := pDefWindowProc.Call(w, uintptr(m), wp, lp)
		return v
	}
	switch m {
	case 0x135, 0x138: // WM_CTLCOLORBTN, WM_CTLCOLORSTATIC
		if tag, _, _ := pGetWindowLongPtr.Call(lp, windowUserData); tag == statusSurfaceTag {
			pSetBkColor.Call(wp, statusBackground)
			pSetTextColor.Call(wp, statusForeground)
			pSetBkMode.Call(wp, 2) // OPAQUE: repaint changing status text cleanly.
			return a.statusBrush
		}
		// Match labels and checkbox captions to the shared window background.
		// Return a solid brush so changing text also clears its previous contents.
		pSetBkColor.Call(wp, windowBackground)
		pSetBkMode.Call(wp, 1) // TRANSPARENT
		return a.brush
	}
	if m == a.trayMessage && m != 0 {
		a.addTray()
		return 0
	}
	if w == a.overlay && w != 0 {
		switch m {
		case 0x21:
			return 4 // MA_NOACTIVATEANDEAT: clicking the preview never changes focus.
		case 0xf:
			a.paintOverlay(w)
			return 0
		case 0x14:
			return 1
		}
	}
	if w == a.apiWindow && w != 0 {
		switch m {
		case 0x10:
			a.closeAPI()
			return 0
		case 0x111:
			id, notice := int(wp&0xffff), int((wp>>16)&0xffff)
			switch id {
			case idAPIApply:
				a.saveAPI()
			case idAPITest:
				if !a.testRunning && a.saveAPI() {
					a.startModelTest()
				}
			case idAPINew:
				a.newAPIModel()
			case idAPIClose, 2:
				a.closeAPI()
			case ctrlAPIEndpoint, ctrlAPIProvider:
				if notice == 0x300 || notice == 1 {
					a.updateAPIKeyStatus()
				}
			}
			return 0
		}
	}
	if w == a.hotkeyWindow && w != 0 {
		switch m {
		case 0x10:
			a.closeShortcuts()
			return 0
		case 0x111:
			switch int(wp & 0xffff) {
			case idHotkeyApply:
				a.saveShortcuts()
			case idHotkeyDefaults:
				a.resetShortcutFields()
			case idHotkeyClose, 2:
				a.closeShortcuts() // dialog Escape
			}
			return 0
		}
	}

	if w == a.pad && w != 0 {
		if m == 0x10 {
			a.invalidate(false)
			pDestroyWindow.Call(w)
			a.pad = 0
			a.padEdit = 0
			return 0
		}
		if m == 5 && a.padEdit != 0 {
			var r rect
			pGetClientRect.Call(w, uintptr(unsafe.Pointer(&r)))
			pMoveWindow.Call(a.padEdit, 10, 10, uintptr(r.Right-20), uintptr(r.Bottom-20), 1)
			return 0
		}
	}
	if w == a.window && w != 0 {
		switch m {
		case wmDispatch:
			for {
				select {
				case f := <-a.jobs:
					f()
				default:
					return 0
				}
			}
		case wmTray:
			switch uint32(lp) {
			case 0x203:
				a.showSettings()
			case 0x205, 0x7b:
				a.trayMenu()
			}
			return 0
		case 0x312:
			switch wp {
			case 1:
				a.request(true)
			case 2:
				a.accept()
			case 3:
				a.toggle()
			}
			return 0
		case 0x113:
			a.tick()
			return 0
		case 0x111:
			id := int(wp & 0xffff)
			switch id {
			case ctrlSavedModel:
				if int((wp>>16)&0xffff) == 1 { // CBN_SELCHANGE
					a.selectModelProfile()
				}
			case idAPI:
				a.openAPI()
			case idModelRemove:
				a.removeModelProfile()
			case idHotkeys:
				a.openShortcuts()
			case idSave:
				a.save()
			case idTest:
				a.testModel()
			case idInspect:
				if a.save() {
					a.inspectAt = time.Now().Add(3 * time.Second)
					a.setStatus("Switch to the target textbox now. Inspection starts in 3 seconds and sends nothing to the model.")
				}
			case idPad:
				a.openPad()
			case idHide:
				pShowWindow.Call(a.window, 0)
				a.notify("TypeNext is in the system tray. Double-click its icon to reopen settings.")
			case idQuit:
				a.invalidate(false)
				pPostQuitMessage.Call(0)
			case idPause:
				a.toggle()
			}
			return 0
		case 0x10:
			pShowWindow.Call(a.window, 0)
			return 0
		case 2:
			pPostQuitMessage.Call(0)
			return 0
		}
	}
	v, _, _ := pDefWindowProc.Call(w, uintptr(m), wp, lp)
	return v
}

func keyboardProc(code int32, wp uintptr, k *keyboardHook) uintptr {
	a := currentApp
	if code >= 0 && a != nil && k.Flags&0x10 == 0 && a.consumeAcceptedTab(k.VK, wp) {
		return 1
	}
	if code >= 0 && a != nil && (wp == 0x100 || wp == 0x104) {
		if k.Flags&0x10 == 0 { // Ignore injected events; key contents are never stored.
			v := k.VK
			modifier := v == 0x10 || v == 0x11 || v == 0x12 || (v >= 0xa0 && v <= 0xa5) || v == 0x5b || v == 0x5c
			modifiers := currentModifiers()
			ownShortcut := a.hotkeys != nil && a.hotkeys.Matches(v, modifiers)
			if !modifier && !ownShortcut {
				if v == 0x09 && a.cfg.AcceptTab && a.candidateReady && a.snapshot != nil && modifiers == 0 && foreground() == uintptr(a.snapshot.Window) {
					// The callback must stay fast; validation and insertion run asynchronously.
					a.tabAcceptHeld = true
					pPostMessage.Call(a.window, 0x312, 2, 0)
					return 1
				}
				if v == 0x1b && a.dismissSuggestion() {
					return 1
				}
				a.keyboardActivity(v, modifiers, foreground())
			}
		}
	}
	ret, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, uintptr(unsafe.Pointer(k)))
	return ret
}

// A Tab press used for acceptance belongs to TypeNext until key-up. Otherwise
// key repeat reaches the editor and cancels insertion while Insert waits for
// the original Tab press to be released. Invalidation must not reset this flag.
func (a *app) consumeAcceptedTab(vk uint32, message uintptr) bool {
	if vk != 0x09 || !a.tabAcceptHeld {
		return false
	}
	switch message {
	case 0x100, 0x104: // WM_KEYDOWN, WM_SYSKEYDOWN
		return true
	case 0x101, 0x105: // WM_KEYUP, WM_SYSKEYUP
		a.tabAcceptHeld = false
		return true
	}
	return false
}

func mouseProc(code int32, wp, lp uintptr) uintptr {
	if code >= 0 && currentApp != nil {
		switch wp {
		case 0x201, 0x204, 0x207, 0x20a, 0x20b, 0x20e:
			currentApp.invalidate(false)
		}
	}
	ret, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return ret
}

func (a *app) trayMessageValue() {
	v, _, _ := pRegisterWindowMessage.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	a.trayMessage = uint32(v)
}
func (a *app) trayData() notifyIconData {
	d := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: a.window, ID: 1, Flags: 7, Callback: wmTray, Icon: a.smallIcon}
	copy(d.Tip[:], syscall.StringToUTF16("TypeNext — local or API completion"))
	return d
}
func (a *app) addTray()    { d := a.trayData(); pShellNotifyIcon.Call(0, uintptr(unsafe.Pointer(&d))) }
func (a *app) removeTray() { d := a.trayData(); pShellNotifyIcon.Call(2, uintptr(unsafe.Pointer(&d))) }
func (a *app) notify(s string) {
	a.setStatus(s)
	d := a.trayData()
	d.Flags = 0x10
	d.InfoFlags = 1
	copy(d.InfoTitle[:], syscall.StringToUTF16("TypeNext"))
	v := syscall.StringToUTF16(core.Head(s, 180))
	copy(d.Info[:], v)
	pShellNotifyIcon.Call(1, uintptr(unsafe.Pointer(&d)))
}
func (a *app) showSettings() { pShowWindow.Call(a.window, 9); pSetForegroundWindow.Call(a.window) }
func (a *app) trayMenu() {
	menu, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(menu)
	for _, item := range []struct {
		id   int
		text string
	}{{idSave, "Open settings"}, {idAPI, "API settings…"}, {idHotkeys, "Shortcuts…"}, {idPad, "Open test pad"}, {idPause, "Pause / resume"}, {idQuit, "Quit TypeNext"}} {
		pAppendMenu.Call(menu, 0, uintptr(item.id), uintptr(unsafe.Pointer(u16(item.text))))
	}
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	pSetForegroundWindow.Call(a.window)
	choice, _, _ := pTrackPopupMenu.Call(menu, 0x100|2, uintptr(p.X), uintptr(p.Y), 0, a.window, 0)
	if choice == idSave {
		a.showSettings()
	} else if choice != 0 {
		pPostMessage.Call(a.window, 0x111, choice, 0)
	}
	pPostMessage.Call(a.window, 0, 0, 0)
}

func (a *app) hideOverlay() {
	if a.overlay != 0 && (a.overlayModel != "" || a.overlayText != "" || a.overlayFooter != "") {
		pShowWindow.Call(a.overlay, 0)
	}
	a.overlayModel = ""
	a.overlayText = ""
	a.overlayFooter = ""
}
func (a *app) showOverlay(s core.TextContext, model, text, footer string) {
	a.overlayModel = model
	a.overlayText = text
	a.overlayFooter = footer
	if a.overlay == 0 {
		return
	}
	width := a.s(560)
	// Measure actual wrapped text in the same font used for painting.
	dc, _, _ := pGetDC.Call(a.overlay)
	old, _, _ := pSelectObject.Call(dc, a.font)
	r := rect{0, 0, int32(width - a.s(36)), 0}
	pDrawText.Call(dc, uintptr(unsafe.Pointer(u16(text))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), 0x400|0x10|0x800)
	pSelectObject.Call(dc, old)
	pReleaseDC.Call(a.overlay, dc)
	height := int(r.Bottom) + a.s(56)
	if height < a.s(80) {
		height = a.s(80)
	}
	if height > a.s(420) {
		height = a.s(420)
	}
	x, y := int(s.X), int(s.Y)+a.s(6)
	caretRect := rect{s.X, s.Y, s.X + 2, s.Y + 2}
	monitor, _, _ := pMonitorFromRect.Call(uintptr(unsafe.Pointer(&caretRect)), 2)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := pGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&mi))); ok != 0 {
		if x+width > int(mi.Work.Right) {
			x = int(mi.Work.Right) - width - 8
		}
		if x < int(mi.Work.Left) {
			x = int(mi.Work.Left) + 8
		}
		if y+height > int(mi.Work.Bottom) {
			y = int(s.Y-s.CaretHeight) - height - a.s(6)
		}
		if y < int(mi.Work.Top) {
			y = int(mi.Work.Top) + 8
		}
	}
	pSetWindowPos.Call(a.overlay, ^uintptr(0), uintptr(int64(x)), uintptr(int64(y)), uintptr(width), uintptr(height), 0x50)
	pInvalidateRect.Call(a.overlay, 0, 1)
}
func (a *app) paintOverlay(w uintptr) {
	var ps paintStruct
	dc, _, _ := pBeginPaint.Call(w, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(w, uintptr(unsafe.Pointer(&ps)))
	var bounds rect
	pGetClientRect.Call(w, uintptr(unsafe.Pointer(&bounds)))
	// Keep a visible outline against both light and dark application windows.
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&bounds)), a.overlayBorderBrush)
	border := int32(max(1, a.s(1)))
	surface := rect{border, border, bounds.Right - border, bounds.Bottom - border}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&surface)), a.overlayBrush)
	strip := rect{border, border, border + int32(a.s(4)), surface.Bottom}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&strip)), a.overlayAccentBrush)
	pSetBkMode.Call(dc, 1)
	draw := func(text string, r rect, font, color uintptr, flags uintptr) rect {
		old, _, _ := pSelectObject.Call(dc, font)
		defer pSelectObject.Call(dc, old)
		pSetTextColor.Call(dc, color)
		pDrawText.Call(dc, uintptr(unsafe.Pointer(u16(text))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), flags|0x800)
		return r
	}
	pad := int32(a.s(18))
	draw(a.overlayText, rect{pad, int32(a.s(16)), bounds.Right - pad, bounds.Bottom - int32(a.s(40))}, a.font, overlayForeground, 0x10)
	footer := rect{pad, bounds.Bottom - int32(a.s(28)), bounds.Right - pad, bounds.Bottom - int32(a.s(7))}
	// Share one quiet footer row. Reserve the instruction width first so
	// long model IDs cannot crowd out custom insertion shortcuts.
	if a.overlayModel != "" {
		measured := draw(a.overlayModel, rect{}, a.smallFont, overlayMuted, 0x20|0x400)
		hint := draw(a.overlayFooter, rect{}, a.smallFont, overlayMuted, 0x20|0x400)
		gap := int32(a.s(16))
		available := max(0, footer.Right-footer.Left-hint.Right-gap)
		modelWidth := min(measured.Right, (footer.Right-footer.Left)/3, available)
		modelRect := rect{footer.Right - modelWidth, footer.Top, footer.Right, footer.Bottom}
		draw(a.overlayModel, modelRect, a.smallFont, overlayMuted, 0x20|0x2|0x8000)
		footer.Right = modelRect.Left - gap
	}
	draw(a.overlayFooter, footer, a.smallFont, overlayMuted, 0x20|0x8000)
}

func ShowFatal(err error) {
	pMessageBox.Call(0, uintptr(unsafe.Pointer(u16(err.Error()))), uintptr(unsafe.Pointer(u16("TypeNext could not start"))), 0x10)
}

// Keep only one tray helper instance running per logon session.
func SingleInstance() (func(), bool) {
	name := u16("Local\\TypeNext-0.1")
	h, _, e := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 || e == syscall.Errno(183) {
		if h != 0 {
			pCloseHandle.Call(h)
		}
		return func() {}, false
	}
	return func() { pCloseHandle.Call(h) }, true
}
