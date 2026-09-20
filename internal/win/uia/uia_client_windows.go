//go:build windows && amd64

package uia

import (
	"fmt"
	"unsafe"
)

var (
	clsidUIA8 = guid{A: 0xe22ad333, B: 0xb25f, C: 0x460c, D: [8]byte{0x83, 0xd0, 0x05, 0x81, 0x10, 0x73, 0x95, 0xc9}}
	iidUIA2   = guid{A: 0x34723aff, B: 0x0c9d, C: 0x49d0, D: [8]byte{0x98, 0x96, 0x7a, 0xb5, 0x2d, 0xf8, 0xcd, 0x8a}}
)

const (
	uiaConnectionTimeoutMS  = 1000
	uiaTransactionTimeoutMS = 1500
)

func createAutomationClass(class, iid guid) (*comObject, uintptr) {
	var out *comObject
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&class)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	return out, hr
}

func createUIAutomation(create func(guid, guid) (*comObject, uintptr)) (*comObject, error) {
	// A Go context cannot interrupt a COM call already in progress. UIA8 exposes
	// native provider timeouts so one unresponsive app does not occupy the worker
	// for the default twenty-second transaction timeout.
	a, hr := create(clsidUIA8, iidUIA2)
	if !failed(hr) && a != nil {
		// IUIAutomation2 inherits the 58-slot IUIAutomation vtable.
		// Slots 61/63 are put_ConnectionTimeout/put_TransactionTimeout.
		for _, setting := range []struct{ slot, milliseconds int }{{61, uiaConnectionTimeoutMS}, {63, uiaTransactionTimeoutMS}} {
			if hr = comCall(a, setting.slot, uintptr(setting.milliseconds)); failed(hr) {
				release(a)
				return nil, fmt.Errorf("cannot configure Windows accessibility timeout (0x%08x)", uint32(hr))
			}
		}
		return a, nil
	}
	release(a)
	// Preserve the existing reader on systems without the newer class/interface.
	// Other initialization failures should be reported rather than hidden.
	if uint32(hr) != 0x80040154 && uint32(hr) != 0x80004002 { // REGDB_E_CLASSNOTREG, E_NOINTERFACE
		return nil, fmt.Errorf("Windows UI Automation is unavailable (0x%08x)", uint32(hr))
	}
	a, hr = create(clsidUIA, iidUIA)
	if failed(hr) || a == nil {
		release(a)
		return nil, fmt.Errorf("Windows UI Automation is unavailable (0x%08x)", uint32(hr))
	}
	return a, nil
}
