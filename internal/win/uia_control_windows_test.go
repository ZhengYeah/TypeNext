//go:build windows && amd64

package win

import (
	"fmt"
	"strings"
	"testing"
)

func TestTextControlEligibility(t *testing.T) {
	// Cover every standard UIA control type, including outer windows, lists,
	// list items and buttons that must not become editors merely by exposing
	// TextPattern.
	allowed := map[int32]bool{50004: true, 50020: true, 50025: true, 50030: true, 50033: true}
	for control := int32(50000); control <= 50040; control++ {
		if got := textControlAllowed(control); got != allowed[control] {
			t.Errorf("control %d eligibility = %t, want %t", control, got, allowed[control])
		}
	}
	for _, control := range []int32{-1, 0, 49999, 50041} {
		if textControlAllowed(control) {
			t.Errorf("unknown control %d accepted", control)
		}
	}
}

func TestNonstandardTextRequiresExplicitWritableMetadata(t *testing.T) {
	for _, control := range []int32{50020, 50025, 50033} {
		t.Run(fmt.Sprint(control), func(t *testing.T) {
			if err := validateTextEditability(control, false, true); err != nil {
				t.Fatalf("explicit writable field rejected: %v", err)
			}
			if err := validateTextEditability(control, true, true); err == nil {
				t.Fatal("read-only field accepted")
			}
			for _, readOnly := range []bool{false, true} {
				if err := validateTextEditability(control, readOnly, false); err == nil {
					t.Fatal("unknown writability accepted")
				}
			}
		})
	}
}

func TestStandardTextRetainsUnknownWritabilityCompatibility(t *testing.T) {
	for _, control := range []int32{50004, 50030} {
		if err := validateTextEditability(control, false, false); err != nil {
			t.Errorf("standard control %d rejected missing metadata: %v", control, err)
		}
		if err := validateTextEditability(control, false, true); err != nil {
			t.Errorf("standard control %d rejected writable metadata: %v", control, err)
		}
		if err := validateTextEditability(control, true, true); err == nil {
			t.Errorf("standard control %d accepted read-only metadata", control)
		}
	}
}

func TestUnsupportedRolesStayRejectedWithWritableMetadata(t *testing.T) {
	for _, control := range []int32{0, 50000, 50007, 50008, 50032, 50040} {
		if err := validateTextEditability(control, false, true); err == nil {
			t.Errorf("unsupported control %d accepted explicit writable metadata", control)
		}
	}
}

func TestWeixinOuterWindowExplainsAccessibilitySetting(t *testing.T) {
	for _, process := range []string{"Weixin.exe", "WECHAT.EXE"} {
		message := focusedTextControlError(process, 50032).Error()
		if len([]rune(message)) > 180 {
			t.Fatal("Weixin setup guidance would be truncated in the tray notification")
		}
		for _, want := range []string{"outer window", "Try Settings > General", "读屏优化模式", "no text was read"} {
			if !strings.Contains(message, want) {
				t.Errorf("%s error lacks %q: %s", process, want, message)
			}
		}
	}
	for _, tc := range []struct {
		process string
		control int32
	}{
		{"notepad.exe", 50032},
		{"weixin.exe", 50008},
		{"weixin-helper.exe", 50032},
	} {
		message := focusedTextControlError(tc.process, tc.control).Error()
		if strings.Contains(message, "读屏优化模式") {
			t.Errorf("unrelated role/process received Weixin guidance: %s", message)
		}
		if !strings.Contains(message, fmt.Sprint(tc.control)) || !strings.Contains(message, "no text was read") {
			t.Errorf("generic rejection lacks control metadata or read status: %s", message)
		}
	}
}
