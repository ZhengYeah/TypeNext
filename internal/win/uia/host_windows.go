//go:build windows && amd64

// Package uia reads bounded text through Windows UI Automation.
package uia

import (
	"errors"
	"time"
	"typenext/internal/core"
)

// Host supplies window and input operations owned by the Windows application.
// Callbacks run on the worker's COM thread, except WaitRelease, which runs on the caller of Insert.
// They must not access application UI state without locking.
type Host struct {
	Foreground     func() uintptr
	ProcessOf      func(uintptr) (uint32, string, error)
	Composing      func(uintptr) bool
	NativeCaret    func(uintptr) (int32, int32, int32, bool)
	WaitRelease    func(time.Duration, ...uintptr) bool
	ModifiersDown  func() bool
	SendUnicode    func(string) error
	ReadPowerPoint func(core.Config, uintptr, string) (core.TextContext, error)
}

// New starts a UI Automation worker using the application's native operations.
func New(host Host) (*Worker, error) {
	if host.Foreground == nil || host.ProcessOf == nil || host.Composing == nil ||
		host.NativeCaret == nil || host.WaitRelease == nil || host.ModifiersDown == nil ||
		host.SendUnicode == nil || host.ReadPowerPoint == nil {
		return nil, errors.New("UI Automation requires window, input, and PowerPoint host operations")
	}
	return newUIA(host)
}
