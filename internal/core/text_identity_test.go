package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLogicalCaretIgnoresPlacementChanges(t *testing.T) {
	a := TextContext{Window: 42, FocusID: "textbox", CaretID: "caret:1", Prefix: "before", Suffix: "after", X: 12, Y: 34}
	b := a
	b.X, b.Y, b.CaretHeight, b.PositionSource = 200, 400, 20, "field-corner positioning"
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("popup positioning changes must not discard an unchanged logical caret")
	}
	for _, mutate := range []func(*TextContext){
		func(c *TextContext) { c.Window++ },
		func(c *TextContext) { c.FocusID = "another textbox" },
		func(c *TextContext) { c.CaretID = "caret:2" },
		func(c *TextContext) { c.CaretID = "" },
		func(c *TextContext) { c.Prefix += "x" },
		func(c *TextContext) { c.Suffix += "x" },
	} {
		b = a
		mutate(&b)
		if a.Fingerprint() == b.Fingerprint() {
			t.Fatal("different text, target, or logical caret accepted as unchanged")
		}
	}
}

func TestContextJSONExcludesCaretIdentity(t *testing.T) {
	c := TextContext{Window: 42, FocusID: "private target", CaretID: "private caret", Prefix: "before", Suffix: "after", X: 12, Y: 34}
	var fields map[string]string
	if err := json.Unmarshal([]byte(ContextJSON(c)), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["prefix"] != c.Prefix || fields["suffix"] != c.Suffix {
		t.Fatal("model context must contain only bounded prefix and suffix")
	}
}

func TestPowerPointApprovalPreservesSavedAllowlist(t *testing.T) {
	if !DefaultConfig().Allows("POWERPNT.EXE") {
		t.Fatal("new configurations should include the PowerPoint adapter")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"allowed_apps":["notepad.exe"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Allows("POWERPNT.EXE") || !cfg.Allows("notepad.exe") {
		t.Fatal("loading saved settings must retain the user's app permissions")
	}
}
