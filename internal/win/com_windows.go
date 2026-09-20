//go:build windows && amd64

package win

import "typenext/internal/win/com"

type comObject = com.Object
type variant = com.Variant
type guid = com.GUID

//go:uintptrescapes
func comCall(o *comObject, slot int, args ...uintptr) uintptr {
	return com.Call(o, slot, args...)
}

func failed(hr uintptr) bool { return com.Failed(hr) }
func release(o *comObject)   { com.Release(o) }
