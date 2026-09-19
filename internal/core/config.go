// Package core contains the platform-independent, testable TypeNext logic.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.1.5-preview"

type Config struct {
	// Remote networking is opt-in. Consent and encrypted keys are bound to the
	// canonical request URL, so changing providers cannot reuse either silently.
	AllowRemote      bool              `json:"allow_remote_api"`
	AllowRemoteAuto  bool              `json:"allow_automatic_remote_requests"`
	RemoteConsent    string            `json:"approved_remote_endpoint,omitempty"`
	EncryptedAPIKeys map[string]string `json:"encrypted_api_keys,omitempty"`
	TokenParameter   string            `json:"token_parameter"`
	SendTemperature  bool              `json:"send_temperature"`
	ReasoningEffort  string            `json:"reasoning_effort,omitempty"`

	SuggestHotkey      string   `json:"suggest_hotkey"`
	AcceptHotkey       string   `json:"accept_hotkey"`
	PauseHotkey        string   `json:"pause_hotkey"`
	Provider           string   `json:"provider"`
	Endpoint           string   `json:"endpoint"`
	Model              string   `json:"model"`
	Auto               bool     `json:"automatic_suggestions"`
	AcceptTab          bool     `json:"accept_with_tab"`
	DisableThinking    bool     `json:"disable_thinking"`
	DebounceMS         int      `json:"debounce_ms"`
	PrefixChars        int      `json:"prefix_characters"`
	SuffixChars        int      `json:"suffix_characters"`
	MaxTokens          int      `json:"max_tokens"`
	MaxSuggestionChars int      `json:"max_suggestion_characters"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
	AllowedApps        []string `json:"allowed_apps"`
	APIKeyEnv          string   `json:"api_key_environment_variable,omitempty"`
}

func DefaultConfig() Config {
	return Config{SuggestHotkey: "Ctrl+Shift+F9", AcceptHotkey: "Ctrl+Shift+F10", PauseHotkey: "Ctrl+Shift+F11",
		TokenParameter: "max_tokens", SendTemperature: true,
		Provider: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen3:4b",
		DisableThinking: true, DebounceMS: 750, PrefixChars: 1000, SuffixChars: 200,
		MaxTokens: 64, MaxSuggestionChars: 320, TimeoutSeconds: 45,
		AllowedApps: []string{"notepad.exe", "winword.exe", "powerpnt.exe", "typora.exe", "wechat.exe", "weixin.exe"},
		APIKeyEnv:   "TYPENEXT_API_KEY"}
}

func (c Config) Validate() error {
	if _, err := c.HotkeyBindings(); err != nil {
		return err
	}
	if c.Provider != "ollama" && c.Provider != "openai-compatible" {
		return errors.New("provider must be ollama or openai-compatible")
	}
	if _, e := ServerBase(c.Endpoint, c.AllowRemote); e != nil {
		return e
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("model name is required")
	}
	if len(c.Model) > 200 || strings.ContainsAny(c.Model, "\r\n\x00") {
		return errors.New("invalid model name")
	}
	if c.Provider == "ollama" && strings.HasSuffix(strings.ToLower(c.Model), "cloud") {
		return errors.New("choose a locally installed model, not a cloud model")
	}
	if c.DebounceMS < 300 || c.DebounceMS > 10000 {
		return errors.New("pause must be 300–10000 milliseconds")
	}
	if c.PrefixChars < 32 || c.PrefixChars > 8000 {
		return errors.New("prefix length must be 32–8000 characters")
	}
	if c.SuffixChars < 0 || c.SuffixChars > 2000 {
		return errors.New("suffix length must be 0–2000 characters")
	}
	if c.MaxTokens < 8 || c.MaxTokens > 4096 {
		return errors.New("max_tokens must be 8–4096")
	}
	if c.MaxSuggestionChars < 16 || c.MaxSuggestionChars > 1000 {
		return errors.New("max_suggestion_characters must be 16–1000")
	}
	if c.TimeoutSeconds < 5 || c.TimeoutSeconds > 180 {
		return errors.New("timeout_seconds must be 5–180")
	}
	if c.TokenParameter != "max_tokens" && c.TokenParameter != "max_completion_tokens" {
		return errors.New("token_parameter must be max_tokens or max_completion_tokens")
	}
	switch c.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high":
	default:
		return errors.New("reasoning_effort must be blank, none, minimal, low, medium, or high")
	}
	if len(c.APIKeyEnv) > 200 || strings.ContainsAny(c.APIKeyEnv, "=\r\n\x00") {
		return errors.New("invalid API-key environment variable name")
	}
	if len(c.EncryptedAPIKeys) > 32 {
		return errors.New("at most 32 endpoint-specific API keys can be saved")
	}
	for scope, cipher := range c.EncryptedAPIKeys {
		if len(scope) > 2048 || len(cipher) > 20000 || !strings.HasPrefix(cipher, "dpapi:") {
			return errors.New("invalid encrypted API-key entry; keys must be saved through API settings")
		}
	}
	for _, app := range c.AllowedApps {
		if strings.ContainsAny(app, "/\\*?\x00") || !strings.HasSuffix(strings.ToLower(app), ".exe") {
			return fmt.Errorf("allowed apps must be executable basenames ending in .exe: %q", app)
		}
	}
	return nil
}

var blocked = map[string]bool{
	"keepass.exe": true, "keepassxc.exe": true, "1password.exe": true, "bitwarden.exe": true,
	"credentialuibroker.exe": true, "consent.exe": true, "logonui.exe": true,
	"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true, "windowsterminal.exe": true,
}

func (c Config) Allows(app string) bool {
	app = strings.ToLower(app)
	if blocked[app] {
		return false
	}
	for _, a := range c.AllowedApps {
		if strings.EqualFold(a, app) {
			return true
		}
	}
	return false
}

func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TypeNext", "config.json"), nil
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if len(data) > 1<<20 {
		return c, errors.New("configuration file is too large")
	}
	if e = json.Unmarshal(data, &c); e != nil {
		return DefaultConfig(), errors.New("config.json is not valid JSON")
	}
	if e = c.Validate(); e != nil {
		return DefaultConfig(), e
	}
	return c, nil
}

func SaveConfig(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	// On Windows os.Rename replaces ordinary existing files. No text history is saved.
	if err = os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}
