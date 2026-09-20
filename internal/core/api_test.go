package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func remoteConfig(endpoint string) Config {
	c := DefaultConfig()
	c.Provider = "openai-compatible"
	c.Endpoint = endpoint
	c.AllowRemote = true
	c.APIKeyEnv = ""
	u, err := c.RequestURL()
	if err == nil {
		c.RemoteConsent = u.String()
	}
	return c
}
func response(contentType, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

const sampleSSE = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"works.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestStreamingMetadataDoesNotRepeatPartial(t *testing.T) {
	for _, tc := range []struct {
		provider, contentType, body string
	}{
		{"openai-compatible", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"works.\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"ollama", "application/x-ndjson", "{\"message\":{\"content\":\"\"},\"done\":false}\n" +
			"{\"message\":{\"content\":\"works.\"},\"done\":false}\n{\"done\":true}\n"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Provider, cfg.APIKeyEnv = tc.provider, ""
			client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(tc.contentType, tc.body), nil
			})}}
			var partials []string
			got, err := client.Complete(context.Background(), cfg, TextContext{}, func(s string) {
				partials = append(partials, s)
			})
			if err != nil || got != "works." || len(partials) != 1 || partials[0] != got {
				t.Fatalf("result=%q partials=%q err=%v", got, partials, err)
			}
		})
	}
}

// A real HTTPS test server, with a private test CA and a test-only dialer.
// Production certificate checking is unchanged. Nothing connects to the Internet.
func remoteTLSServer(t *testing.T, h http.HandlerFunc) (*Client, Config, *httptest.Server) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1234), DNSNames: []string{"api.typenext.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(h)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	t.Cleanup(s.Close)
	parsed, _ := url.Parse(s.URL)
	client := NewClient()
	tr := client.HTTP.Transport.(*http.Transport)
	pool := x509.NewCertPool()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(cert)
	tr.TLSClientConfig.RootCAs = pool
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "api.typenext.test:443" {
			return nil, errors.New("unexpected test destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, parsed.Host)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return client, remoteConfig("https://api.typenext.test/v1"), s
}

func TestRemoteURLValidation(t *testing.T) {
	valid := map[string]string{
		"https://api.example.com/v1/":                   "https://api.example.com/v1",
		"https://API.EXAMPLE.COM:443/v1":                "https://api.example.com/v1",
		"https://api.example.com:8443/chat/completions": "https://api.example.com:8443/chat/completions",
		"https://192.168.0.2:8443/v1":                   "https://192.168.0.2:8443/v1",
		"https://[2001:db8::1]/v1":                      "https://[2001:db8::1]/v1",
		"http://LOCALHOST:80/":                          "http://127.0.0.1",
	}
	for input, want := range valid {
		t.Run(input, func(t *testing.T) {
			u, err := ServerBase(input, true)
			if err != nil || u.String() != want {
				t.Fatalf("got %v %v", u, err)
			}
		})
	}
	for _, input := range []string{
		"http://api.example.com", "http://192.168.1.2", "https://user:key@api.example.com", "https://api.example.com?key=secret", "https://api.example.com?", "https://api.example.com#key", "https://api.example.com:0", "https://api.example.com:99999", "https://api.example.com:", "https://api.example.com:notaport", "https:///v1", "https://.example.com", "https://example..com", "https://example.com.", "https://-example.com", "https://example-.com", "https://[::]", "https://224.1.1.1", "https://[fe80::1%25eth0]", "https://api.example.com/a/../b", "https://api.example.com/v1//chat", "https://api.example.com/v1%2fchat", "https://api.example.com/a%00b", "https://api.example.com/a\\b", "https://api.example.com/\n", "https://" + strings.Repeat("a", 64) + ".com",
	} {
		t.Run("reject_"+input, func(t *testing.T) {
			if _, err := ServerBase(input, true); err == nil {
				t.Fatal("unsafe URL accepted")
			}
		})
	}
}

func TestEndpointConstruction(t *testing.T) {
	for _, tc := range []struct{ provider, input, want string }{
		{"openai-compatible", "https://example.com", "https://example.com/v1/chat/completions"},
		{"openai-compatible", "https://example.com/v1/", "https://example.com/v1/chat/completions"},
		{"openai-compatible", "https://example.com/v1/chat/completions/", "https://example.com/v1/chat/completions"},
		{"openai-compatible", "https://example.com/chat/completions", "https://example.com/chat/completions"},
		{"openai-compatible", "https://example.com/custom/api/v2", "https://example.com/custom/api/v2/chat/completions"},
		{"ollama", "http://localhost:11434", "http://127.0.0.1:11434/api/chat"},
		{"ollama", "http://localhost:11434/api", "http://127.0.0.1:11434/api/chat"},
		{"ollama", "http://localhost:11434/api/chat/", "http://127.0.0.1:11434/api/chat"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			c := remoteConfig(tc.input)
			c.Provider = tc.provider
			u, err := c.RequestURL()
			if err != nil || u.String() != tc.want {
				t.Fatalf("%v %v", u, err)
			}
		})
	}
}

func TestRemoteConsentBeforeNetworkOrKey(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.AllowRemote = false },
		func(c *Config) { c.RemoteConsent = "" },
		func(c *Config) { c.Endpoint = "https://another.example.com/v1" },
		func(c *Config) { c.Endpoint = "https://api.example.com/different" },
	} {
		cfg := remoteConfig("https://api.example.com/v1")
		cfg.EncryptedAPIKeys = map[string]string{cfg.RemoteConsent: "dpapi:dummy"}
		mutate(&cfg)
		called := false
		client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("must not send") })}, DecryptKey: func(string, string) (string, error) { called = true; return "", nil }}
		if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err == nil || called {
			t.Fatalf("unauthorized call: %v %v", err, called)
		}
	}
}

func TestRemoteHTTPSStreamingAndAuthentication(t *testing.T) {
	t.Setenv("TYPENEXT_REMOTE_TEST_KEY", "example-secret")
	var count atomic.Int32
	client, cfg, _ := remoteTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.TLS == nil || r.URL.Path != "/v1/chat/completions" {
			t.Error("wrong endpoint")
		}
		if r.Header.Get("Authorization") != "Bearer example-secret" {
			t.Error("missing authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["max_completion_tokens"] != float64(64) || body["max_tokens"] != nil {
			t.Error("wrong token parameter")
		}
		if body["temperature"] != nil || body["reasoning_effort"] != "none" {
			t.Error("wrong request options")
		}
		messages := body["messages"].([]any)
		raw := messages[1].(map[string]any)["content"].(string)
		var sent map[string]string
		json.Unmarshal([]byte(raw), &sent)
		if len(sent) != 2 || len([]rune(sent["prefix"])) != 32 || len([]rune(sent["suffix"])) != 2 {
			t.Errorf("context not clipped: %v", sent)
		}
		if strings.Contains(raw, "INTERNAL_METADATA") {
			t.Error("metadata leaked")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": heartbeat\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_content\":\"HIDDEN_REASONING\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, sampleSSE)
	})
	cfg.APIKeyEnv = "TYPENEXT_REMOTE_TEST_KEY"
	cfg.TokenParameter = "max_completion_tokens"
	cfg.SendTemperature = false
	cfg.ReasoningEffort = "none"
	cfg.PrefixChars = 32
	cfg.SuffixChars = 2
	got, err := client.Complete(context.Background(), cfg, TextContext{Prefix: strings.Repeat("中", 100), Suffix: "abcdef", Process: "INTERNAL_METADATA", FocusID: "INTERNAL_METADATA"}, func(p string) {
		if strings.Contains(p, "HIDDEN") {
			t.Error("reasoning leaked")
		}
	})
	if err != nil || got != "works." || count.Load() != 1 {
		t.Fatalf("%q %v (%d calls)", got, err, count.Load())
	}
}

func TestRemoteTLSCertificateRequired(t *testing.T) {
	client, cfg, _ := remoteTLSServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("HTTP reached with untrusted certificate") })
	tr := client.HTTP.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = x509.NewCertPool() // Trust nothing.
	if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err == nil {
		t.Fatal("invalid certificate accepted")
	}
}

func TestRemoteRedirectBlocked(t *testing.T) {
	var count atomic.Int32
	client, cfg, _ := remoteTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		http.Redirect(w, r, "https://another.example.com/stolen", 307)
	})
	if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err == nil || count.Load() != 1 {
		t.Fatal("redirect followed", err, count.Load())
	}
}

func TestSavedKeyBindingAndPrecedence(t *testing.T) {
	t.Setenv("TYPENEXT_API_TEST_ENV", "environment-secret")
	cfg := remoteConfig("https://api.example.com/v1")
	cfg.APIKeyEnv = "TYPENEXT_API_TEST_ENV"
	cfg.EncryptedAPIKeys = map[string]string{cfg.RemoteConsent: "dpapi:test-cipher"}
	decrypted := 0
	wantKey := "stored-secret"
	client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		want := ""
		if wantKey != "" {
			want = "Bearer " + wantKey
		}
		if r.Header.Get("Authorization") != want {
			t.Error("wrong endpoint credential")
		}
		return response("text/event-stream", sampleSSE), nil
	})}, DecryptKey: func(cipher, scope string) (string, error) {
		decrypted++
		if scope != "https://api.example.com/v1/chat/completions" || cipher != "dpapi:test-cipher" {
			t.Error("wrong key scope")
		}
		return "stored-secret", nil
	}}
	if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err != nil {
		t.Fatal(err)
	}
	cfg.Endpoint = "https://other.example.com/v1"
	u, _ := cfg.RequestURL()
	cfg.RemoteConsent = u.String()
	cfg.APIKeyEnv = "" // No explicitly configured fallback for the new provider.
	wantKey = ""
	if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if decrypted != 1 {
		t.Fatal("saved key used for a different endpoint")
	}
}

func TestKeyDecryptionFailureDoesNotFallbackOrLeak(t *testing.T) {
	cfg := remoteConfig("https://example.com/v1")
	cfg.EncryptedAPIKeys = map[string]string{cfg.RemoteConsent: "dpapi:dummy"}
	for _, resolver := range []func(string, string) (string, error){nil, func(string, string) (string, error) { return "", errors.New("SECRET_ERROR_CONTENT") }} {
		sent := false
		client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { sent = true; return nil, errors.New("unexpected") })}, DecryptKey: resolver}
		_, err := client.Complete(context.Background(), cfg, TextContext{}, nil)
		if sent || err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("unsafe decryption failure", err)
		}
	}
}

func TestRemoteErrorMessagesAndNoRetry(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 422, 429, 500, 503, 504, 307} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			cfg := remoteConfig("https://example.com/v1")
			calls := 0
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				r := response("application/json", `{"error":"SECRET_PROMPT_AND_API_KEY"}`)
				r.StatusCode = status
				return r, nil
			})
			_, err := client.Complete(context.Background(), cfg, TextContext{}, nil)
			if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), fmt.Sprint(status)) || calls != 1 {
				t.Fatal("unsafe error behavior", err, calls)
			}
		})
	}
}

func TestCompatibleResponseVariants(t *testing.T) {
	cases := []struct {
		name, ctype, body string
		success           bool
	}{
		{"pretty JSON", "application/json", "{\n \"choices\": [{\"message\": {\"content\":\"works.\"}, \"finish_reason\":\"stop\"}]\n}", true},
		{"null delta content", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":null},\"finish_reason\":null}]}\n\n" + sampleSSE, true},
		{"multiline SSE", "text/event-stream", "data: {\"choices\": [\n" + "data: {\"delta\":{\"content\":\"works.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true},
		{"usage chunk", "text/event-stream", "data: {\"choices\":[],\"usage\":{\"total_tokens\":30}}\n\n" + sampleSSE, true},
		{"metadata", "text/event-stream", "event: message\nid: 1\nretry: 1000\n" + sampleSSE, true},
		{"finish length", "application/json", `{"choices":[{"message":{"content":"works."},"finish_reason":"length"}]}`, true},
		{"finish-only EOF", "text/event-stream", strings.Split(sampleSSE, "data: [DONE]")[0], true},
		{"truncated event", "text/event-stream", `data: {"choices":`, false},
		{"broken JSON", "application/json", `{"choices":`, false},
		{"incomplete", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n", false},
		{"server error", "text/event-stream", "event: error\ndata: {\"error\":\"SECRET\"}\n\n", false},
		{"filter", "application/json", `{"choices":[{"message":{"content":"partial"},"finish_reason":"content_filter"}]}`, false},
		{"tool calls", "application/json", `{"choices":[{"message":{"content":"partial"},"finish_reason":"tool_calls"}]}`, false},
		{"empty content", "application/json", `{"choices":[{"message":{"content":null},"finish_reason":"stop"}]}`, false},
		{"reasoning only", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"SECRET\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", false},
		{"empty done", "text/event-stream", "data: [DONE]\n\n", false},
		{"empty JSON", "application/json", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.ctype, tc.body), nil })
			got, err := client.Complete(context.Background(), remoteConfig("https://example.com"), TextContext{}, nil)
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result: %q %v", got, err)
			}
			if tc.success && got != "works." {
				t.Fatalf("wrong continuation: %q", got)
			}
			if err != nil && (got != "" || strings.Contains(err.Error(), "SECRET")) {
				t.Fatal("unsafe failure")
			}
		})
	}
}

func TestRequestOptionsByProvider(t *testing.T) {
	for _, host := range []string{"api.openai.com", "api.deepseek.com", "api.custom.test"} {
		t.Run(host, func(t *testing.T) {
			cfg := remoteConfig("https://" + host + "/v1")
			cfg.SendTemperature = false
			cfg.TokenParameter = "max_completion_tokens"
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["temperature"] != nil || body["reasoning_effort"] != nil || body["max_tokens"] != nil {
					t.Error("unexpected optional parameter")
				}
				if body["max_completion_tokens"] != float64(cfg.MaxTokens) {
					t.Error("missing budget")
				}
				if host == "api.openai.com" && body["store"] != false {
					t.Error("missing store=false")
				}
				if host == "api.deepseek.com" {
					think, ok := body["thinking"].(map[string]any)
					if !ok || think["type"] != "disabled" {
						t.Error("missing thinking override")
					}
				} else if body["thinking"] != nil {
					t.Error("vendor parameter sent to wrong provider")
				}
				return response("text/event-stream", sampleSSE), nil
			})
			if _, err := client.Complete(context.Background(), cfg, TextContext{}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAPIConfigDefaultsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	partial := `{"provider":"openai-compatible","endpoint":"http://localhost:1234/v1","model":"my-model","suggest_hotkey":"Ctrl+Alt+N","automatic_suggestions":true}`
	if err := os.WriteFile(path, []byte(partial), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowRemote || cfg.AllowRemoteAuto || cfg.TokenParameter != "max_tokens" || !cfg.SendTemperature || cfg.SuggestHotkey != "Ctrl+Alt+N" || !cfg.Auto {
		t.Fatal("unsafe defaults for partial configuration")
	}
	cfg.Endpoint = "https://api.example.com/v1"
	cfg.AllowRemote = true
	u, _ := cfg.RequestURL()
	cfg.RemoteConsent = u.String()
	cfg.EncryptedAPIKeys = map[string]string{u.String(): "dpapi:FAKE_CIPHERTEXT_FOR_UNIT_TEST_ONLY"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got.EncryptedAPIKeys[u.String()] != cfg.EncryptedAPIKeys[u.String()] || got.RemoteConsent != cfg.RemoteConsent {
		t.Fatal("API config did not persist", err)
	}
	clone := got.Clone()
	delete(clone.EncryptedAPIKeys, u.String())
	clone.AllowedApps[0] = "other.exe"
	if got.EncryptedAPIKeys[u.String()] == "" || got.AllowedApps[0] == "other.exe" {
		t.Fatal("clone shares mutable state")
	}
}

func TestAutomaticRemoteRequiresSeparatePermission(t *testing.T) {
	local := DefaultConfig()
	local.Auto = true
	if !local.AutomaticAllowed() {
		t.Fatal("local automatic unexpectedly disabled")
	}
	remote := remoteConfig("https://example.com")
	remote.Auto = true
	if remote.AutomaticAllowed() {
		t.Fatal("remote automatic enabled by default")
	}
	remote.AllowRemoteAuto = true
	if !remote.AutomaticAllowed() {
		t.Fatal("approved remote automatic disabled")
	}
	remote.Auto = false
	if remote.AutomaticAllowed() {
		t.Fatal("global automatic off ignored")
	}
	remote.Auto = true
	remote.RemoteConsent = ""
	if remote.AutomaticAllowed() {
		t.Fatal("unapproved automatic request allowed")
	}
}

func TestAPIKeyValidationAndConfigBounds(t *testing.T) {
	for _, key := range []string{"ok-token", "sk-example_-123", ""} {
		if err := ValidateAPIKey(key); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"white space", "bad\nkey", "bad\rkey", "bad\x00key", "中文", strings.Repeat("x", 8193)} {
		if ValidateAPIKey(key) == nil {
			t.Fatal("unsafe key accepted")
		}
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.TokenParameter = "arbitrary_field" },
		func(c *Config) { c.ReasoningEffort = "invalid" },
		func(c *Config) { c.APIKeyEnv = "API\nKEY" },
		func(c *Config) { c.MaxTokens = 4097 },
		func(c *Config) { c.EncryptedAPIKeys = map[string]string{"scope": "plaintext-secret"} },
	} {
		c := DefaultConfig()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("invalid API configuration accepted")
		}
	}
}

func TestResponseLimits(t *testing.T) {
	for _, tc := range []struct{ name, ctype, body string }{
		{"json body", "application/json", strings.Repeat(" ", 2<<20) + "{}"},
		{"large content", "application/json", `{"choices":[{"message":{"content":"` + strings.Repeat("x", 65537) + `"},"finish_reason":"stop"}]}`},
		{"large event", "text/event-stream", "data: " + strings.Repeat("x", 600000) + "\n\n"},
		{"many events", "text/event-stream", strings.Repeat(": heartbeat padding padding\n", 90000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.ctype, tc.body), nil })
			if _, err := client.Complete(context.Background(), remoteConfig("https://example.com"), TextContext{}, nil); err == nil {
				t.Fatal("unbounded response accepted")
			}
		})
	}
}

func TestAutomaticRemoteThrottle(t *testing.T) {
	c := remoteConfig("https://example.com")
	c.Auto = true
	c.AllowRemoteAuto = true
	now := time.Now()
	if c.AutomaticDue(now, now.Add(-2*time.Second)) {
		t.Fatal("automatic request too soon")
	}
	if !c.AutomaticDue(now, now.Add(-3*time.Second)) {
		t.Fatal("automatic request delayed beyond interval")
	}
	c.Endpoint = "http://127.0.0.1:1234"
	if !c.AutomaticDue(now, now) {
		t.Fatal("local request incorrectly throttled")
	}
	c.Auto = false
	if c.AutomaticDue(now, time.Time{}) {
		t.Fatal("disabled automatic request allowed")
	}
}
