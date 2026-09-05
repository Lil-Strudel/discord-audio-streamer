package config

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// entropy is mixed into the encryption so that a blob taken from this app's
// config cannot be decrypted by another program running as the same user
// without knowing this value. It is obfuscation, not a secret: DPAPI's real
// protection is that the key is derived from the user's Windows account.
var entropy = []byte("DiscordAudioStreamer/token/v1")

// sealSecret encrypts data with DPAPI, scoped to the current user.
//
// The key never leaves Windows, so a config file copied to another machine or
// opened by another user account is useless. That is the whole point of using
// DPAPI rather than a key we would have to embed in the binary, where anyone
// with the exe could recover it.
func sealSecret(data []byte) ([]byte, error) {
	in := newBlob(data)
	extra := newBlob(entropy)

	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &extra, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	return copyBlob(out), nil
}

// unsealSecret decrypts a DPAPI blob produced by sealSecret.
func unsealSecret(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrNoToken
	}

	in := newBlob(data)
	extra := newBlob(entropy)

	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &extra, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	return copyBlob(out), nil
}

func newBlob(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// copyBlob copies out of memory Windows owns and we are about to free.
func copyBlob(b windows.DataBlob) []byte {
	return append([]byte(nil), unsafe.Slice(b.Data, b.Size)...)
}
