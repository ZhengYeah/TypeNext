//go:build windows && amd64

package win

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"typenext/internal/core"
	"unsafe"
)

// Saving main-window settings must not register global shortcuts in these tests.
type modelProfileTestRegistrar struct{}

func (modelProfileTestRegistrar) Register(int, core.Hotkey) error { return nil }
func (modelProfileTestRegistrar) Unregister(int) error            { return nil }

func newModelProfileTestApp(t *testing.T) *app {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	inst, _, _ := pGetModuleHandle.Call(0)
	parent, _, err := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("STATIC"))), 0, 0x00cf0000, 0, 0, 800, 850, 0, 0, inst, 0)
	if parent == 0 {
		t.Fatalf("create hidden settings parent: %v", err)
	}
	t.Cleanup(func() { pDestroyWindow.Call(parent) })
	c := core.DefaultConfig()
	if err := c.SaveModelProfile("Writing"); err != nil {
		t.Fatal(err)
	}
	c.Provider = "openai-compatible"
	c.Endpoint = "http://127.0.0.1:1234/v1"
	c.Model = "coding-model"
	c.MaxTokens = 128
	c.DisableThinking = false
	if err := c.SaveModelProfile("Coding"); err != nil {
		t.Fatal(err)
	}
	if err := c.UseModelProfile("Writing"); err != nil {
		t.Fatal(err)
	}
	a := &app{
		window: parent, instance: inst, cfg: c, controls: map[int]uintptr{}, scale: 1, enabled: true,
		configPath: filepath.Join(t.TempDir(), "config.json"),
		hotkeys:    core.NewHotkeySet(modelProfileTestRegistrar{}),
	}
	a.buildSettings()
	a.refreshModelProfiles()
	for _, id := range []int{ctrlSavedModel, ctrlPause, ctrlPrefix, ctrlSuffix, ctrlAuto, ctrlTab, ctrlAllowed, idAPI, idPause} {
		if a.controls[id] == 0 {
			t.Fatalf("settings control %d was not created", id)
		}
	}
	if a.modelDetailsLabel == 0 || a.endpointLabel == 0 || a.connectionLabel == 0 || a.statusLabel == 0 {
		t.Fatal("settings status labels were not created")
	}
	return a
}

func TestSavedModelSelectionPersistsAndPreservesPendingSettings(t *testing.T) {
	a := newModelProfileTestApp(t)
	before := a.cfg.Clone()
	if err := core.SaveConfig(a.configPath, a.cfg); err != nil {
		t.Fatal(err)
	}
	setControlText(a.controls[ctrlPause], "1500")
	setControlText(a.controls[ctrlPrefix], "1536")
	setControlText(a.controls[ctrlSuffix], "350")
	setControlText(a.controls[ctrlAllowed], "editor.exe\r\nnotes.exe")
	setCheck(a.controls[ctrlAuto], true)
	setCheck(a.controls[ctrlTab], false)
	canceled := false
	a.cancel = func() { canceled = true }
	a.suggestion = "a suggestion from the previous model"
	a.candidateReady = true

	selectCombo(a.controls[ctrlSavedModel], 1)
	a.selectModelProfile()
	if a.cfg.ActiveModelProfile != "Coding" || a.cfg.Model != "coding-model" || a.cfg.Provider != "openai-compatible" || a.cfg.MaxTokens != 128 || a.cfg.DisableThinking {
		t.Fatalf("selected connection was not applied: %+v", a.cfg)
	}
	if !canceled || a.suggestion != "" || a.candidateReady {
		t.Fatal("changing the active model must cancel and clear the previous suggestion")
	}
	if comboIndex(a.controls[ctrlSavedModel]) != 1 || windowText(a.controls[ctrlSavedModel]) != "Coding" {
		t.Fatal("saved-model selector does not show the active connection")
	}
	details := windowText(a.modelDetailsLabel)
	for _, want := range []string{a.cfg.Model, a.cfg.Provider} {
		if !strings.Contains(details, want) {
			t.Errorf("connection details %q do not contain %q", details, want)
		}
	}
	if got := windowText(a.endpointLabel); got != a.cfg.Endpoint {
		t.Errorf("endpoint label = %q, want %q", got, a.cfg.Endpoint)
	}
	count, _, _ := pSendMessage.Call(a.controls[ctrlSavedModel], 0x146, 0, 0) // CB_GETCOUNT
	if count != 2 {
		t.Fatalf("refresh duplicated or dropped saved models: count = %d", count)
	}
	persisted, err := core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ActiveModelProfile != "Coding" || persisted.Model != a.cfg.Model || persisted.Endpoint != a.cfg.Endpoint {
		t.Fatal("model selection was not persisted immediately")
	}
	for _, c := range []core.Config{a.cfg, persisted} {
		if c.DebounceMS != before.DebounceMS || c.PrefixChars != before.PrefixChars || c.SuffixChars != before.SuffixChars || c.Auto != before.Auto || c.AcceptTab != before.AcceptTab || !reflect.DeepEqual(c.AllowedApps, before.AllowedApps) {
			t.Fatal("selecting a model also saved pending interaction edits")
		}
	}
	pending, err := a.readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if pending.DebounceMS != 1500 || pending.PrefixChars != 1536 || pending.SuffixChars != 350 || !pending.Auto || pending.AcceptTab || !reflect.DeepEqual(pending.AllowedApps, []string{"editor.exe", "notes.exe"}) {
		t.Fatal("model selection discarded pending interaction edits")
	}
	if !a.save() {
		t.Fatalf("save pending settings: %s", windowText(a.statusLabel))
	}
	persisted, err = core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Clone(), pending.Clone()) {
		t.Fatalf("Save settings did not persist the chosen model and pending edits:\ngot  %+v\nwant %+v", persisted, pending)
	}
}

func TestSavedModelSelectionFailureRestoresActiveConnection(t *testing.T) {
	a := newModelProfileTestApp(t)
	before := a.cfg.Clone()
	details := windowText(a.modelDetailsLabel)
	endpoint := windowText(a.endpointLabel)
	// A directory at the destination reliably prevents replacing the config file.
	if err := os.Mkdir(a.configPath, 0700); err != nil {
		t.Fatal(err)
	}
	setControlText(a.controls[ctrlPause], "1700")
	a.suggestion = "the existing suggestion"
	a.candidateReady = true
	canceled := false
	a.cancel = func() { canceled = true }
	selectCombo(a.controls[ctrlSavedModel], 1)
	a.selectModelProfile()
	if !reflect.DeepEqual(a.cfg, before) {
		t.Fatal("failed persistence mutated the active or saved model configuration")
	}
	if comboIndex(a.controls[ctrlSavedModel]) != 0 || windowText(a.controls[ctrlSavedModel]) != "Writing" || windowText(a.modelDetailsLabel) != details || windowText(a.endpointLabel) != endpoint {
		t.Fatal("failed persistence did not restore the original selection and details")
	}
	if windowText(a.controls[ctrlPause]) != "1700" {
		t.Fatal("failed selection discarded pending main-window edits")
	}
	if canceled || a.suggestion != "the existing suggestion" || !a.candidateReady {
		t.Fatal("failed selection invalidated the unchanged active model's suggestion")
	}
	if !strings.Contains(windowText(a.statusLabel), "Could not select saved model") {
		t.Fatalf("missing selection failure message: %q", windowText(a.statusLabel))
	}
}

func TestModelConnectionControlsDoNotOverlap(t *testing.T) {
	a := newModelProfileTestApp(t)
	heading, _, err := user32.NewProc("FindWindowExW").Call(a.window, 0, uintptr(unsafe.Pointer(u16("STATIC"))), uintptr(unsafe.Pointer(u16("1  Connect a model"))))
	if heading == 0 {
		t.Fatalf("find connection section heading: %v", err)
	}
	bounds := func(w uintptr) rect {
		t.Helper()
		var r rect
		if ok, _, err := user32.NewProc("GetWindowRect").Call(w, uintptr(unsafe.Pointer(&r))); ok == 0 {
			t.Fatalf("get control bounds: %v", err)
		}
		return r
	}
	title, button, selector := bounds(heading), bounds(a.controls[idAPI]), bounds(a.controls[ctrlSavedModel])
	if button.Top < title.Bottom || selector.Top < title.Bottom {
		t.Fatalf("API settings and the saved model selector must sit below the connection heading: heading=%+v button=%+v selector=%+v", title, button, selector)
	}
	if button.Left <= selector.Right {
		t.Fatalf("API settings must have a gap to the right of the saved model selector: button=%+v selector=%+v", button, selector)
	}
	// Native combo boxes and buttons have slightly different rendered heights.
	centerOffset := (button.Top+button.Bottom)/2 - (selector.Top+selector.Bottom)/2
	tolerance := int32(a.s(4))
	if button.Top >= selector.Bottom || button.Bottom <= selector.Top || centerOffset < -tolerance || centerOffset > tolerance {
		t.Fatalf("API settings must align with the saved model selector's row: button=%+v selector=%+v", button, selector)
	}
}
