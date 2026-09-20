package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModelProfileMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	fresh, err := LoadConfig(path)
	if err != nil || len(fresh.ModelProfiles) != 1 || fresh.ActiveModelProfile != fresh.Model {
		t.Fatalf("missing configuration did not create a saved model: %+v, %v", fresh, err)
	}
	legacy := remoteConfig("https://api.example.com/v1")
	legacy.Model = "existing-model"
	legacy.AllowRemoteAuto = true
	legacy.TokenParameter = "max_completion_tokens"
	legacy.ReasoningEffort = "low"
	legacy.SendTemperature = false
	legacy.APIKeyEnv = "EXISTING_API_KEY"
	legacy.EncryptedAPIKeys = map[string]string{legacy.RemoteConsent: "dpapi:existing-key"}
	legacy.AllowedApps = []string{"winword.exe"}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ModelProfiles) != 1 || got.ModelProfiles[0] != legacy.currentModelProfile(legacy.Model) {
		t.Fatalf("legacy connection changed during migration: %+v", got.ModelProfiles)
	}
	if !reflect.DeepEqual(got.EncryptedAPIKeys, legacy.EncryptedAPIKeys) || !reflect.DeepEqual(got.AllowedApps, legacy.AllowedApps) || got.CheckConsent() != nil {
		t.Fatal("migration changed global settings or remote consent")
	}
	got.EnsureModelProfiles()
	if len(got.ModelProfiles) != 1 {
		t.Fatal("migration was not idempotent")
	}
}

func TestModelProfilesSwitchConnectionsOnly(t *testing.T) {
	c := DefaultConfig()
	c.Auto = true
	c.DebounceMS = 1300
	c.SuggestHotkey = "Ctrl+Alt+N"
	c.AllowedApps = []string{"notepad.exe"}
	if err := c.SaveModelProfile(" Local "); err != nil {
		t.Fatal(err)
	}
	local := c.currentModelProfile("Local")
	remote := remoteConfig("https://api.example.com/v1")
	remote.Model = "remote-model"
	remote.APIKeyEnv = "REMOTE_KEY"
	remote.AllowRemoteAuto = true
	remote.TokenParameter = "max_completion_tokens"
	remote.SendTemperature = false
	remote.ReasoningEffort = "high"
	remote.DisableThinking = false
	remote.MaxTokens = 512
	remote.TimeoutSeconds = 90
	remote.currentModelProfile("Remote").apply(&c)
	c.EncryptedAPIKeys = map[string]string{remote.RemoteConsent: "dpapi:remote-key"}
	if err := c.SaveModelProfile("Remote"); err != nil {
		t.Fatal(err)
	}
	if err := c.UseModelProfile(" local "); err != nil {
		t.Fatal(err)
	}
	if c.currentModelProfile("Local") != local || c.ActiveModelProfile != "Local" {
		t.Fatal("switching to local did not restore all connection settings")
	}
	if !c.Auto || c.DebounceMS != 1300 || c.SuggestHotkey != "Ctrl+Alt+N" || !reflect.DeepEqual(c.AllowedApps, []string{"notepad.exe"}) || c.EncryptedAPIKeys[remote.RemoteConsent] != "dpapi:remote-key" {
		t.Fatal("switching modified global settings or endpoint-bound keys")
	}
	if err := c.UseModelProfile("REMOTE"); err != nil {
		t.Fatal(err)
	}
	if c.currentModelProfile("Remote") != remote.currentModelProfile("Remote") || !c.AutomaticAllowed() {
		t.Fatal("switching to remote did not restore settings and consent")
	}
	// Selecting a saved entry must not grant approval for a different URL.
	c.ModelProfiles[1].Endpoint = "https://other.example.com/v1"
	if err := c.UseModelProfile("Remote"); err != nil {
		t.Fatal(err)
	}
	if c.CheckConsent() == nil || c.AutomaticAllowed() {
		t.Fatal("selection approved a changed remote endpoint")
	}
}

func TestModelProfilePersistenceAndIsolation(t *testing.T) {
	c := DefaultConfig()
	if err := c.SaveModelProfile("First"); err != nil {
		t.Fatal(err)
	}
	first := c.ModelProfiles[0]
	c.Model = "second-model"
	if err := c.SaveModelProfile("Second"); err != nil {
		t.Fatal(err)
	}
	c.Model = "updated-second-model" // Top-level edits remain compatible.
	c.EncryptedAPIKeys = map[string]string{"https://api.example.com/v1/chat/completions": "dpapi:one-copy"}
	before := c.Clone()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, before) {
		t.Fatal("SaveConfig modified the caller's shared profiles")
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveModelProfile != "Second" || got.ModelProfiles[0] != first || got.ModelProfiles[1].Model != c.Model {
		t.Fatal("persistence changed inactive entries or lost the active model update")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Count(string(data), "dpapi:one-copy") != 1 {
		t.Fatal("endpoint key was duplicated into model profiles", err)
	}
	clone := got.Clone()
	clone.ModelProfiles[0].Name = "changed"
	if got.ModelProfiles[0].Name != "First" {
		t.Fatal("cloned profiles share a mutable backing slice")
	}
	got.MaxTokens = 128
	if err := got.SaveModelProfile(" second "); err != nil {
		t.Fatal(err)
	}
	if len(got.ModelProfiles) != 2 || got.ModelProfiles[1].MaxTokens != 128 || got.ActiveModelProfile != "second" {
		t.Fatal("saving an existing name did not update that entry")
	}
}

func TestInvalidModelProfiles(t *testing.T) {
	base := DefaultConfig()
	base.EnsureModelProfiles()
	base = base.Clone()
	for _, name := range []string{"", "  ", "bad\nname", "bad\x00name", strings.Repeat("a", 81)} {
		c := base.Clone()
		if err := c.SaveModelProfile(name); err == nil || !reflect.DeepEqual(c, base) {
			t.Errorf("invalid name %q accepted or changed settings", name)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"duplicate", func(c *Config) {
			c.ModelProfiles = append(c.ModelProfiles, c.ModelProfiles[0])
			c.ModelProfiles[1].Name = strings.ToUpper(c.ModelProfiles[0].Name)
		}},
		{"unknown active", func(c *Config) { c.ActiveModelProfile = "missing" }},
		{"missing active", func(c *Config) { c.ActiveModelProfile = "" }},
		{"orphan active", func(c *Config) { c.ModelProfiles = nil }},
		{"blank name", func(c *Config) { c.ModelProfiles[0].Name = "" }},
		{"surrounding spaces", func(c *Config) { c.ModelProfiles[0].Name = " saved " }},
		{"invalid provider", func(c *Config) { c.ModelProfiles[0].Provider = "unsupported" }},
		{"remote not allowed", func(c *Config) { c.ModelProfiles[0].Endpoint = "https://example.com" }},
		{"invalid request limit", func(c *Config) { c.ModelProfiles[0].MaxTokens = 0 }},
		{"too many", func(c *Config) {
			for i := 1; i <= maxModelProfiles; i++ {
				p := c.ModelProfiles[0]
				p.Name = fmt.Sprintf("model %d", i)
				c.ModelProfiles = append(c.ModelProfiles, p)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base.Clone()
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid saved model configuration accepted")
			}
		})
	}
	c := base.Clone()
	if err := c.UseModelProfile("missing"); err == nil || !reflect.DeepEqual(c, base) {
		t.Fatal("unknown selection was accepted or changed settings")
	}
	c.Model = ""
	before := c.Clone()
	if err := c.SaveModelProfile("Invalid connection"); err == nil || !reflect.DeepEqual(c, before) {
		t.Fatal("invalid connection was saved or changed the collection")
	}
}
