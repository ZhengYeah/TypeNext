package core

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxModelProfiles = 32

// ModelProfile contains one saved connection. Application permissions,
// interaction preferences, and endpoint-bound encrypted keys remain global.
type ModelProfile struct {
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	Endpoint        string `json:"endpoint"`
	Model           string `json:"model"`
	APIKeyEnv       string `json:"api_key_environment_variable,omitempty"`
	AllowRemote     bool   `json:"allow_remote_api"`
	AllowRemoteAuto bool   `json:"allow_automatic_remote_requests"`
	RemoteConsent   string `json:"approved_remote_endpoint,omitempty"`
	TokenParameter  string `json:"token_parameter"`
	SendTemperature bool   `json:"send_temperature"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	DisableThinking bool   `json:"disable_thinking"`
	MaxTokens       int    `json:"max_tokens"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
}

func (c Config) currentModelProfile(name string) ModelProfile {
	return ModelProfile{
		Name: name, Provider: c.Provider, Endpoint: c.Endpoint, Model: c.Model,
		APIKeyEnv: c.APIKeyEnv, AllowRemote: c.AllowRemote, AllowRemoteAuto: c.AllowRemoteAuto,
		RemoteConsent: c.RemoteConsent, TokenParameter: c.TokenParameter,
		SendTemperature: c.SendTemperature, ReasoningEffort: c.ReasoningEffort,
		DisableThinking: c.DisableThinking, MaxTokens: c.MaxTokens, TimeoutSeconds: c.TimeoutSeconds,
	}
}

func (p ModelProfile) apply(c *Config) {
	c.Provider, c.Endpoint, c.Model = p.Provider, p.Endpoint, p.Model
	c.APIKeyEnv, c.AllowRemote, c.AllowRemoteAuto = p.APIKeyEnv, p.AllowRemote, p.AllowRemoteAuto
	c.RemoteConsent, c.TokenParameter = p.RemoteConsent, p.TokenParameter
	c.SendTemperature, c.ReasoningEffort = p.SendTemperature, p.ReasoningEffort
	c.DisableThinking, c.MaxTokens, c.TimeoutSeconds = p.DisableThinking, p.MaxTokens, p.TimeoutSeconds
}

// EnsureModelProfiles migrates a configuration predating saved connections.
// Existing collections are left intact so malformed entries can be reported.
func (c *Config) EnsureModelProfiles() {
	if c.ModelProfiles != nil || c.ActiveModelProfile != "" {
		return
	}
	name := strings.TrimSpace(c.Model)
	if runes := []rune(name); len(runes) > 80 {
		name = strings.TrimSpace(string(runes[:80]))
	}
	if validateModelProfileName(name) != nil {
		name = "Current model"
	}
	c.ModelProfiles = []ModelProfile{c.currentModelProfile(name)}
	c.ActiveModelProfile = name
}

// HasModelConnection distinguishes legacy top-level connections from an
// explicitly empty saved collection after the last model has been removed.
func (c Config) HasModelConnection() bool {
	return c.ModelProfiles == nil || len(c.ModelProfiles) > 0
}

// SaveModelProfile saves the current connection under a name. Reusing a name
// updates that connection; a new name keeps the previously saved connections.
func (c *Config) SaveModelProfile(name string) error {
	name = strings.TrimSpace(name)
	if err := validateModelProfileName(name); err != nil {
		return err
	}
	next := c.Clone()
	profile := next.currentModelProfile(name)
	index := next.modelProfileIndex(name)
	if index >= 0 {
		next.ModelProfiles[index] = profile
	} else {
		next.ModelProfiles = append(next.ModelProfiles, profile)
	}
	next.ActiveModelProfile = name
	if err := next.Validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

// UseModelProfile changes only connection-specific settings. Consent remains
// bound to the selected connection's canonical URL and is checked on requests.
func (c *Config) UseModelProfile(name string) error {
	index := c.modelProfileIndex(strings.TrimSpace(name))
	if index < 0 {
		return fmt.Errorf("saved model %q was not found", name)
	}
	next := c.Clone()
	profile := next.ModelProfiles[index]
	profile.apply(&next)
	next.ActiveModelProfile = profile.Name
	if err := next.Validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

// RemoveModelProfile removes one saved connection. Removing the active model
// selects the first remaining profile with its saved settings and consent.
// Removing the last model clears the connection without changing global settings.
func (c *Config) RemoveModelProfile(name string) error {
	index := c.modelProfileIndex(strings.TrimSpace(name))
	if index < 0 {
		return fmt.Errorf("saved model %q was not found", name)
	}
	next := c.Clone()
	wasActive := strings.EqualFold(next.ActiveModelProfile, next.ModelProfiles[index].Name)
	next.ModelProfiles = append(next.ModelProfiles[:index], next.ModelProfiles[index+1:]...)
	if len(next.ModelProfiles) == 0 {
		next.ModelProfiles = []ModelProfile{}
		next.ActiveModelProfile = ""
		DefaultConfig().currentModelProfile("").apply(&next)
	} else if wasActive {
		profile := next.ModelProfiles[0]
		profile.apply(&next)
		next.ActiveModelProfile = profile.Name
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c Config) modelProfileIndex(name string) int {
	for i, profile := range c.ModelProfiles {
		if strings.EqualFold(profile.Name, name) {
			return i
		}
	}
	return -1
}

func (c *Config) syncActiveModelProfile() {
	if i := c.modelProfileIndex(c.ActiveModelProfile); i >= 0 {
		c.ModelProfiles[i] = c.currentModelProfile(c.ModelProfiles[i].Name)
		c.ActiveModelProfile = c.ModelProfiles[i].Name
	}
}

func validateModelProfileName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 {
		return errors.New("saved model name must contain 1–80 characters without leading or trailing spaces")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("saved model name cannot contain control characters")
		}
	}
	return nil
}

func (c Config) validateModelProfiles() error {
	if len(c.ModelProfiles) > maxModelProfiles {
		return fmt.Errorf("at most %d model configurations can be saved", maxModelProfiles)
	}
	for i, profile := range c.ModelProfiles {
		if err := validateModelProfileName(profile.Name); err != nil {
			return err
		}
		for _, previous := range c.ModelProfiles[:i] {
			if strings.EqualFold(previous.Name, profile.Name) {
				return fmt.Errorf("saved model names must be unique: %q", profile.Name)
			}
		}
		var connection Config
		profile.apply(&connection)
		if err := connection.validateModelConnection(); err != nil {
			return fmt.Errorf("saved model %q: %w", profile.Name, err)
		}
	}
	if len(c.ModelProfiles) == 0 && c.ActiveModelProfile == "" {
		return nil // Legacy top-level connection or an intentionally empty collection.
	}
	if c.modelProfileIndex(c.ActiveModelProfile) < 0 {
		return errors.New("active model configuration must refer to a saved model")
	}
	return nil
}

func (c Config) validateModelConnection() error {
	if c.Provider != "ollama" && c.Provider != "openai-compatible" {
		return errors.New("provider must be ollama or openai-compatible")
	}
	if _, err := ServerBase(c.Endpoint, c.AllowRemote); err != nil {
		return err
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
	if c.MaxTokens < 8 || c.MaxTokens > 4096 {
		return errors.New("max_tokens must be 8–4096")
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
	if len(c.RemoteConsent) > 4096 || strings.ContainsAny(c.RemoteConsent, "\r\n\x00") {
		return errors.New("invalid approved remote endpoint")
	}
	return nil
}
