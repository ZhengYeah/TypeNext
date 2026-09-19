//go:build windows && amd64

package win

import (
	"syscall"
	"testing"
	"unsafe"
)

type readOnlyTestRange struct {
	comObject
	vt    uint16
	value int16
	hr    uintptr
}

var readOnlyTestVTable = [96]uintptr{
	9: syscall.NewCallback(func(r *readOnlyTestRange, attribute uintptr, out *variant) uintptr {
		if attribute != 40015 {
			return 0x80070057
		}
		*out = variant{VT: r.vt}
		*(*int16)(unsafe.Pointer(&out.Data[0])) = r.value
		return r.hr
	}),
}

func TestReadOnlyAttributeRequiresSuccessfulBoolean(t *testing.T) {
	for _, tc := range []struct {
		name            string
		vt              uint16
		value           int16
		hr              uintptr
		readOnly, known bool
	}{
		{"writable", 11, 0, 0, false, true},
		{"read only", 11, -1, 0, true, true},
		{"provider failure", 11, 0, 0x80004005, false, false},
		{"empty attribute", 0, 0, 0, false, false},
		{"unsupported attribute", 13, 0, 0, false, false},
		{"wrong scalar type", 3, 0, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &readOnlyTestRange{comObject: comObject{VTable: &readOnlyTestVTable}, vt: tc.vt, value: tc.value, hr: tc.hr}
			readOnly, known := rangeReadonly(&r.comObject)
			if readOnly != tc.readOnly || known != tc.known {
				t.Fatalf("got readonly=%t known=%t", readOnly, known)
			}
			if err := validateTextEditability(50020, readOnly, known); (err == nil) != (tc.known && !tc.readOnly) {
				t.Fatal("Qt Text control admitted without successful writable metadata")
			}
		})
	}
}
