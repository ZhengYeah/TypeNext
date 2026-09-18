//go:build windows && amd64

package win

import "unsafe"

// Both directions are checked, so a size mismatch fails cross-compilation too.
var (
	_ [unsafe.Sizeof(input{}) - 40]byte
	_ [40 - unsafe.Sizeof(input{})]byte
	_ [unsafe.Sizeof(msg{}) - 48]byte
	_ [48 - unsafe.Sizeof(msg{})]byte
	_ [unsafe.Sizeof(windowClass{}) - 80]byte
	_ [80 - unsafe.Sizeof(windowClass{})]byte
	_ [unsafe.Sizeof(guiThreadInfo{}) - 72]byte
	_ [72 - unsafe.Sizeof(guiThreadInfo{})]byte
	_ [unsafe.Sizeof(notifyIconData{}) - 976]byte
	_ [976 - unsafe.Sizeof(notifyIconData{})]byte
	_ [unsafe.Sizeof(variant{}) - 24]byte
	_ [24 - unsafe.Sizeof(variant{})]byte
	_ [unsafe.Sizeof(paintStruct{}) - 72]byte
	_ [72 - unsafe.Sizeof(paintStruct{})]byte
	_ [unsafe.Offsetof(input{}.Extra) - 24]byte
	_ [24 - unsafe.Offsetof(input{}.Extra)]byte
)
