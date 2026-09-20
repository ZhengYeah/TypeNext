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
	idAPINew           = 116
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
	ctrlAPIProfileName = 317
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
	a.apiWindow, err = a.createSettingsWindow("TypeNext — API settings", 120, 70, 880, 784, a.window)
	if a.apiWindow == 0 {
		a.setStatus(fmt.Sprintf("Cannot open API settings: %v", err))
		return
	}
	control := func(class, text string, id, x, y, w, h int, style uintptr) uintptr {
		return a.controlIn(a.apiWindow, class, text, id, x, y, w, h, style)
	}
	label := func(text string, x, y, w, h int) { control("STATIC", text, 0, x, y, w, h, 0) }
	note := func(text string, x, y, w, h int) {
		label := control("STATIC", text, 0, x, y, w, h, 0)
		pSendMessage.Call(label, 0x30, a.smallFont, 1)
	}
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
	h := control("STATIC", "API connection", 0, 24, 24, 832, 32, 0)
	pSendMessage.Call(h, 0x30, a.heading, 1)
	label("Connect a local model or HTTPS API. Choose saved models on the main page.", 24, 64, 832, 22)
	a.separatorIn(a.apiWindow, 24, 96, 832)

	label("1  Connection", 24, 118, 832, 24)
	label("Saved name", 24, 156, 120, 24)
	nameBox := control("EDIT", c.ActiveModelProfile, ctrlAPIProfileName, 156, 152, 556, 28, 0x10080)
	pSendMessage.Call(nameBox, 0xc5, 80, 0) // EM_SETLIMITTEXT
	control("BUTTON", "New model", idAPINew, 728, 150, 128, 32, 0x10000)
	label("Protocol", 24, 196, 120, 24)
	idx := 0
	if c.Provider == "openai-compatible" {
		idx = 1
	}
	combo(ctrlAPIProvider, 156, 192, 236, []string{"ollama", "openai-compatible"}, idx)
	label("Model ID", 416, 196, 96, 24)
	control("EDIT", c.Model, ctrlAPIModel, 524, 192, 332, 28, 0x10080)
	label("API URL", 24, 236, 120, 24)
	control("EDIT", c.Endpoint, ctrlAPIEndpoint, 156, 232, 700, 28, 0x10080)

	label("2  Authentication", 24, 276, 832, 24)
	label("API key", 24, 312, 120, 24)
	keyBox := control("EDIT", "", ctrlAPIKey, 156, 308, 700, 28, 0x100a0) // ES_PASSWORD
	pSendMessage.Call(keyBox, 0xc5, 8192, 0)                              // EM_SETLIMITTEXT
	a.statusText(a.apiWindow, "", ctrlAPIKeyStatus, 156, 348, 700, 44)
	label("Key environment", 24, 412, 120, 24)
	control("EDIT", c.APIKeyEnv, ctrlAPIEnv, 156, 408, 364, 28, 0x10080)
	check("Remove this endpoint's key", ctrlAPIClear, 544, 408, 312, false)

	// Two option groups share the remaining space instead of extending the form
	// into a long stack. Match the main window's margins and section spacing.
	label("3  Request options", 24, 464, 404, 24)
	label("Output tokens", 24, 504, 100, 24)
	control("EDIT", strconv.Itoa(c.MaxTokens), ctrlAPITokens, 132, 500, 64, 28, 0x12000)
	idx = 0
	if c.TokenParameter == "max_completion_tokens" {
		idx = 1
	}
	combo(ctrlAPITokenParam, 208, 500, 220, []string{"max_tokens", "max_completion_tokens"}, idx)
	label("Timeout (s)", 24, 544, 88, 24)
	control("EDIT", strconv.Itoa(c.TimeoutSeconds), ctrlAPITimeout, 120, 540, 64, 28, 0x12000)
	label("Reasoning", 196, 544, 88, 24)
	efforts := []string{"(default)", "none", "minimal", "low", "medium", "high"}
	idx = 0
	for i, v := range efforts {
		if v == c.ReasoningEffort {
			idx = i
		}
	}
	combo(ctrlAPIReasoning, 292, 540, 136, efforts, idx)
	check("Non-thinking mode (Ollama / official DeepSeek)", ctrlAPIThinking, 24, 584, 404, c.DisableThinking)
	check("Send temperature = 0.2", ctrlAPITemperature, 24, 616, 404, c.SendTemperature)

	label("4  Remote access", 456, 464, 400, 24)
	check("Allow remote HTTPS API requests", ctrlAPIRemote, 456, 500, 400, c.AllowRemote)
	note("Sends textbox context off this PC.", 480, 532, 376, 20)
	check("Allow automatic remote requests", ctrlAPIAutoRemote, 456, 564, 400, c.AllowRemoteAuto)
	note("Enable Automatic suggestions on the main page.\r\nSends unfinished text; usage charges may apply.\r\nAt least 3 seconds apart, with no retries.", 480, 596, 376, 48)

	a.separatorIn(a.apiWindow, 24, 656, 832)
	control("BUTTON", "Save model", idAPIApply, 24, 672, 176, 32, 0x10000)
	control("BUTTON", "Test API (sample)", idAPITest, 212, 672, 184, 32, 0x10000)
	control("BUTTON", "Close", idAPIClose, 736, 672, 120, 32, 0x10000)
	a.statusText(a.apiWindow, "A new saved name adds a model; an existing name updates it. Native Anthropic and Responses-only APIs are not supported.", ctrlAPIMessage, 24, 716, 832, 44)
	user32.NewProc("EnableWindow").Call(a.window, 0)
	a.updateAPIKeyStatus()
	if !c.HasModelConnection() {
		a.newAPIModel()
	}
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
	text := "No saved key for this endpoint. Enter one, or use the environment variable below."
	if u, err := c.RequestURL(); err == nil && c.EncryptedAPIKeys[u.String()] != "" {
		text = "A key is saved for this endpoint. Leave blank to keep it, or enter a replacement."
	}
	text += "\r\nSaved keys are encrypted for your Windows account and take priority over the environment variable."
	setControlText(a.controls[ctrlAPIKeyStatus], text)
}
func (a *app) newAPIModel() {
	if a.apiWindow == 0 {
		return
	}
	c := core.DefaultConfig()
	// Only connection fields are reset. Pending main-window preferences and
	// encrypted endpoint keys remain in the draft until the model is saved.
	a.apiDraft.RemoteConsent = ""
	setControlText(a.controls[ctrlAPIProfileName], "")
	selectCombo(a.controls[ctrlAPIProvider], 0)
	setControlText(a.controls[ctrlAPIEndpoint], "")
	setControlText(a.controls[ctrlAPIModel], "")
	setControlText(a.controls[ctrlAPIEnv], c.APIKeyEnv)
	setControlText(a.controls[ctrlAPIKey], "")
	setCheck(a.controls[ctrlAPIClear], false)
	setControlText(a.controls[ctrlAPITokens], strconv.Itoa(c.MaxTokens))
	setControlText(a.controls[ctrlAPITimeout], strconv.Itoa(c.TimeoutSeconds))
	selectCombo(a.controls[ctrlAPITokenParam], 0)
	selectCombo(a.controls[ctrlAPIReasoning], 0)
	setCheck(a.controls[ctrlAPITemperature], c.SendTemperature)
	setCheck(a.controls[ctrlAPIThinking], c.DisableThinking)
	setCheck(a.controls[ctrlAPIRemote], false)
	setCheck(a.controls[ctrlAPIAutoRemote], false)
	a.updateAPIKeyStatus()
	a.apiMessage("Enter a saved name, choose the protocol, and fill in the API URL, model ID and key. Save model adds it to the main page's list and selects it.")
	pSetFocus.Call(a.controls[ctrlAPIProfileName])
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
	name := strings.TrimSpace(windowText(a.controls[ctrlAPIProfileName]))
	if name == "" {
		a.apiMessage("Enter a saved name for this model, such as Local Qwen or DeepSeek.")
		pSetFocus.Call(a.controls[ctrlAPIProfileName])
		return false
	}
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
	if err := c.SaveModelProfile(name); err != nil {
		a.apiMessage(err.Error())
		return false
	}
	if err := core.SaveConfig(a.configPath, c); err != nil {
		a.apiMessage("Could not save model: " + err.Error())
		return false
	}
	a.invalidate(false)
	a.cfg = c
	a.apiDraft = c.Clone()
	setControlText(a.controls[ctrlAPIProfileName], c.ActiveModelProfile)
	setControlText(a.controls[ctrlAPIKey], "")
	setCheck(a.controls[ctrlAPIClear], false)
	a.applyHotkeys()
	a.updateAPIKeyStatus()
	a.refreshModelProfiles()
	a.setStatus("Model saved. " + a.connectionSummary())
	a.apiMessage("Saved and selected on the main page. Test API uses only a fixed sample; use your Suggest shortcut in an approved app for real completion.")
	return true
}

func (a *app) connectionSummary() string {
	if !a.cfg.HasModelConnection() {
		return "No saved model. Open API settings to add a model."
	}
	if reason := automaticBlockReason(a.cfg); reason != "" {
		return reason
	}
	if err := a.cfg.CheckConsent(); err != nil {
		return err.Error()
	}
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
	if err := a.cfg.CheckConsent(); err != nil {
		text = err.Error()
	}
	if reason := automaticBlockReason(a.cfg); reason != "" {
		text = reason
	}
	if !a.cfg.HasModelConnection() {
		text = a.connectionSummary()
	}
	setControlText(a.connectionLabel, text)
}
