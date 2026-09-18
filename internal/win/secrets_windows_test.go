//go:build windows && amd64

package win

import (
	"strings"
	"testing"
)

func TestDPAPIEndpointBoundRoundTrip(t *testing.T) {
	scope := "https://api.example.test/v1/chat/completions"
	cipher, err := protectAPIKey("sample-key-NOT-A-REAL-CREDENTIAL", scope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipher, "sample-key") {
		t.Fatal("key was not encrypted")
	}
	key, err := unprotectAPIKey(cipher, scope)
	if err != nil || key != "sample-key-NOT-A-REAL-CREDENTIAL" {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := unprotectAPIKey(cipher, "https://another.example.test/v1/chat/completions"); err == nil {
		t.Fatal("wrong endpoint accepted")
	}
}

func TestDPAPIRejectsBadInputs(t *testing.T) {
	for _, v := range []string{"", "plaintext", "dpapi:???", "dpapi:", "dpapi:AA=="} {
		if _, err := unprotectAPIKey(v, "scope"); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if _, err := protectAPIKey("", "scope"); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := protectAPIKey("bad\r\nkey", "scope"); err == nil {
		t.Fatal("bad key accepted")
	}
}
