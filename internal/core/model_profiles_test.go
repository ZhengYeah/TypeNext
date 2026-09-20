package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemoveInactiveModelProfile(t *testing.T) {
	c := DefaultConfig()
	for _, name := range []string{"First", "Second", "Third"} {
		c.Model = strings.ToLower(name)
		if err := c.SaveModelProfile(name); err != nil {
			t.Fatal(err)
		}
	}
	// Unsaved edits to the active connection must survive removing another entry.
	c.Model = "edited-third"
	before := c.Clone()
	shared := c
	if err := c.RemoveModelProfile(" sEcOnD "); err != nil {
		t.Fatal(err)
	}
	want := before.Clone()
	want.ModelProfiles = []ModelProfile{before.ModelProfiles[0], before.ModelProfiles[2]}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("removing an inactive model changed other settings: %+v", c)
	}
	if !reflect.DeepEqual(shared.ModelProfiles, before.ModelProfiles) {
		t.Fatal("removal mutated the backing slice used by an existing configuration")
	}
}

func TestRemoveActiveModelProfileRestoresSavedConnection(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(fmt.Sprintf("approved=%t", approved), func(t *testing.T) {
			c := remoteConfig("https://api.example.com/v1")
			c.Auto, c.AllowRemoteAuto = true, true
			c.APIKeyEnv = "REMOTE_KEY"
			c.TokenParameter, c.ReasoningEffort = "max_completion_tokens", "high"
			c.SendTemperature, c.DisableThinking = false, false
			c.MaxTokens, c.TimeoutSeconds = 256, 90
			c.EncryptedAPIKeys = map[string]string{c.RemoteConsent: "dpapi:shared-key"}
			if !approved {
				c.Endpoint = "https://changed.example.com/v1"
			}
			if err := c.SaveModelProfile("Remote"); err != nil {
				t.Fatal(err)
			}
			remote := c.ModelProfiles[0]
			DefaultConfig().currentModelProfile("Local").apply(&c)
			if err := c.SaveModelProfile("Local"); err != nil {
				t.Fatal(err)
			}
			c.Model = "other-local"
			if err := c.SaveModelProfile("Other"); err != nil {
				t.Fatal(err)
			}
			if err := c.UseModelProfile("Local"); err != nil {
				t.Fatal(err)
			}
			before := c.Clone()
			if err := c.RemoveModelProfile(" LOCAL "); err != nil {
				t.Fatal(err)
			}
			want := before.Clone()
			want.ModelProfiles = []ModelProfile{remote, before.ModelProfiles[2]}
			remote.apply(&want)
			want.ActiveModelProfile = remote.Name
			if !reflect.DeepEqual(c, want) {
				t.Fatal("active removal did not restore the first remaining connection and preserve globals")
			}
			if (c.CheckConsent() == nil) != approved || c.AutomaticAllowed() != approved {
				t.Fatal("active removal changed the fallback model's existing consent")
			}
		})
	}
}

func TestRemoveLastModelProfilePersistsEmptyState(t *testing.T) {
	c := remoteConfig("https://api.example.com/v1")
	c.AllowRemoteAuto, c.Auto = true, true
	c.APIKeyEnv, c.ReasoningEffort = "REMOTE_KEY", "high"
	c.TokenParameter = "max_completion_tokens"
	c.SendTemperature, c.DisableThinking = false, false
	c.MaxTokens, c.TimeoutSeconds = 512, 100
	c.DebounceMS, c.PrefixChars, c.SuffixChars = 1500, 1200, 250
	c.MaxSuggestionChars = 400
	c.AcceptTab = false
	c.SuggestHotkey = "Ctrl+Alt+N"
	c.AllowedApps = []string{"notepad.exe"}
	c.EncryptedAPIKeys = map[string]string{c.RemoteConsent: "dpapi:keep-key"}
	if err := c.SaveModelProfile("Only"); err != nil {
		t.Fatal(err)
	}
	before := c.Clone()
	if err := c.RemoveModelProfile("Only"); err != nil {
		t.Fatal(err)
	}
	want := before.Clone()
	want.ModelProfiles, want.ActiveModelProfile = []ModelProfile{}, ""
	DefaultConfig().currentModelProfile("").apply(&want)
	if !reflect.DeepEqual(c, want) {
		t.Fatal("last removal did not reset only connection fields while preserving globals and keys")
	}
	if c.HasModelConnection() || c.AutomaticAllowed() || c.CheckConsent() == nil {
		t.Fatal("last removal left inference enabled")
	}
	clone := c.Clone()
	clone.EnsureModelProfiles()
	if clone.ModelProfiles == nil || clone.HasModelConnection() {
		t.Fatal("cloning or migration recreated a removed model")
	}
	clone.AllowedApps[0] = "winword.exe"
	for scope := range clone.EncryptedAPIKeys {
		clone.EncryptedAPIKeys[scope] = "dpapi:changed"
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatal("cloning shared mutable global settings")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	for i := 0; i < 2; i++ {
		if err := SaveConfig(path, c); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), `"model_profiles": []`) {
			t.Fatalf("empty saved collection was not persisted: %s, %v", data, err)
		}
		c, err = LoadConfig(path)
		if err != nil || !reflect.DeepEqual(c, want) || c.HasModelConnection() {
			t.Fatalf("loading recreated a removed model or changed preferences: %+v, %v", c, err)
		}
	}
	if err := c.SaveModelProfile("New model"); err != nil {
		t.Fatal(err)
	}
	if !c.HasModelConnection() || len(c.ModelProfiles) != 1 || c.ActiveModelProfile != "New model" || c.CheckConsent() != nil {
		t.Fatal("could not save a new model after removing every model")
	}
}

func TestRemoveModelProfileFailureIsAtomic(t *testing.T) {
	base := DefaultConfig()
	for _, name := range []string{"First", "Second"} {
		if err := base.SaveModelProfile(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, remove string
		mutate       func(*Config)
	}{
		{"unknown", "missing", func(*Config) {}},
		{"empty name", "  ", func(*Config) {}},
		{"invalid fallback", "Second", func(c *Config) { c.ModelProfiles[0].Model = "" }},
		{"invalid preferences", "First", func(c *Config) { c.DebounceMS = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base.Clone()
			tc.mutate(&c)
			before := c.Clone()
			if err := c.RemoveModelProfile(tc.remove); err == nil || !reflect.DeepEqual(c, before) {
				t.Fatal("failed removal modified the configuration or was accepted")
			}
		})
	}
}

func TestEmptyModelProfilesRejectRequestsBeforeNetworkOrKey(t *testing.T) {
	c := DefaultConfig()
	if !c.HasModelConnection() || !c.Clone().HasModelConnection() || c.CheckConsent() != nil {
		t.Fatal("legacy top-level-only connections must remain compatible")
	}
	c.ModelProfiles = []ModelProfile{}
	c.Auto = true
	u, err := c.RequestURL()
	if err != nil {
		t.Fatal(err)
	}
	c.EncryptedAPIKeys = map[string]string{u.String(): "dpapi:unused"}
	called := false
	client := &Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("must not send")
		})},
		DecryptKey: func(string, string) (string, error) {
			called = true
			return "", nil
		},
	}
	_, err = client.Complete(context.Background(), c, TextContext{Prefix: "private text"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no saved model") || called || c.AutomaticAllowed() {
		t.Fatalf("empty configuration was not rejected before network and key access: called=%t err=%v", called, err)
	}
}

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
