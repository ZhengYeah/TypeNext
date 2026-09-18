//go:build windows && amd64

package win

import (
	"testing"
	"unsafe"
)

func TestWindowsX64ABI(t *testing.T) {
	tests := []struct {
		name      string
		got, want uintptr
	}{
		{"INPUT", unsafe.Sizeof(input{}), 40},
		{"MSG", unsafe.Sizeof(msg{}), 48},
		{"WNDCLASSEXW", unsafe.Sizeof(windowClass{}), 80},
		{"GUITHREADINFO", unsafe.Sizeof(guiThreadInfo{}), 72},
		{"NOTIFYICONDATAW", unsafe.Sizeof(notifyIconData{}), 976},
		{"VARIANT", unsafe.Sizeof(variant{}), 24},
		{"PAINTSTRUCT", unsafe.Sizeof(paintStruct{}), 72},
		{"INPUT.ki", unsafe.Offsetof(input{}.VK), 8},
		{"INPUT.ki.dwExtraInfo", unsafe.Offsetof(input{}.Extra), 24},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %d want %d", tc.got, tc.want)
			}
		})
	}
}
