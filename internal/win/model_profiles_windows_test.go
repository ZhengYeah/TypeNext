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
	for _, id := range []int{ctrlSavedModel, ctrlPause, ctrlPrefix, ctrlSuffix, ctrlAuto, ctrlTab, ctrlAllowed, idAPI, idPause, idModelRemove, idTest} {
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

func TestSavedModelRemovalPersistsAndPreservesPendingSettings(t *testing.T) {
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
	a.suggestion = "a suggestion from the removed model"
	a.candidateReady = true
	revision := a.revision.Load()

	a.removeSavedModel("Writing")
	if len(a.cfg.ModelProfiles) != 1 || a.cfg.ModelProfiles[0].Name != "Coding" || a.cfg.ActiveModelProfile != "Coding" || a.cfg.Model != "coding-model" || a.cfg.Endpoint != "http://127.0.0.1:1234/v1" || a.cfg.MaxTokens != 128 || a.cfg.DisableThinking {
		t.Fatalf("removing the active model did not select the first remaining connection: %+v", a.cfg)
	}
	if !canceled || a.suggestion != "" || a.candidateReady || a.revision.Load() <= revision {
		t.Fatal("removing the active model must cancel and invalidate its suggestion")
	}
	count, _, _ := pSendMessage.Call(a.controls[ctrlSavedModel], 0x146, 0, 0) // CB_GETCOUNT
	if count != 1 || comboIndex(a.controls[ctrlSavedModel]) != 0 || windowText(a.controls[ctrlSavedModel]) != "Coding" {
		t.Fatal("saved-model selector did not remove the old entry and select the remaining connection")
	}
	if details := windowText(a.modelDetailsLabel); !strings.Contains(details, a.cfg.Model) || !strings.Contains(details, a.cfg.Provider) {
		t.Fatalf("connection details did not refresh: %q", details)
	}
	if got := windowText(a.endpointLabel); got != a.cfg.Endpoint {
		t.Errorf("endpoint label = %q, want %q", got, a.cfg.Endpoint)
	}
	persisted, err := core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Clone(), a.cfg.Clone()) {
		t.Fatal("model removal was not persisted immediately")
	}
	for _, c := range []core.Config{a.cfg, persisted} {
		if c.DebounceMS != before.DebounceMS || c.PrefixChars != before.PrefixChars || c.SuffixChars != before.SuffixChars || c.Auto != before.Auto || c.AcceptTab != before.AcceptTab || !reflect.DeepEqual(c.AllowedApps, before.AllowedApps) {
			t.Fatal("removing a model also saved pending interaction edits")
		}
	}
	pending, err := a.readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if pending.DebounceMS != 1500 || pending.PrefixChars != 1536 || pending.SuffixChars != 350 || !pending.Auto || pending.AcceptTab || !reflect.DeepEqual(pending.AllowedApps, []string{"editor.exe", "notes.exe"}) {
		t.Fatal("model removal discarded pending interaction edits")
	}
	if !a.save() {
		t.Fatalf("save pending settings: %s", windowText(a.statusLabel))
	}
	persisted, err = core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Clone(), pending.Clone()) {
		t.Fatalf("Save settings did not preserve removal and pending edits:\ngot  %+v\nwant %+v", persisted, pending)
	}
}

func TestInactiveSavedModelRemovalPreservesActiveSuggestion(t *testing.T) {
	a := newModelProfileTestApp(t)
	// An inactive remote model must be removable without selecting it or
	// opening the endpoint approval dialog first.
	a.cfg.ModelProfiles[1].Endpoint = "https://example.com/v1"
	a.cfg.ModelProfiles[1].AllowRemote = true
	a.cfg.ModelProfiles[1].RemoteConsent = ""
	before := a.cfg.Clone()
	a.suggestion = "a suggestion from Writing"
	a.candidateReady = true
	canceled := false
	a.cancel = func() { canceled = true }
	revision := a.revision.Load()

	a.removeSavedModel("Coding")
	if len(a.cfg.ModelProfiles) != 1 || a.cfg.ModelProfiles[0].Name != "Writing" || a.cfg.ActiveModelProfile != before.ActiveModelProfile || a.cfg.Model != before.Model || a.cfg.Endpoint != before.Endpoint {
		t.Fatalf("removing an inactive model changed the active connection: %+v", a.cfg)
	}
	before.ModelProfiles = before.ModelProfiles[:1]
	if !reflect.DeepEqual(a.cfg, before) {
		t.Fatal("removing an unapproved inactive model changed configuration beyond the saved model list")
	}
	if canceled || a.suggestion != "a suggestion from Writing" || !a.candidateReady || a.revision.Load() != revision {
		t.Fatal("removing an inactive model invalidated the unchanged active model's suggestion")
	}
	persisted, err := core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Clone(), a.cfg.Clone()) {
		t.Fatal("inactive model removal was not persisted")
	}
	count, _, _ := pSendMessage.Call(a.controls[ctrlSavedModel], 0x146, 0, 0) // CB_GETCOUNT
	if count != 1 || comboIndex(a.controls[ctrlSavedModel]) != 0 || windowText(a.controls[ctrlSavedModel]) != "Writing" {
		t.Fatal("inactive removal did not refresh the saved-model selector")
	}
}

func TestSavedModelRemovalShowsUnapprovedFallbackConnection(t *testing.T) {
	a := newModelProfileTestApp(t)
	a.cfg.Auto = false
	a.cfg.ModelProfiles[1].Endpoint = "https://example.com/v1"
	a.cfg.ModelProfiles[1].AllowRemote = true
	a.cfg.ModelProfiles[1].RemoteConsent = ""

	a.removeSavedModel("Writing")
	if a.cfg.ActiveModelProfile != "Coding" || a.cfg.Endpoint != "https://example.com/v1" || !a.cfg.AllowRemote || a.cfg.RemoteConsent != "" || a.cfg.Auto {
		t.Fatalf("removal did not preserve the fallback model's missing approval: %+v", a.cfg)
	}
	consentErr := a.cfg.CheckConsent()
	if consentErr == nil || !strings.Contains(consentErr.Error(), "not been approved") {
		t.Fatalf("unapproved fallback became available for requests: %v", consentErr)
	}
	if got := windowText(a.connectionLabel); got != consentErr.Error() {
		t.Fatalf("connection label = %q, want missing-approval message %q", got, consentErr.Error())
	}
	if got := a.connectionSummary(); got != consentErr.Error() {
		t.Fatalf("connection summary = %q, want missing-approval message %q", got, consentErr.Error())
	}
	if got := windowText(a.statusLabel); !strings.Contains(got, consentErr.Error()) {
		t.Fatalf("removal status does not explain missing approval: %q", got)
	}
	persisted, err := core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ActiveModelProfile != "Coding" || persisted.RemoteConsent != "" || persisted.CheckConsent() == nil {
		t.Fatalf("persisted fallback unexpectedly acquired endpoint approval: %+v", persisted)
	}
}

func TestSavedModelRemovalFailurePreservesConfigurationAndSuggestion(t *testing.T) {
	a := newModelProfileTestApp(t)
	before := a.cfg.Clone()
	details := windowText(a.modelDetailsLabel)
	endpoint := windowText(a.endpointLabel)
	connection := windowText(a.connectionLabel)
	// A directory at the destination reliably prevents replacing the config file.
	if err := os.Mkdir(a.configPath, 0700); err != nil {
		t.Fatal(err)
	}
	setControlText(a.controls[ctrlPause], "1700")
	a.suggestion = "the existing suggestion"
	a.candidateReady = true
	canceled := false
	a.cancel = func() { canceled = true }
	revision := a.revision.Load()

	a.removeSavedModel("Writing")
	if !reflect.DeepEqual(a.cfg, before) {
		t.Fatal("failed persistence mutated the active or saved model configuration")
	}
	count, _, _ := pSendMessage.Call(a.controls[ctrlSavedModel], 0x146, 0, 0) // CB_GETCOUNT
	if count != 2 || comboIndex(a.controls[ctrlSavedModel]) != 0 || windowText(a.controls[ctrlSavedModel]) != "Writing" || windowText(a.modelDetailsLabel) != details || windowText(a.endpointLabel) != endpoint || windowText(a.connectionLabel) != connection {
		t.Fatal("failed removal changed the original selector or connection details")
	}
	if windowText(a.controls[ctrlPause]) != "1700" {
		t.Fatal("failed removal discarded pending main-window edits")
	}
	if canceled || a.suggestion != "the existing suggestion" || !a.candidateReady || a.revision.Load() != revision {
		t.Fatal("failed removal invalidated the unchanged active model's suggestion")
	}
	if !strings.Contains(windowText(a.statusLabel), "Could not remove saved model") {
		t.Fatalf("missing removal failure message: %q", windowText(a.statusLabel))
	}
}

func TestLastSavedModelRemovalLeavesEmptyConnectionAndAllowsSettingsSave(t *testing.T) {
	a := newModelProfileTestApp(t)
	a.removeSavedModel("Coding")
	setControlText(a.controls[ctrlPause], "1700")
	a.removeSavedModel("Writing")

	if len(a.cfg.ModelProfiles) != 0 || a.cfg.ActiveModelProfile != "" || a.cfg.HasModelConnection() {
		t.Fatalf("last removal retained a model connection: %+v", a.cfg)
	}
	count, _, _ := pSendMessage.Call(a.controls[ctrlSavedModel], 0x146, 0, 0) // CB_GETCOUNT
	if count != 0 || comboIndex(a.controls[ctrlSavedModel]) != -1 {
		t.Fatal("last removal left a saved-model selector entry")
	}
	for _, id := range []int{ctrlSavedModel, idModelRemove, idTest} {
		if enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(a.controls[id]); enabled != 0 {
			t.Errorf("control %d should be disabled without a saved model", id)
		}
	}
	if summary := strings.ToLower(a.connectionSummary()); !strings.Contains(summary, "no saved model") {
		t.Fatalf("missing empty connection summary: %q", summary)
	}
	if details := strings.ToLower(windowText(a.modelDetailsLabel)); !strings.Contains(details, "no saved model") {
		t.Fatalf("missing empty saved model details: %q", details)
	}
	if endpoint := windowText(a.endpointLabel); endpoint != "" {
		t.Fatalf("empty connection still displays an endpoint: %q", endpoint)
	}
	persisted, err := core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ModelProfiles == nil || len(persisted.ModelProfiles) != 0 || persisted.HasModelConnection() || persisted.ActiveModelProfile != "" {
		t.Fatalf("reloading last removal recreated a saved model: %+v", persisted)
	}
	if !a.save() {
		t.Fatalf("saving main settings without a saved model failed: %s", windowText(a.statusLabel))
	}
	persisted, err = core.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.DebounceMS != 1700 || persisted.ModelProfiles == nil || len(persisted.ModelProfiles) != 0 || persisted.HasModelConnection() || persisted.ActiveModelProfile != "" {
		t.Fatalf("saving pending settings recreated a removed model or dropped edits: %+v", persisted)
	}
	if err := a.cfg.SaveModelProfile("Readded"); err != nil {
		t.Fatal(err)
	}
	a.refreshModelProfiles()
	for _, id := range []int{ctrlSavedModel, idModelRemove, idTest} {
		if enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(a.controls[id]); enabled == 0 {
			t.Errorf("control %d was not reenabled after saving a new model", id)
		}
	}
	if comboIndex(a.controls[ctrlSavedModel]) != 0 || windowText(a.controls[ctrlSavedModel]) != "Readded" {
		t.Fatal("saving a new model did not restore the saved-model selector")
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
	title, button, selector, remove := bounds(heading), bounds(a.controls[idAPI]), bounds(a.controls[ctrlSavedModel]), bounds(a.controls[idModelRemove])
	if button.Top < title.Bottom || selector.Top < title.Bottom || remove.Top < title.Bottom {
		t.Fatalf("model connection controls must sit below the connection heading: heading=%+v button=%+v selector=%+v remove=%+v", title, button, selector, remove)
	}
	if remove.Left <= selector.Right || button.Left <= remove.Right {
		t.Fatalf("remove model must have gaps between the selector and API settings: button=%+v selector=%+v remove=%+v", button, selector, remove)
	}
	// Native combo boxes and buttons have slightly different rendered heights.
	tolerance := int32(a.s(4))
	for _, b := range []rect{button, remove} {
		centerOffset := (b.Top+b.Bottom)/2 - (selector.Top+selector.Bottom)/2
		if b.Top >= selector.Bottom || b.Bottom <= selector.Top || centerOffset < -tolerance || centerOffset > tolerance {
			t.Fatalf("model connection buttons must align with the saved model selector's row: button=%+v selector=%+v", b, selector)
		}
	}
}
