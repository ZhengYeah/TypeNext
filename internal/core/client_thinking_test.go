package core

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestThinkingRequestControls(t *testing.T) {
	localOff := map[string]any{
		"reasoning_effort":     "none",
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	highEffort := map[string]any{"reasoning_effort": "high"}
	for _, tc := range []struct {
		name, provider, endpoint, model, effort string
		disable, allowRemote                    bool
		want                                    map[string]any
	}{
		{"local IPv4 disabled", "openai-compatible", "http://127.0.0.1:8080/v1", "qwen3.8", "", true, false, localOff},
		{"local hostname disabled overrides effort", "openai-compatible", "http://localhost:8080/v1", "Qwen/Qwen3-8B", "high", true, false, localOff},
		{"local IPv6 disabled", "openai-compatible", "http://[::1]:8080/v1", "qwen3.5", "", true, false, localOff},
		{"local with remote permission", "openai-compatible", "http://127.0.0.1:8080/v1", "qwen3.8", "high", true, true, localOff},
		{"local custom model alias", "openai-compatible", "http://localhost:8080/v1", "my-completion-model", "", true, false, localOff},
		{"local enabled server default", "openai-compatible", "http://127.0.0.1:8080/v1", "qwen3.8", "", false, false, nil},
		{"local enabled configured effort", "openai-compatible", "http://127.0.0.1:8080/v1", "qwen3.8", "high", false, false, highEffort},
		{"remote Qwen unchanged", "openai-compatible", "https://models.example.com/v1", "qwen3.8", "", true, true, nil},
		{"remote Qwen preserves effort", "openai-compatible", "https://models.example.com/v1", "qwen3.8", "high", true, true, highEffort},
		{"Ollama disabled", "ollama", "http://127.0.0.1:11434", "qwen3:8b", "high", true, false, map[string]any{"think": false}},
		{"Ollama enabled", "ollama", "http://127.0.0.1:11434", "qwen3:8b", "", false, false, nil},
		{"DeepSeek disabled", "openai-compatible", "https://api.deepseek.com/v1", "deepseek-chat", "high", true, true, map[string]any{"reasoning_effort": "high", "thinking": map[string]any{"type": "disabled"}}},
		{"DeepSeek enabled", "openai-compatible", "https://api.deepseek.com/v1", "deepseek-chat", "high", false, true, highEffort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Provider, cfg.Endpoint, cfg.Model = tc.provider, tc.endpoint, tc.model
			cfg.DisableThinking, cfg.AllowRemote = tc.disable, tc.allowRemote
			cfg.ReasoningEffort, cfg.APIKeyEnv = tc.effort, ""
			if cfg.IsRemote() {
				u, err := cfg.RequestURL()
				if err != nil {
					t.Fatal(err)
				}
				cfg.RemoteConsent = u.String()
			}
			textContext := TextContext{Prefix: "A draft /think", Suffix: " comes next."}
			called := false
			client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var controls map[string]any
				for _, key := range []string{"reasoning_effort", "chat_template_kwargs", "think", "thinking", "enable_thinking", "extra_body"} {
					if value, ok := body[key]; ok {
						if controls == nil {
							controls = make(map[string]any)
						}
						controls[key] = value
					}
				}
				if !reflect.DeepEqual(controls, tc.want) {
					t.Errorf("thinking controls = %#v, want %#v", controls, tc.want)
				}
				wantMessages := []any{
					map[string]any{"role": "system", "content": SystemPrompt},
					map[string]any{"role": "user", "content": ContextJSON(textContext)},
				}
				if !reflect.DeepEqual(body["messages"], wantMessages) || body["model"] != tc.model {
					t.Error("thinking controls changed the model or context messages")
				}
				if tc.provider == "ollama" {
					return response("application/x-ndjson", "{\"message\":{\"content\":\"works.\"},\"done\":true}\n"), nil
				}
				return response("text/event-stream", sampleSSE), nil
			})}}
			got, err := client.Complete(context.Background(), cfg, textContext, nil)
			if err != nil || got != "works." || !called {
				t.Fatalf("Complete() = %q, %v; request sent = %v", got, err, called)
			}
		})
	}
}
