//go:build windows && amd64

package uia

import (
	"fmt"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type testAutomationClient struct {
	comObject
	refs, failSlot          int
	connection, transaction uintptr
}

var testAutomationClientVTable = [96]uintptr{
	2: syscall.NewCallback(func(a *testAutomationClient) uintptr {
		a.refs--
		return uintptr(a.refs)
	}),
	61: syscall.NewCallback(func(a *testAutomationClient, milliseconds uintptr) uintptr {
		if a.failSlot == 61 {
			return 0x80004005
		}
		a.connection = milliseconds
		return 0
	}),
	63: syscall.NewCallback(func(a *testAutomationClient, milliseconds uintptr) uintptr {
		if a.failSlot == 63 {
			return 0x80004005
		}
		a.transaction = milliseconds
		return 0
	}),
}

func TestUIAutomationInitialization(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		modernError, legacyError uintptr
		failSlot                 int
		fallback, wantError      bool
	}{
		{name: "native timeouts"},
		{name: "older class", modernError: 0x80040154, fallback: true},
		{name: "older interface", modernError: 0x80004002, fallback: true},
		{name: "access denied", modernError: 0x80070005, wantError: true},
		{name: "connection timeout failure", failSlot: 61, wantError: true},
		{name: "transaction timeout failure", failSlot: 63, wantError: true},
		{name: "legacy failure", modernError: 0x80040154, legacyError: 0x80004005, fallback: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modern := &testAutomationClient{comObject: comObject{VTable: &testAutomationClientVTable}, refs: 1, failSlot: tc.failSlot}
			legacy := &testAutomationClient{comObject: comObject{VTable: &testAutomationClientVTable}, refs: 1}
			calls := 0
			a, err := createUIAutomation(func(class, iid guid) (*comObject, uintptr) {
				calls++
				if calls == 1 {
					if class != clsidUIA8 || iid != iidUIA2 {
						t.Fatal("modern creation requested incorrect class/interface")
					}
					return &modern.comObject, tc.modernError
				}
				if calls != 2 || class != clsidUIA || iid != iidUIA {
					t.Fatal("legacy fallback requested incorrect class/interface")
				}
				return &legacy.comObject, tc.legacyError
			})
			if (err != nil) != tc.wantError || (a == nil) != tc.wantError {
				t.Fatalf("client=%p error=%v", a, err)
			}
			if (calls == 2) != tc.fallback {
				t.Fatalf("creation calls=%d, fallback=%v", calls, tc.fallback)
			}
			if !tc.wantError && !tc.fallback && (modern.connection != 1000 || modern.transaction != 1500) {
				t.Fatalf("timeouts=%d/%d", modern.connection, modern.transaction)
			}
			if legacy.connection != 0 || legacy.transaction != 0 {
				t.Fatal("IUIAutomation2 methods were called on the legacy interface")
			}
			release(a)
			if modern.refs != 0 || (calls == 2 && legacy.refs != 0) {
				t.Fatalf("COM references leaked: modern=%d legacy=%d", modern.refs, legacy.refs)
			}
		})
	}
}

func TestUIAutomationNativeTimeouts(t *testing.T) {
	w, err := newUIA(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer close(w.jobs)
	reply := make(chan error, 1)
	w.jobs <- func(a *comObject) {
		var modern *comObject
		hr := comCall(a, 0, uintptr(unsafe.Pointer(&iidUIA2)), uintptr(unsafe.Pointer(&modern)))
		defer release(modern)
		if failed(hr) || modern == nil {
			reply <- fmt.Errorf("native IUIAutomation2 unavailable: 0x%08x", uint32(hr))
			return
		}
		connection, first := scalar(modern, 60)
		transaction, second := scalar(modern, 62)
		if first != nil || second != nil || connection != 1000 || transaction != 1500 {
			reply <- fmt.Errorf("native timeouts=%d/%d, errors=%v/%v", connection, transaction, first, second)
			return
		}
		reply <- nil
	}
	select {
	case err = <-reply:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native UIA timeout verification stalled")
	}
}
