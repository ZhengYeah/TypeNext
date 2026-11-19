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
	for _, id := range []int{ctrlSavedModel, idTest} {
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
	if enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(a.controls[idModelRemove]); enabled == 0 {
		return
	}
	profiles := a.cfg.ModelProfiles
	i := comboIndex(a.controls[ctrlSavedModel])
	if i < 0 || i >= len(profiles) {
		return
	}
	name := profiles[i].Name
	message := fmt.Sprintf("Remove saved model %q from TypeNext?", name)
	if len(profiles) == 1 {
		message += "\n\nSuggestions will stop until you add another connection with New model."
	} else {
		next := 0
		if i == 0 {
			next = 1
		}
		message += fmt.Sprintf("\n\n%q will become the selected model.", profiles[next].Name)
	}
	reply, _, _ := pMessageBox.Call(a.apiWindow, uintptr(unsafe.Pointer(u16(message))), uintptr(unsafe.Pointer(u16("TypeNext — remove saved model"))), 0x124) // Yes/No, default No.
	if reply == 6 {
		a.removeSavedModel(name)
	}
}

// Persist before applying the change so a write failure leaves the current
// connection and suggestion intact. Pending main-window edits stay in controls.
func (a *app) removeSavedModel(name string) {
	c := a.cfg.Clone()
	err := c.RemoveModelProfile(name)
	if err == nil {
		err = core.SaveConfig(a.configPath, c)
	}
	if err != nil {
		message := "Could not remove saved model: " + err.Error()
		a.apiMessage(message)
		a.setStatus(message)
		return
	}
	if !strings.EqualFold(c.ActiveModelProfile, a.cfg.ActiveModelProfile) {
		a.invalidate(false)
	}
	a.cfg = c
	a.closeAPI()
	a.refreshModelProfiles()
	a.setStatus("Removed " + name + ". " + a.connectionSummary())
}
