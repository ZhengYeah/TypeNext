package core

// Presets are conveniences, not network discovery or guarantees that a user's
// account has access to a particular model. The model field remains editable.
type APIPreset struct {
	Name, Provider, Endpoint, Model, TokenParameter, KeyEnv string
	SendTemperature                                         bool
}

func APIPresets() []APIPreset {
	return []APIPreset{
		{"Ollama (local)", "ollama", "http://127.0.0.1:11434", "qwen3:4b", "max_tokens", "TYPENEXT_API_KEY", true},
		{"OpenAI-compatible (local)", "openai-compatible", "http://127.0.0.1:1234/v1", "", "max_tokens", "TYPENEXT_API_KEY", true},
		{"OpenAI API", "openai-compatible", "https://api.openai.com/v1", "", "max_completion_tokens", "OPENAI_API_KEY", false},
		{"DeepSeek API", "openai-compatible", "https://api.deepseek.com/chat/completions", "", "max_tokens", "DEEPSEEK_API_KEY", true},
		{"OpenRouter API", "openai-compatible", "https://openrouter.ai/api/v1", "", "max_tokens", "OPENROUTER_API_KEY", false},
		{"Custom OpenAI-compatible API", "openai-compatible", "", "", "max_tokens", "TYPENEXT_API_KEY", true},
	}
}
