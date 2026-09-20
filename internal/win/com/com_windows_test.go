//go:build windows && amd64

package com

import (
	"fmt"
	"syscall"
	"testing"
)

var testCallVTable = [96]uintptr{
	0: syscall.NewCallback(func(o *Object) uintptr { return 7 }),
	1: syscall.NewCallback(func(o *Object, a, b, c, d uintptr) uintptr {
		return a + 2*b + 3*c + 4*d
	}),
	2: syscall.NewCallback(func(o *Object, a, b, c, d, e, f, g, h uintptr) uintptr {
		return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h
	}),
	3: syscall.NewCallback(func(o *Object, a, b, c, d, e, f, g, h, i uintptr) uintptr {
		return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i
	}),
}

func TestCOMCallArguments(t *testing.T) {
	o := &Object{VTable: &testCallVTable}
	for slot, tc := range []struct {
		args []uintptr
		want uintptr
	}{
		{nil, 7},
		{[]uintptr{1, 2, 3, 4}, 30},
		{[]uintptr{1, 2, 3, 4, 5, 6, 7, 8}, 204},
		{[]uintptr{1, 2, 3, 4, 5, 6, 7, 8, 9}, 285},
	} {
		if got := Call(o, slot, tc.args...); got != tc.want {
			t.Errorf("slot %d returned %d, want %d", slot, got, tc.want)
		}
	}
	if hr := Call(nil, 0); uint32(hr) != 0x80004003 {
		t.Fatalf("nil COM object returned 0x%x", hr)
	}
}

func BenchmarkCOMCall(b *testing.B) {
	o := &Object{VTable: &testCallVTable}
	for slot, args := range [][]uintptr{nil, {1, 2, 3, 4}, {1, 2, 3, 4, 5, 6, 7, 8}} {
		b.Run(fmt.Sprintf("args%d", len(args)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Call(o, slot, args...)
			}
		})
	}
}
