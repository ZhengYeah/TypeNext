package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLocalBase(t *testing.T) {
	allowed := map[string]string{
		"http://localhost:11434":   "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/":  "http://127.0.0.1:11434",
		"http://127.2.3.4:1234/v1": "http://127.2.3.4:1234/v1",
		"http://[::1]:1234/v1/":    "http://[::1]:1234/v1",
		"https://127.0.0.1":        "https://127.0.0.1",
	}
	for raw, want := range allowed {
		t.Run(raw, func(t *testing.T) {
			u, e := LocalBase(raw)
			if e != nil || u.String() != want {
				t.Fatalf("got %v %v", u, e)
			}
		})
	}
	for _, raw := range []string{
		"http://example.com", "http://localhost.example.com", "http://192.168.1.2:11434",
		"http://0.0.0.0:11434", "http://[::]:11434", "http://127.0.0.1.evil.test",
		"http://127.0.0.1@evil.test", "http://alice:secret@127.0.0.1", "ftp://127.0.0.1",
		"file:///tmp/test", "http://127.0.0.1?token=x", "http://127.0.0.1#test",
		"http://2130706433", "http://[::1%25eth0]", "", "localhost:11434",
	} {
		t.Run("reject_"+raw, func(t *testing.T) {
			if _, e := LocalBase(raw); e == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
}

func TestConfiguration(t *testing.T) {
	c := DefaultConfig()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "TypeNext", "config.json")
	if e := SaveConfig(path, c); e != nil {
		t.Fatal(e)
	}
	got, e := LoadConfig(path)
	if e != nil || got.Model != c.Model {
		t.Fatalf("load: %+v %v", got, e)
	}
	c.Model = "qwen3:1.7b"
	if e = SaveConfig(path, c); e != nil {
		t.Fatal(e)
	}
	got, e = LoadConfig(path)
	if e != nil || got.Model != c.Model {
		t.Fatal("overwrite failed", e)
	}
	os.WriteFile(path, []byte("bad JSON"), 0600)
	if _, e = LoadConfig(path); e == nil {
		t.Fatal("accepted invalid JSON")
	}
}

func TestConfigBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"remote", func(c *Config) { c.Endpoint = "https://example.com" }},
		{"empty model", func(c *Config) { c.Model = "" }},
		{"cloud model", func(c *Config) { c.Model = "model:cloud" }},
		{"bad provider", func(c *Config) { c.Provider = "remote" }},
		{"small pause", func(c *Config) { c.DebounceMS = 1 }},
		{"large prefix", func(c *Config) { c.PrefixChars = 8001 }},
		{"negative suffix", func(c *Config) { c.SuffixChars = -1 }},
		{"wildcard app", func(c *Config) { c.AllowedApps = []string{"*.exe"} }},
		{"full app path", func(c *Config) { c.AllowedApps = []string{`C:\Word.exe`} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(&c)
			if c.Validate() == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestAllowlist(t *testing.T) {
	c := DefaultConfig()
	for _, app := range []string{"Notepad.exe", "WINWORD.EXE", "POWERPNT.EXE", "WeChat.exe"} {
		if !c.Allows(app) {
			t.Fatal(app)
		}
	}
	for _, app := range []string{"chrome.exe", "Code.exe", "random.exe"} {
		if c.Allows(app) {
			t.Fatal(app)
		}
	}
	for _, app := range []string{"keepass.exe", "cmd.exe", "windowsterminal.exe", "bitwarden.exe"} {
		c.AllowedApps = append(c.AllowedApps, app)
		if c.Allows(app) {
			t.Fatal("hard block bypass", app)
		}
	}
}

func TestTextCleaning(t *testing.T) {
	cases := []struct{ name, raw, prefix, suffix, want string }{
		{"leading whitespace", " preserves privacy.", "The algorithm", "", " preserves privacy."},
		{"Chinese", "保护用户隐私。", "该方法可以", "", "保护用户隐私。"},
		{"reasoning", "<think>do not show me</think>works.", "", "", "works."},
		{"incomplete reasoning", "<think>unfinished", "", "", ""},
		{"prefix echo", "This can protect privacy.", "This can ", "", "protect privacy."},
		{"suffix echo", "protect privacy. Next section.", "This can ", " Next section.", "protect privacy."},
		{"controls", "safe\r\nnext\tpart\x00\x1b\u202e", "", "", "safe next part"},
		{"empty", " \t\n", "", "", ""},
		{"fence", "```python\nprint(1)\n```", "", "", ""},
		{"end token", "useful<|im_end|>bad", "", "", "useful"},
		{"punctuation", "hello.", "", ".", "hello."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanSuggestion(tc.raw, TextContext{Prefix: tc.prefix, Suffix: tc.suffix}, 320)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	s := CleanSuggestion("😀中文abcdefgh", TextContext{}, 3)
	if s != "😀中文" || !utf8.ValidString(s) {
		t.Fatal(s)
	}
	if Head("甲乙丙", 2) != "甲乙" || Tail("甲乙丙", 2) != "乙丙" {
		t.Fatal("rune clipping")
	}
}

func TestContextFingerprint(t *testing.T) {
	a := TextContext{Window: 1, FocusID: "abc", Prefix: "before", Suffix: "after", X: 1, Y: 2}
	b := a
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("unstable")
	}
	mutations := []func(*TextContext){func(x *TextContext) { x.Window++ }, func(x *TextContext) { x.FocusID = "other" }, func(x *TextContext) { x.Prefix += "x" }, func(x *TextContext) { x.Suffix += "x" }, func(x *TextContext) { x.X++ }, func(x *TextContext) { x.Y++ }}
	for _, f := range mutations {
		b = a
		f(&b)
		if b.Fingerprint() == a.Fingerprint() {
			t.Fatal("stale context accepted")
		}
	}
}

func TestOllamaStreaming(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path: %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"message":{"thinking":"private reasoning","content":""},"done":false}`)
		fmt.Fprintln(w, `{"message":{"content":" protect"},"done":false}`)
		w.(http.Flusher).Flush()
		fmt.Fprintln(w, `{"message":{"content":"隐私。"},"done":true}`)
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Endpoint = server.URL
	partials := 0
	result, e := NewClient().Complete(context.Background(), cfg, TextContext{Prefix: "This can"}, func(string) { partials++ })
	if e != nil || result != " protect隐私。" {
		t.Fatalf("%q %v", result, e)
	}
	if received["think"] != false || received["stream"] != true || partials < 2 {
		t.Fatalf("bad request: %#v", received)
	}
	msgs := received["messages"].([]any)
	text := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(text, `"prefix":"This can"`) {
		t.Fatal("missing context")
	}
}

func TestOpenAICompatibleStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keepalive\n\n")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"privacy "},"finish_reason":null}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"matters."},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Provider = "openai-compatible"
	cfg.Endpoint = server.URL
	result, e := NewClient().Complete(context.Background(), cfg, TextContext{}, nil)
	if e != nil || result != "privacy matters." {
		t.Fatalf("%q %v", result, e)
	}
}

func TestHTTPFailures(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"status", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "SECRET_PROMPT", 404) }},
		{"incomplete", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, `{"message":{"content":"partial"},"done":false}`)
		}},
		{"invalid json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "bad") }},
		{"server error", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"error":"SECRET_PROMPT"}`) }},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://example.com", 302) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(tc.handler)
			defer s.Close()
			cfg := DefaultConfig()
			cfg.Endpoint = s.URL
			result, e := NewClient().Complete(context.Background(), cfg, TextContext{}, nil)
			if e == nil || result != "" {
				t.Fatalf("expected safe failure, got %q %v", result, e)
			}
			if strings.Contains(e.Error(), "SECRET_PROMPT") {
				t.Fatal("error leaked response text")
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer s.Close()
	cfg := DefaultConfig()
	cfg.Endpoint = s.URL
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := NewClient().Complete(ctx, cfg, TextContext{}, nil)
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation failed", e)
	}
}

func TestNoProxy(t *testing.T) {
	tr := NewClient().HTTP.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("environment proxies must be disabled")
	}
}

func TestAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("TYPENEXT_TEST_KEY", "sample-key")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sample-key" {
			t.Error("missing API key")
		}
		fmt.Fprintln(w, `{"message":{"content":"ok"},"done":true}`)
	}))
	defer s.Close()
	c := DefaultConfig()
	c.Endpoint = s.URL
	c.APIKeyEnv = "TYPENEXT_TEST_KEY"
	if _, e := NewClient().Complete(context.Background(), c, TextContext{}, nil); e != nil {
		t.Fatal(e)
	}
}
