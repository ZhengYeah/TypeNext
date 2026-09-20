//go:build windows && amd64

package uia

import (
	"syscall"
	"typenext/internal/win/com"
)

type comObject = com.Object
type variant = com.Variant
type guid = com.GUID
type rect struct{ Left, Top, Right, Bottom int32 }

//go:uintptrescapes
func comCall(o *comObject, slot int, args ...uintptr) uintptr {
	return com.Call(o, slot, args...)
}

func failed(hr uintptr) bool { return com.Failed(hr) }
func release(o *comObject)   { com.Release(o) }

var (
	ole32                  = syscall.NewLazyDLL("ole32.dll")
	oleaut32               = syscall.NewLazyDLL("oleaut32.dll")
	pCoInitializeEx        = ole32.NewProc("CoInitializeEx")
	pCoUninitialize        = ole32.NewProc("CoUninitialize")
	pCoCreateInstance      = ole32.NewProc("CoCreateInstance")
	pSafeArrayGetLBound    = oleaut32.NewProc("SafeArrayGetLBound")
	pSafeArrayGetUBound    = oleaut32.NewProc("SafeArrayGetUBound")
	pSafeArrayAccessData   = oleaut32.NewProc("SafeArrayAccessData")
	pSafeArrayUnaccessData = oleaut32.NewProc("SafeArrayUnaccessData")
	pSafeArrayDestroy      = oleaut32.NewProc("SafeArrayDestroy")
	pSysStringLen          = oleaut32.NewProc("SysStringLen")
	pSysFreeString         = oleaut32.NewProc("SysFreeString")
	pVariantClear          = oleaut32.NewProc("VariantClear")
)
