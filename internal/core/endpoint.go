package core

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LocalBase validates a loopback-only server URL.
func LocalBase(raw string) (*url.URL, error) { return ServerBase(raw, false) }

// ServerBase never permits URL credentials, query-string secrets, or plaintext
// remote HTTP. Loopback remains usable without consent and without DNS lookup.
func ServerBase(raw string, allowRemote bool) (*url.URL, error) {
	if strings.ContainsAny(raw, "\r\n\t\x00") {
		return nil, errors.New("server URL cannot contain control characters")
	}
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 2048 || strings.ContainsAny(raw, "\\\r\n\t\x00") {
		return nil, errors.New("enter a valid server URL (at most 2048 characters)")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" {
		return nil, errors.New("enter a valid server URL including http:// or https://")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("server URL must use http or https")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("server URL cannot contain credentials, a query, or a fragment")
	}
	if strings.ContainsAny(u.Path, "\\\r\n\t\x00") {
		return nil, errors.New("server path contains invalid characters")
	}
	// Reject ambiguous path encodings rather than normalizing an authorization
	// boundary differently from the server. Ordinary API paths need none.
	if u.RawPath != "" || strings.Contains(u.Path, "//") {
		return nil, errors.New("use an ordinary unescaped API path without repeated slashes")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("API paths cannot contain dot segments")
		}
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip, ipErr := netip.ParseAddr(host)
	local := ipErr == nil && ip.IsLoopback() && ip.Zone() == ""
	if !local {
		if !allowRemote {
			return nil, errors.New("remote APIs are off; enable Allow remote HTTPS API requests in API settings")
		}
		if u.Scheme != "https" {
			return nil, errors.New("remote API connections require HTTPS; HTTP is allowed only for loopback")
		}
		if ipErr == nil && (ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast()) {
			return nil, errors.New("invalid remote API address")
		}
		if ipErr != nil {
			if len(host) > 253 || strings.HasSuffix(host, ".") {
				return nil, errors.New("invalid API hostname")
			}
			for _, label := range strings.Split(host, ".") {
				if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
					return nil, errors.New("invalid API hostname")
				}
				for _, r := range label {
					if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
						return nil, errors.New("use an ASCII or punycode API hostname")
					}
				}
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("server port cannot be empty")
	}
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return nil, errors.New("server port must be 1–65535")
		}
		port = strconv.Itoa(p)
		if (port == "443" && u.Scheme == "https") || (port == "80" && u.Scheme == "http") {
			port = ""
		}
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if ipErr == nil && ip.Is6() {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// RequestURL accepts either a base URL or the complete chat endpoint.
// A bare OpenAI-compatible host gets /v1.
func (c Config) RequestURL() (*url.URL, error) {
	u, err := ServerBase(c.Endpoint, c.AllowRemote)
	if err != nil {
		return nil, err
	}
	switch c.Provider {
	case "ollama":
		if !strings.HasSuffix(u.Path, "/api/chat") {
			u.Path = strings.TrimSuffix(u.Path, "/api") + "/api/chat"
		}
	case "openai-compatible":
		if !strings.HasSuffix(u.Path, "/chat/completions") {
			if u.Path == "" {
				u.Path = "/v1"
			}
			u.Path += "/chat/completions"
		}
	default:
		return nil, errors.New("unsupported API protocol")
	}
	return u, nil
}

func (c Config) IsRemote() bool {
	_, err := LocalBase(c.Endpoint)
	return err != nil // Fail closed on invalid URLs.
}

func (c Config) CheckConsent() error {
	u, err := c.RequestURL()
	if err != nil {
		return err
	}
	if c.IsRemote() && c.RemoteConsent != u.String() {
		return errors.New("this remote endpoint has not been approved; open API settings and click Save model")
	}
	return nil
}

func (c Config) AutomaticAllowed() bool {
	return c.Auto && (!c.IsRemote() || c.AllowRemoteAuto) && c.CheckConsent() == nil
}

// Clone avoids mutating maps/slices already in use by an inference goroutine.
func (c Config) Clone() Config {
	out := c
	out.AllowedApps = append([]string(nil), c.AllowedApps...)
	out.ModelProfiles = append([]ModelProfile(nil), c.ModelProfiles...)
	out.EncryptedAPIKeys = make(map[string]string, len(c.EncryptedAPIKeys))
	for k, v := range c.EncryptedAPIKeys {
		out.EncryptedAPIKeys[k] = v
	}
	return out
}

func ValidateAPIKey(key string) error {
	if len(key) > 8192 {
		return errors.New("API key is too long")
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return errors.New("API key must not contain spaces or control characters")
		}
	}
	return nil
}

// AutomaticDue spaces only automatic remote requests; manual requests remain
// explicit user actions. This is a throttle, not a spending cap.
func (c Config) AutomaticDue(now, lastRemoteRequest time.Time) bool {
	return c.AutomaticAllowed() && (!c.IsRemote() || now.Sub(lastRemoteRequest) >= 3*time.Second)
}
