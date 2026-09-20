package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// A Client supports local Ollama and local/remote OpenAI Chat Completions.
// Remote calls require HTTPS plus endpoint-specific user consent in Config.
// The production transport never follows redirects or uses environment proxies.
type Client struct {
	HTTP       *http.Client
	DecryptKey func(ciphertext, scope string) (string, error)
}

func NewClient() *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	// The per-request context controls both first-byte and overall timeout.
	tr.ResponseHeaderTimeout = 0
	return &Client{HTTP: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("server redirects are disabled")
	}}}
}

func (c *Client) apiKey(cfg Config, scope string) (string, error) {
	var key string
	if cipher := cfg.EncryptedAPIKeys[scope]; cipher != "" {
		if c.DecryptKey == nil {
			return "", errors.New("the saved API key needs Windows DPAPI; re-enter it in API settings")
		}
		var err error
		key, err = c.DecryptKey(cipher, scope)
		if err != nil {
			return "", errors.New("cannot unlock the saved API key; re-enter it on this Windows account")
		}
	} else if cfg.APIKeyEnv != "" {
		key = strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	}
	if err := ValidateAPIKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func (c *Client) Complete(ctx context.Context, cfg Config, t TextContext, onPartial func(string)) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if err := cfg.CheckConsent(); err != nil {
		return "", err
	}
	base, _ := cfg.RequestURL()
	key, err := c.apiKey(cfg, base.String())
	if err != nil {
		return "", err
	}
	// Enforce context limits at the network boundary too, independent of the
	// accessibility reader. Only these two strings leave the process.
	t.Prefix = Tail(t.Prefix, cfg.PrefixChars)
	t.Suffix = Head(t.Suffix, cfg.SuffixChars)
	messages := []map[string]string{{"role": "system", "content": SystemPrompt}, {"role": "user", "content": ContextJSON(t)}}
	body := map[string]any{"model": cfg.Model, "messages": messages, "stream": true}
	if cfg.Provider == "ollama" {
		if cfg.DisableThinking {
			body["think"] = false
		}
		body["keep_alive"] = "10m"
		body["options"] = map[string]any{"temperature": 0.2, "num_predict": cfg.MaxTokens}
	} else {
		body[cfg.TokenParameter] = cfg.MaxTokens
		if cfg.SendTemperature {
			body["temperature"] = 0.2
		}
		if cfg.ReasoningEffort != "" {
			body["reasoning_effort"] = cfg.ReasoningEffort
		}
		if base.Hostname() == "api.deepseek.com" && cfg.DisableThinking {
			body["thinking"] = map[string]string{"type": "disabled"}
		}
		// This is not a zero-retention guarantee; provider policy still applies.
		if base.Hostname() == "api.openai.com" {
			body["store"] = false
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", errors.New("cannot encode the API request")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(data))
	if err != nil {
		return "", errors.New("cannot construct the API request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson, text/event-stream, application/json")
	req.Header.Set("User-Agent", "TypeNext/"+Version)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("cannot reach the model API; check URL, connection, TLS certificate, and server availability (redirects and proxies are disabled)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apiHTTPError(resp.StatusCode)
	}

	var raw strings.Builder
	emit := func(piece string) error {
		if piece == "" {
			return nil
		}
		if raw.Len()+len(piece) > 65536 {
			return errors.New("model response exceeded the safety limit")
		}
		raw.WriteString(piece)
		if onPartial != nil {
			onPartial(CleanSuggestion(raw.String(), t, cfg.MaxSuggestionChars))
		}
		return nil
	}
	done := false
	acceptCompletion := func(data []byte) error {
		var v struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return errors.New("invalid OpenAI-compatible response")
		}
		if len(v.Error) > 0 && string(v.Error) != "null" {
			return errors.New("model API reported an inference error; response details are hidden to protect textbox content")
		}
		for _, choice := range v.Choices {
			if choice.Index != 0 {
				continue
			}
			piece := choice.Delta.Content
			if piece == "" {
				piece = choice.Message.Content
			}
			if err := emit(piece); err != nil {
				return err
			}
			if choice.FinishReason != nil {
				reason := *choice.FinishReason
				if reason != "stop" && reason != "length" {
					return errors.New("the model did not finish with a text continuation; nothing will be inserted")
				}
				done = true
			}
			break
		}
		return nil
	}

	// Some compatible servers ignore stream=true and return ordinary JSON.
	if cfg.Provider == "openai-compatible" && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		if err != nil {
			return "", safeStreamError(ctx)
		}
		if len(data) > 2<<20 {
			return "", errors.New("API response exceeded the safety limit")
		}
		if err := acceptCompletion(data); err != nil {
			return "", err
		}
	} else {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, (2<<20)+1))
		scanner.Buffer(make([]byte, 4096), 512*1024)
		var pending string
		total := 0
		process := func(payload string) (bool, error) {
			if payload == "[DONE]" {
				done = true
				return true, nil
			}
			return false, acceptCompletion([]byte(payload))
		}
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			total += len(scanner.Bytes()) + 1
			if total > 2<<20 {
				return "", errors.New("API response exceeded the safety limit")
			}
			if cfg.Provider == "openai-compatible" {
				if line == "" {
					if pending != "" {
						return "", errors.New("invalid multiline API event")
					}
					continue
				}
				if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
					continue
				}
				if strings.HasPrefix(line, "data:") {
					line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				}
				if pending != "" {
					line = pending + "\n" + line
				}
				if line != "[DONE]" && !json.Valid([]byte(line)) {
					pending = line
					if len(pending) > 512*1024 {
						return "", errors.New("API event exceeded the safety limit")
					}
					continue
				}
				pending = ""
				stop, err := process(line)
				if err != nil {
					return "", err
				}
				if stop {
					break
				}
			} else {
				if line == "" || strings.HasPrefix(line, ":") {
					continue
				}
				var v struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
					Done  bool   `json:"done"`
					Error string `json:"error"`
				}
				if err := json.Unmarshal([]byte(line), &v); err != nil {
					return "", errors.New("invalid Ollama stream")
				}
				if v.Error != "" {
					return "", errors.New("Ollama reported an inference error; check the model name and thinking setting")
				}
				if err := emit(v.Message.Content); err != nil {
					return "", err
				}
				if v.Done {
					done = true
					break
				}
			}
		}
		if scanner.Err() != nil {
			return "", safeStreamError(ctx)
		}
		if pending != "" {
			return "", errors.New("API stream ended inside an incomplete event")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !done {
		return "", errors.New("model stream ended before a completion marker; nothing will be inserted")
	}
	result := CleanSuggestion(raw.String(), t, cfg.MaxSuggestionChars)
	if result == "" {
		return "", errors.New("model returned no usable continuation; choose a non-thinking model or increase the output-token budget in API settings")
	}
	return result, nil
}

func safeStreamError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("API stream interrupted or malformed; nothing will be inserted")
}

func apiHTTPError(code int) error {
	hint := "check the provider, model, and request settings"
	switch code {
	case 400, 422:
		hint = "check the model, token parameter, temperature, and reasoning settings in API settings"
	case 401:
		hint = "authentication failed; check your API key"
	case 403:
		hint = "access denied; check API-key permissions and model access"
	case 404:
		hint = "endpoint or model not found; check the base URL and exact model ID"
	case 408, 504:
		hint = "the provider timed out; try a smaller or faster model"
	case 429:
		hint = "rate limit or quota reached; check your provider dashboard; TypeNext does not retry automatically"
	default:
		if code >= 300 && code < 400 {
			hint = "redirects are blocked; enter the final API URL directly"
		}
		if code >= 500 {
			hint = "the provider is unavailable; retry manually later"
		}
	}
	return fmt.Errorf("model API returned HTTP %d: %s", code, hint)
}
