//go:build windows && amd64

package win

import "testing"

func TestPowerPointPaneFromActualFocus(t *testing.T) {
	// This models the observed Office16 hierarchy: PPTFrameClass -> MDIClient
	// -> mdiClass. Ribbon/search controls are siblings outside the editing pane.
	parents := map[uintptr]uintptr{2: 1, 3: 2, 4: 3, 5: 1, 6: 5, 8: 7, 10: 10}
	classes := map[uintptr]string{1: "PPTFrameClass", 2: "MDIClient", 3: "mdiClass", 4: "edit child", 5: "NetUIHWND", 6: "RICHEDIT60W", 7: "PPTFrameClass", 8: "mdiClass", 10: "cycle"}
	root := func(w uintptr) uintptr {
		if w == 8 {
			return 7
		}
		return 1
	}
	parent := func(w uintptr) uintptr { return parents[w] }
	class := func(w uintptr) string { return classes[w] }
	for _, tc := range []struct {
		name                string
		window, focus, want uintptr
	}{
		{"modern slide pane", 1, 3, 3},
		{"child inside slide pane", 1, 4, 3},
		{"ribbon", 1, 5, 0},
		{"search field with stale slide selection", 1, 6, 0},
		{"another presentation", 1, 8, 0},
		{"frame without text focus", 1, 1, 0},
		{"no focus", 1, 0, 0},
		{"no foreground", 0, 3, 0},
		{"broken parent cycle", 1, 10, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pptPaneFromFocus(tc.window, tc.focus, root, parent, class); got != tc.want {
				t.Fatalf("pane=%d want=%d", got, tc.want)
			}
		})
	}
	for _, supported := range []string{"paneClassDC", "MDICLASS"} {
		classes[3] = supported
		if got := pptPaneFromFocus(1, 4, root, parent, class); got != 3 {
			t.Fatalf("supported class %s was not found", supported)
		}
	}
}
