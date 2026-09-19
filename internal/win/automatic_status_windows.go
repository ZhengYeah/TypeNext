//go:build windows && amd64

package win

import "typenext/internal/core"

// Keep an enabled-but-blocked automatic mode visible in the main settings,
// rather than making a saved typing delay look as though it is being ignored.
func automaticBlockReason(c core.Config) string {
	if !c.Auto {
		return ""
	}
	if c.IsRemote() && !c.AllowRemoteAuto {
		return "Automatic suggestions blocked: enable Allow automatic remote requests in API settings."
	}
	if err := c.CheckConsent(); err != nil {
		return "Automatic suggestions blocked: " + err.Error()
	}
	return ""
}
