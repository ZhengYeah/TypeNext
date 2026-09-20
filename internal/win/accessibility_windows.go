//go:build windows && amd64

package win

import "typenext/internal/win/uia"

func newAccessibilityWorker() (*uia.Worker, error) {
	return uia.New(uia.Host{
		Foreground:     foreground,
		ProcessOf:      processOf,
		Composing:      composing,
		NativeCaret:    nativeCaret,
		WaitRelease:    waitRelease,
		ModifiersDown:  modifiersDown,
		SendUnicode:    sendUnicode,
		ReadPowerPoint: readPowerPointContext,
	})
}
