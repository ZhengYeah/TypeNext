//go:build windows && amd64

package win

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"typenext/internal/core"
	"unsafe"
)

const (
	idAPI              = 111
	idAPIApply         = 112
	idAPITest          = 113
	idAPIClose         = 114
	idAPIPreset        = 115
	ctrlAPIPreset      = 300
	ctrlAPIProvider    = 301
	ctrlAPIEndpoint    = 302
	ctrlAPIModel       = 303
	ctrlAPIKey         = 304
	ctrlAPIEnv         = 305
	ctrlAPIClear       = 306
	ctrlAPITokens      = 307
	ctrlAPITokenParam  = 308
	ctrlAPITimeout     = 309
	ctrlAPIReasoning   = 310
	ctrlAPITemperature = 311
	ctrlAPIThinking    = 312
	ctrlAPIRemote      = 313
	ctrlAPIAutoRemote  = 314
	ctrlAPIMessage     = 315
	ctrlAPIKeyStatus   = 316
)

func setControlText(w uintptr, s string) { pSetWindowText.Call(w, uintptr(unsafe.Pointer(u16(s)))) }
func setCheck(w uintptr, on bool) {
	value := uintptr(0)
	if on {
		value = 1
	}
	pSendMessage.Call(w, 0xf1, value, 0)
}
func comboIndex(w uintptr) int     { v, _, _ := pSendMessage.Call(w, 0x147, 0, 0); return int(int32(v)) }
func selectCombo(w uintptr, i int) { pSendMessage.Call(w, 0x14e, uintptr(i), 0) }

func (a *app) openAPI() {
	a.showSettings()
	if a.apiWindow != 0 {
		pSetForegroundWindow.Call(a.apiWindow)
		return
	}
	c, err := a.readSettings()
	if err != nil {
		a.setStatus(err.Error())
		return
	}
	a.apiDraft = c.Clone()
	a.inspectAt = time.Time{}
	a.invalidate(false)
	a.closeShortcuts()
	a.apiWindow, _, err = pCreateWindowEx.Call(0x00010000, uintptr(unsafe.Pointer(u16("TypeNextWindow"))),
		uintptr(unsafe.Pointer(u16("TypeNext — API settings"))), 0x00CA0000,
		120, 70, uintptr(a.s(770)), uintptr(a.s(754)), a.window, 0, a.instance, 0)
	if a.apiWindow == 0 {
		a.setStatus(fmt.Sprintf("Cannot open API settings: %v", err))
		return
	}
	control := func(class, text string, id, x, y, w, h int, style uintptr) uintptr {
		return a.controlIn(a.apiWindow, class, text, id, x, y, w, h, style)
	}
	label := func(text string, x, y, w, h int) { control("STATIC", text, 0, x, y, w, h, 0) }
	check := func(text string, id, x, y, w int, on bool) {
		setCheck(control("BUTTON", text, id, x, y, w, 26, 0x10003), on)
	}
	combo := func(id, x, y, w int, values []string, index int) {
		h := control("COMBOBOX", "", id, x, y, w, 180, 0x10003)
		for _, v := range values {
			pSendMessage.Call(h, 0x143, 0, uintptr(unsafe.Pointer(u16(v))))
		}
		selectCombo(h, index)
	}
	h := control("STATIC", "API connection", 0, 24, 18, 690, 35, 0)
	pSendMessage.Call(h, 0x30, a.heading, 1)
	label("Use a local model or an HTTPS API. Only approved textbox context is sent; requests may incur provider charges.", 24, 58, 694, 38)
	label("Preset", 24, 102, 122, 25)
	names := []string{}
	for _, p := range core.APIPresets() {
		names = append(names, p.Name)
	}
	combo(ctrlAPIPreset, 154, 98, 422, names, 0)
	control("BUTTON", "Use preset", idAPIPreset, 592, 96, 126, 30, 0x10000)
	label("Protocol", 24, 138, 122, 25)
	idx := 0
	if c.Provider == "openai-compatible" {
		idx = 1
	}
	combo(ctrlAPIProvider, 154, 134, 282, []string{"ollama", "openai-compatible"}, idx)
	label("API URL", 24, 174, 128, 25)
	control("EDIT", c.Endpoint, ctrlAPIEndpoint, 154, 170, 564, 27, 0x10080)
	label("Model ID", 24, 210, 122, 25)
	control("EDIT", c.Model, ctrlAPIModel, 154, 206, 564, 27, 0x10080)
	label("API key", 24, 246, 122, 25)
	keyBox := control("EDIT", "", ctrlAPIKey, 154, 242, 564, 27, 0x100a0) // ES_PASSWORD
	pSendMessage.Call(keyBox, 0xc5, 8192, 0)                              // EM_SETLIMITTEXT
	h = control("STATIC", "Leave blank to keep this endpoint's saved key. Keys are encrypted for your Windows account.", ctrlAPIKeyStatus, 154, 274, 564, 34, 0)
	pSendMessage.Call(h, 0x30, a.smallFont, 1)
	label("Key environment", 24, 314, 124, 25)
	control("EDIT", c.APIKeyEnv, ctrlAPIEnv, 154, 310, 306, 27, 0x10080)
	check("Remove this endpoint's key", ctrlAPIClear, 474, 309, 244, false)
	label("Output tokens", 24, 352, 122, 25)
	control("EDIT", strconv.Itoa(c.MaxTokens), ctrlAPITokens, 154, 348, 80, 27, 0x12000)
	idx = 0
	if c.TokenParameter == "max_completion_tokens" {
		idx = 1
	}
	combo(ctrlAPITokenParam, 249, 348, 226, []string{"max_tokens", "max_completion_tokens"}, idx)
	label("Timeout (s)", 492, 352, 123, 25)
	control("EDIT", strconv.Itoa(c.TimeoutSeconds), ctrlAPITimeout, 624, 348, 94, 27, 0x12000)
	label("Reasoning effort", 24, 390, 126, 25)
	efforts := []string{"(omit / provider default)", "none", "minimal", "low", "medium", "high"}
	idx = 0
	for i, v := range efforts {
		if v == c.ReasoningEffort {
			idx = i
		}
	}
	combo(ctrlAPIReasoning, 154, 386, 250, efforts, idx)
	check("Send temperature = 0.2", ctrlAPITemperature, 424, 385, 294, c.SendTemperature)
	check("Request non-thinking mode (Ollama / official DeepSeek only)", ctrlAPIThinking, 24, 424, 694, c.DisableThinking)
	check("Allow remote HTTPS API requests (sends textbox context off this PC)", ctrlAPIRemote, 24, 458, 694, c.AllowRemote)
	check("Allow automatic remote requests (unfinished text; usage charges possible)", ctrlAPIAutoRemote, 24, 489, 694, c.AllowRemoteAuto)
	label("Automatic use also needs Automatic suggestions in the main window. Remote automatic requests are spaced by at least 3 seconds; no automatic retries. Saved key takes priority over the environment variable.", 24, 525, 694, 42)
	control("BUTTON", "Apply API settings", idAPIApply, 24, 579, 173, 32, 0x10000)
	control("BUTTON", "Test API (sample)", idAPITest, 210, 579, 184, 32, 0x10000)
	control("BUTTON", "Close", idAPIClose, 598, 579, 120, 32, 0x10000)
	h = control("STATIC", "Enter the exact model ID offered by your provider. Presets do not make a network request. Native Anthropic / Responses-only APIs are not supported.", ctrlAPIMessage, 24, 626, 694, 74, 0)
	pSendMessage.Call(h, 0x30, a.smallFont, 1)
	user32.NewProc("EnableWindow").Call(a.window, 0)
	a.updateAPIKeyStatus()
	pShowWindow.Call(a.apiWindow, 5)
	pSetForegroundWindow.Call(a.apiWindow)
}

func (a *app) closeAPI() {
	if a.apiWindow == 0 {
		return
	}
	setControlText(a.controls[ctrlAPIKey], "")
	user32.NewProc("EnableWindow").Call(a.window, 1)
	w := a.apiWindow
	a.apiWindow = 0
	pDestroyWindow.Call(w)
	a.apiDraft = core.Config{}
	pSetForegroundWindow.Call(a.window)
}
func (a *app) apiMessage(s string) {
	if a.apiWindow != 0 {
		setControlText(a.controls[ctrlAPIMessage], s)
	}
}
func (a *app) apiConnectionFields() core.Config {
	c := a.apiDraft.Clone()
	c.Endpoint = strings.TrimSpace(windowText(a.controls[ctrlAPIEndpoint]))
	c.Model = strings.TrimSpace(windowText(a.controls[ctrlAPIModel]))
	c.Provider = "ollama"
	if comboIndex(a.controls[ctrlAPIProvider]) == 1 {
		c.Provider = "openai-compatible"
	}
	c.AllowRemote = checked(a.controls[ctrlAPIRemote])
	c.AllowRemoteAuto = checked(a.controls[ctrlAPIAutoRemote])
	return c
}
func (a *app) updateAPIKeyStatus() {
	if a.apiWindow == 0 {
		return
	}
	c := a.apiConnectionFields()
	c.AllowRemote = true // Only normalization for an on-screen status. Never a send.
	text := "No saved key for this endpoint. Enter one, or set the environment variable below."
	if u, err := c.RequestURL(); err == nil && c.EncryptedAPIKeys[u.String()] != "" {
		text = "A key is saved for this endpoint. Leave blank to keep it, or enter a replacement."
	}
	setControlText(a.controls[ctrlAPIKeyStatus], text)
}
func (a *app) useAPIPreset() {
	i := comboIndex(a.controls[ctrlAPIPreset])
	presets := core.APIPresets()
	if i < 0 || i >= len(presets) {
		return
	}
	p := presets[i]
	idx := 0
	if p.Provider == "openai-compatible" {
		idx = 1
	}
	selectCombo(a.controls[ctrlAPIProvider], idx)
	setControlText(a.controls[ctrlAPIEndpoint], p.Endpoint)
	setControlText(a.controls[ctrlAPIModel], p.Model)
	setControlText(a.controls[ctrlAPIEnv], p.KeyEnv)
	setControlText(a.controls[ctrlAPIKey], "")
	setCheck(a.controls[ctrlAPIClear], false)
	setCheck(a.controls[ctrlAPIRemote], false)
	setCheck(a.controls[ctrlAPIAutoRemote], false)
	setCheck(a.controls[ctrlAPITemperature], p.SendTemperature)
	setCheck(a.controls[ctrlAPIThinking], true)
	selectCombo(a.controls[ctrlAPIReasoning], 0)
	idx = 0
	if p.TokenParameter == "max_completion_tokens" {
		idx = 1
	}
	selectCombo(a.controls[ctrlAPITokenParam], idx)
	probe := a.apiConnectionFields()
	if p.Endpoint != "" && probe.IsRemote() {
		setControlText(a.controls[ctrlAPITokens], "256")
		setControlText(a.controls[ctrlAPITimeout], "60")
	}
	a.updateAPIKeyStatus()
	a.apiMessage("Preset filled in only. Enter your model ID and key; enable remote HTTPS for an external provider, then Apply or Test. No text has been sent.")
}

func (a *app) approveRemote(c *core.Config, owner uintptr) bool {
	if !c.IsRemote() {
		c.RemoteConsent = ""
		return true
	}
	u, err := c.RequestURL()
	if err != nil {
		a.setStatus(err.Error())
		a.apiMessage(err.Error())
		return false
	}
	newAuto := c.AllowRemoteAuto && (!a.cfg.AllowRemoteAuto || (c.Auto && !a.cfg.Auto))
	if c.RemoteConsent == u.String() && !newAuto {
		return true
	}
	mode := "Manual completion only: app text is sent when you request a suggestion."
	if c.AllowRemoteAuto {
		mode = "Automatic remote requests are permitted when Automatic suggestions is enabled. Pausing while typing can send unfinished text and incur charges."
	}
	text := fmt.Sprintf("Approve this remote model endpoint?\n\n%s\n\nTypeNext will send up to %d characters before and %d after the caret, plus a completion instruction. Your API key is sent to this endpoint if configured.\n\n%s\n\nThe provider may process or retain this content under its own policy. Do not use confidential documents unless you are authorized. Test API sends only a fixed sample, but may also incur charges.\n\nApprove this endpoint?", u.String(), c.PrefixChars, c.SuffixChars, mode)
	reply, _, _ := pMessageBox.Call(owner, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16("TypeNext — approve remote API"))), 0x124) // Yes/No, default No.
	if reply != 6 {
		a.apiMessage("Remote endpoint not approved. Nothing was saved or sent.")
		return false
	}
	c.RemoteConsent = u.String()
	return true
}

func (a *app) saveAPI() bool {
	c := a.apiConnectionFields()
	c.APIKeyEnv = strings.TrimSpace(windowText(a.controls[ctrlAPIEnv]))
	c.DisableThinking = checked(a.controls[ctrlAPIThinking])
	c.SendTemperature = checked(a.controls[ctrlAPITemperature])
	c.TokenParameter = "max_tokens"
	if comboIndex(a.controls[ctrlAPITokenParam]) == 1 {
		c.TokenParameter = "max_completion_tokens"
	}
	efforts := []string{"", "none", "minimal", "low", "medium", "high"}
	i := comboIndex(a.controls[ctrlAPIReasoning])
	if i >= 0 && i < len(efforts) {
		c.ReasoningEffort = efforts[i]
	}
	for _, f := range []struct {
		id  int
		dst *int
	}{{ctrlAPITokens, &c.MaxTokens}, {ctrlAPITimeout, &c.TimeoutSeconds}} {
		n, err := strconv.Atoi(strings.TrimSpace(windowText(a.controls[f.id])))
		if err != nil {
			a.apiMessage("Token budget and timeout must be whole numbers.")
			return false
		}
		*f.dst = n
	}
	if err := c.Validate(); err != nil {
		a.apiMessage(err.Error())
		return false
	}
	u, _ := c.RequestURL()
	key := strings.TrimSpace(windowText(a.controls[ctrlAPIKey]))
	remove := checked(a.controls[ctrlAPIClear])
	if key != "" && remove {
		a.apiMessage("Either enter a replacement key OR remove the saved key, not both.")
		return false
	}
	if remove {
		delete(c.EncryptedAPIKeys, u.String())
	}
	if key != "" {
		cipher, err := protectAPIKey(key, u.String())
		if err != nil {
			a.apiMessage(err.Error())
			return false
		}
		c.EncryptedAPIKeys[u.String()] = cipher
	}
	if err := c.Validate(); err != nil {
		a.apiMessage(err.Error())
		return false
	}
	if !a.approveRemote(&c, a.apiWindow) {
		return false
	}
	if err := core.SaveConfig(a.configPath, c); err != nil {
		a.apiMessage("Could not save API settings: " + err.Error())
		return false
	}
	a.invalidate(false)
	a.cfg = c
	a.apiDraft = c.Clone()
	setControlText(a.controls[ctrlAPIKey], "")
	setCheck(a.controls[ctrlAPIClear], false)
	setControlText(a.controls[ctrlEndpoint], c.Endpoint)
	setControlText(a.controls[ctrlModel], c.Model)
	idx := 0
	if c.Provider == "openai-compatible" {
		idx = 1
	}
	selectCombo(a.controls[ctrlProvider], idx)
	setCheck(a.controls[ctrlThinking], c.DisableThinking)
	a.applyHotkeys()
	a.updateAPIKeyStatus()
	a.refreshConnectionUI()
	a.setStatus("API settings saved. " + a.connectionSummary())
	a.apiMessage("Saved. Test API uses only a fixed sample; use your Suggest shortcut in an approved app for real completion.")
	return true
}

func (a *app) connectionSummary() string {
	if !a.cfg.IsRemote() {
		return "Local endpoint active. Your local server must also be configured not to forward requests."
	}
	if a.cfg.AutomaticAllowed() {
		return "Remote API active; automatic requests are enabled for approved apps."
	}
	return "Remote API active; manual completion only. Automatic remote requests are not active."
}
func (a *app) refreshConnectionUI() {
	if a.connectionLabel == 0 {
		return
	}
	text := "Local endpoint. API settings adds keys, HTTPS providers, and request options."
	if a.cfg.IsRemote() {
		text = "REMOTE API active: textbox context leaves this PC. Use API settings to change access."
	}
	setControlText(a.connectionLabel, text)
}
