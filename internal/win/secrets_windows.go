//go:build windows && amd64

package win

import (
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"typenext/internal/core"
	"unsafe"
)

var (
	crypt32             = syscall.NewLazyDLL("crypt32.dll")
	pCryptProtectData   = crypt32.NewProc("CryptProtectData")
	pCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	pLocalFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	Size uint32
	Data *byte
}

// x64 DATA_BLOB has four bytes of padding before its pointer.
var _ [16 - unsafe.Sizeof(dataBlob{})]byte
var _ [unsafe.Sizeof(dataBlob{}) - 16]byte

func blob(data []byte) dataBlob {
	if len(data) == 0 {
		return dataBlob{}
	}
	return dataBlob{Size: uint32(len(data)), Data: &data[0]}
}
func zero(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// Uses current-user DPAPI, NOT CRYPTPROTECT_LOCAL_MACHINE. The canonical endpoint
// is additional entropy, so a copied ciphertext is not usable for another URL.
func protectAPIKey(key, scope string) (string, error) {
	if err := core.ValidateAPIKey(key); err != nil {
		return "", err
	}
	if key == "" {
		return "", errors.New("cannot save an empty API key")
	}
	data := []byte(key)
	defer zero(data)
	entropy := []byte("TypeNext API key\x00" + scope)
	in, extra := blob(data), blob(entropy)
	var out dataBlob
	ok, _, _ := pCryptProtectData.Call(uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&extra)), 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if ok == 0 {
		return "", errors.New("Windows could not encrypt the API key; it was not saved")
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 || out.Size > 16384 {
		return "", errors.New("invalid Windows encrypted-key result")
	}
	return "dpapi:" + base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, int(out.Size))), nil
}

func unprotectAPIKey(cipher, scope string) (string, error) {
	if !strings.HasPrefix(cipher, "dpapi:") || len(cipher) > 20000 {
		return "", errors.New("invalid encrypted-key format")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cipher, "dpapi:"))
	if err != nil || len(data) == 0 {
		return "", errors.New("invalid encrypted-key encoding")
	}
	entropy := []byte("TypeNext API key\x00" + scope)
	in, extra := blob(data), blob(entropy)
	var out dataBlob
	ok, _, _ := pCryptUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&extra)), 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if ok == 0 {
		return "", errors.New("Windows could not unlock this API key")
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 || out.Size > 8192 {
		return "", errors.New("invalid decrypted-key size")
	}
	plain := unsafe.Slice(out.Data, int(out.Size))
	defer zero(plain)
	return string(plain), nil
}
