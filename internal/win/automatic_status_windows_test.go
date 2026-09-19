//go:build windows && amd64

package win

import (
	"strings"
	"testing"
	"typenext/internal/core"
)

func TestAutomaticStatusExplainsSavedButBlockedDelay(t *testing.T) {
	c := core.DefaultConfig()
	c.Auto = true
	c.DebounceMS = 600
	c.Provider = "openai-compatible"
	c.Endpoint = "https://example.com/v1"
	c.AllowRemote = true
	u, err := c.RequestURL()
	if err != nil {
		t.Fatal(err)
	}
	c.RemoteConsent = u.String()
	a := &app{cfg: c}
	if c.AutomaticAllowed() || !strings.Contains(a.connectionSummary(), "enable Allow automatic remote requests in API settings") {
		t.Fatal("saved delay appears active while automatic remote access is disabled")
	}
	// Reporting the blocker must not grant permission or change the endpoint.
	if a.cfg.AllowRemoteAuto || a.cfg.RemoteConsent != c.RemoteConsent {
		t.Fatal("status changed remote permission")
	}
	a.cfg.AllowRemoteAuto = true
	if !a.cfg.AutomaticAllowed() || automaticBlockReason(a.cfg) != "" {
		t.Fatal("approved automatic mode still shown as blocked")
	}
	a.cfg.RemoteConsent = ""
	if !strings.Contains(automaticBlockReason(a.cfg), "not been approved") {
		t.Fatal("missing endpoint approval is hidden")
	}
	a.cfg.Auto = false
	if automaticBlockReason(a.cfg) != "" {
		t.Fatal("manual-only mode reports an automatic blocker")
	}
	local := core.DefaultConfig()
	local.Auto = true
	if automaticBlockReason(local) != "" {
		t.Fatal("local automatic mode requires remote permission")
	}
}
