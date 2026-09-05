package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAt(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	return s
}

func TestFreshStoreHasDefaultsAndNoToken(t *testing.T) {
	s := newStore(t)

	if s.HasToken() {
		t.Error("a fresh store reports a saved token")
	}
	if _, err := s.Token(); !errors.Is(err, ErrNoToken) {
		t.Errorf("Token = %v, want ErrNoToken", err)
	}
	if got, want := s.Settings(), DefaultSettings(); got != want {
		t.Errorf("Settings = %+v, want %+v", got, want)
	}
}

func TestTokenRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}

	const token = "MTIzNDU2Nzg5MDEyMzQ1Njc4.GhIjKl.exampleTokenValueNotReal"
	if err := s.SetToken(token); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	reopened, err := OpenAt(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.HasToken() {
		t.Fatal("token did not survive a reopen")
	}
	got, err := reopened.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != token {
		t.Fatalf("Token = %q, want %q", got, token)
	}
}

// The token must not be recoverable by reading the file, which is the entire
// reason it is sealed rather than stored as a plain field.
func TestTokenIsNotStoredInTheClearOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("token sealing is DPAPI-backed on Windows only; elsewhere this is a development path")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := OpenAt(path)

	const token = "MTIzNDU2Nzg5MDEyMzQ1Njc4.GhIjKl.exampleTokenValueNotReal"
	if err := s.SetToken(token); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the token appears verbatim in the configuration file")
	}
}

func TestClearToken(t *testing.T) {
	s := newStore(t)
	if err := s.SetToken("something"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearToken(); err != nil {
		t.Fatalf("ClearToken: %v", err)
	}

	if s.HasToken() {
		t.Error("HasToken still reports a token after ClearToken")
	}
	reopened, _ := OpenAt(s.Path())
	if reopened.HasToken() {
		t.Error("the cleared token came back after a reopen")
	}
}

func TestSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := OpenAt(path)

	want := Settings{
		VolumePercent:       65,
		Bitrate:             128_000,
		CaptureBufferFrames: 8,
		LastGuildID:         "123",
		LastChannelID:       "456",
		LastCaptureDeviceID: "@device_cm_{abc}",
	}
	if err := s.SetSettings(want); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	reopened, _ := OpenAt(path)
	if got := reopened.Settings(); got != want {
		t.Fatalf("Settings = %+v, want %+v", got, want)
	}
}

func TestUpdateAppliesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := OpenAt(path)

	if err := s.Update(func(cfg *Settings) { cfg.VolumePercent = 42 }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	reopened, _ := OpenAt(path)
	if got := reopened.Settings().VolumePercent; got != 42 {
		t.Fatalf("VolumePercent = %v, want 42", got)
	}
}

// A hand-edited or partially written file must not leave the app muted or with
// a zero bitrate that libopus would reject.
func TestOutOfRangeSettingsFallBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"version":1,"settings":{"volumePercent":0,"bitrate":0,"captureBufferFrames":0}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}

	got, want := s.Settings(), DefaultSettings()
	if got.VolumePercent != want.VolumePercent || got.Bitrate != want.Bitrate ||
		got.CaptureBufferFrames != want.CaptureBufferFrames {
		t.Fatalf("Settings = %+v, want the defaults %+v", got, want)
	}
}

// Losing preferences is annoying; refusing to start is worse.
func TestCorruptFileStartsFreshInsteadOfFailing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt on a corrupt file returned %v, want a fresh store", err)
	}
	if s.HasToken() {
		t.Error("a corrupt file produced a token")
	}
	if got, want := s.Settings(), DefaultSettings(); got != want {
		t.Errorf("Settings = %+v, want the defaults", got)
	}
}

func TestSaveCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "config.json")
	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("configuration was not written: %v", err)
	}
}

func TestSavedFileIsValidJSONWithAVersion(t *testing.T) {
	s := newStore(t)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
	if decoded.Version != currentVersion {
		t.Errorf("version = %d, want %d", decoded.Version, currentVersion)
	}
}

// Owner-only permissions matter on the development path, where the token is not
// otherwise protected.
func TestSavedFilePermissionsAreRestrictive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful here")
	}

	s := newStore(t)
	if err := s.SetToken("secret"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions are %o, want 600", perm)
	}
}

func TestSealRoundTrip(t *testing.T) {
	sealed, err := sealSecret([]byte("hello"))
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	got, err := unsealSecret(sealed)
	if err != nil {
		t.Fatalf("unsealSecret: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("round trip gave %q", got)
	}
}
