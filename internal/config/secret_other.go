//go:build !windows

package config

// Outside Windows there is no DPAPI, and this is a development-only path: the
// release target is Windows. The token is stored as-is in a file the save path
// creates with owner-only permissions.
//
// If this app ever ships on Linux or macOS for real, this is where a keyring or
// Keychain integration belongs.

func sealSecret(data []byte) ([]byte, error) { return data, nil }

func unsealSecret(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrNoToken
	}
	return data, nil
}
