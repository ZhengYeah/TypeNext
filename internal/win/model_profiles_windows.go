//go:build windows && amd64

package win

import (
	"fmt"
	"strings"
	"typenext/internal/core"
	"unsafe"
)

func (a *app) refreshModelProfiles() {
	hasModels := len(a.cfg.ModelProfiles) > 0
	for _, id := range []int{ctrlSavedModel, idModelRemove, idTest} {
		if control := a.controls[id]; control != 0 {
			enabled := uintptr(0)
			if hasModels {
				enabled = 1
			}
			user32.NewProc("EnableWindow").Call(control, enabled)
		}
	}
	combo := a.controls[ctrlSavedModel]
	if combo != 0 {
		pSendMessage.Call(combo, 0x14b, 0, 0) // CB_RESETCONTENT
		selected := -1
		for i, profile := range a.cfg.ModelProfiles {
			pSendMessage.Call(combo, 0x143, 0, uintptr(unsafe.Pointer(u16(profile.Name))))
			if strings.EqualFold(profile.Name, a.cfg.ActiveModelProfile) {
				selected = i
			}
		}
		selectCombo(combo, selected)
	}
	if a.modelDetailsLabel != 0 {
		text := "No saved model"
		if hasModels {
			text = fmt.Sprintf("%s  /  %s", a.cfg.Model, a.cfg.Provider)
		}
		setControlText(a.modelDetailsLabel, text)
	}
	if a.endpointLabel != 0 {
		endpoint := ""
		if hasModels {
			endpoint = a.cfg.Endpoint
		}
		setControlText(a.endpointLabel, endpoint)
	}
	a.refreshConnectionUI()
}

// Choosing a saved model takes effect immediately. Other edits in the main
// window remain in their controls until Save settings is clicked.
func (a *app) selectModelProfile() {
	i := comboIndex(a.controls[ctrlSavedModel])
	if i < 0 || i >= len(a.cfg.ModelProfiles) {
		return
	}
	name := a.cfg.ModelProfiles[i].Name
	if strings.EqualFold(name, a.cfg.ActiveModelProfile) {
		return
	}
	c := a.cfg.Clone()
	if err := c.UseModelProfile(name); err != nil {
		a.refreshModelProfiles()
		a.setStatus(err.Error())
		return
	}
	if err := c.Validate(); err != nil {
		a.refreshModelProfiles()
		a.setStatus(err.Error())
		return
	}
	if c.CheckConsent() != nil && !a.approveRemote(&c, a.window) {
		a.refreshModelProfiles()
		a.setStatus("Model selection was not changed because the endpoint was not approved.")
		return
	}
	if err := c.SaveModelProfile(c.ActiveModelProfile); err != nil {
		a.refreshModelProfiles()
		a.setStatus(err.Error())
		return
	}
	if err := core.SaveConfig(a.configPath, c); err != nil {
		a.refreshModelProfiles()
		a.setStatus("Could not select saved model: " + err.Error())
		return
	}
	a.invalidate(false)
	a.cfg = c
	a.refreshModelProfiles()
	a.setStatus("Selected " + name + ". " + a.connectionSummary())
}

func (a *app) removeModelProfile() {
	profiles := a.cfg.ModelProfiles
	if len(profiles) == 0 {
		return
	}
	// A separate chooser lets inactive models be removed without first
	// activating them or approving their remote endpoint.
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		a.setStatus("Could not open the saved model removal menu.")
		return
	}
	defer pDestroyMenu.Call(menu)
	for i, profile := range profiles {
		label := strings.ReplaceAll(profile.Name, "&", "&&")
		if ok, _, _ := pAppendMenu.Call(menu, 0, uintptr(i+1), uintptr(unsafe.Pointer(u16(label)))); ok == 0 {
			a.setStatus("Could not open the saved model removal menu.")
			return
		}
	}
	var bounds rect
	if ok, _, _ := user32.NewProc("GetWindowRect").Call(a.controls[idModelRemove], uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		a.setStatus("Could not position the saved model removal menu.")
		return
	}
	choice, _, _ := pTrackPopupMenu.Call(menu, 0x100, uintptr(bounds.Left), uintptr(bounds.Bottom), 0, a.window, 0) // TPM_RETURNCMD
	if choice == 0 || choice > uintptr(len(profiles)) {
		return
	}
	i := int(choice) - 1
	name := profiles[i].Name
	message := fmt.Sprintf("Remove saved model %q from TypeNext?", name)
	if len(profiles) == 1 {
		message += "\n\nSuggestions will stop until you add another model in API settings."
	} else if strings.EqualFold(name, a.cfg.ActiveModelProfile) {
		next := 0
		if i == 0 {
			next = 1
		}
		message += fmt.Sprintf("\n\n%q will become the selected model.", profiles[next].Name)
	}
	reply, _, _ := pMessageBox.Call(a.window, uintptr(unsafe.Pointer(u16(message))), uintptr(unsafe.Pointer(u16("TypeNext — remove saved model"))), 0x124) // Yes/No, default No.
	if reply == 6 {
		a.removeSavedModel(name)
	}
}

// Persist before applying the change so a write failure leaves the current
// connection and suggestion intact. Pending main-window edits stay in controls.
func (a *app) removeSavedModel(name string) {
	c := a.cfg.Clone()
	if err := c.RemoveModelProfile(name); err != nil {
		a.setStatus(err.Error())
		return
	}
	if err := core.SaveConfig(a.configPath, c); err != nil {
		a.setStatus("Could not remove saved model: " + err.Error())
		return
	}
	if !strings.EqualFold(c.ActiveModelProfile, a.cfg.ActiveModelProfile) {
		a.invalidate(false)
	}
	a.cfg = c
	a.refreshModelProfiles()
	a.setStatus("Removed " + name + ". " + a.connectionSummary())
}
