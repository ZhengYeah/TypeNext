//go:build windows && amd64

// Package com provides the COM ABI shared by Windows accessibility adapters.
package com

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Object holds a COM interface pointer and its vtable.
type Object struct{ VTable *[96]uintptr }

// Variant matches the Windows x64 VARIANT layout.
type Variant struct {
	VT, R1, R2, R3 uint16
	Data           [16]byte
}

// GUID identifies a COM class or interface.
type GUID struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var (
	_ [24 - unsafe.Sizeof(Variant{})]byte
	_ [unsafe.Sizeof(Variant{}) - 24]byte
	_ [16 - unsafe.Sizeof(GUID{})]byte
	_ [unsafe.Sizeof(GUID{}) - 16]byte
)

// Call invokes a COM vtable slot and preserves pointer arguments across the call.
//
//go:uintptrescapes
func Call(o *Object, slot int, args ...uintptr) uintptr {
	if o == nil {
		return 0x80004003
	}
	// UIA and IDispatch calls need at most eight explicit arguments. Keep
	// their argument storage on the stack instead of allocating for each call.
	var storage [9]uintptr
	argv := storage[:]
	if len(args)+1 > len(argv) {
		argv = make([]uintptr, len(args)+1)
	}
	argv = argv[:len(args)+1]
	argv[0] = uintptr(unsafe.Pointer(o))
	copy(argv[1:], args)
	hr, _, _ := syscall.SyscallN(o.VTable[slot], argv...)
	runtime.KeepAlive(o)
	return hr
}

// Failed reports whether an HRESULT indicates failure.
func Failed(hr uintptr) bool { return int32(hr) < 0 }

// Release releases a non-nil COM interface reference.
func Release(o *Object) {
	if o != nil {
		Call(o, 2)
	}
}
