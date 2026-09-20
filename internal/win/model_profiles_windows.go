//go:build windows && amd64

package win

import (
	"fmt"
	"strings"
	"typenext/internal/core"
	"unsafe"
)

func (a *app) refreshModelProfiles() {
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
		setControlText(a.modelDetailsLabel, fmt.Sprintf("%s  /  %s", a.cfg.Model, a.cfg.Provider))
	}
	if a.endpointLabel != 0 {
		setControlText(a.endpointLabel, a.cfg.Endpoint)
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
