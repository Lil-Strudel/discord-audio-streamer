// Package config persists the bot token and user settings between runs.
//
// The token is a real credential — anyone holding it controls the bot — so it
// is encrypted at rest with whatever the platform provides rather than written
// into the settings file in the clear.
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/playlist"
)

// appDir is the folder name used under the user's configuration directory:
// %AppData% on Windows, ~/.config elsewhere.
const appDir = "DiscordAudioStreamer"

// currentVersion lets a future release recognise and migrate an older file.
const currentVersion = 1

// Settings are the user's preferences, all safe to store in the clear.
type Settings struct {
	// VolumePercent is the output volume, 0 to audio.MaxVolumePercent.
	VolumePercent float64 `json:"volumePercent"`

	// Bitrate is the Opus target bitrate in bits per second.
	Bitrate int `json:"bitrate"`

	// CaptureBufferFrames is the live-capture buffer depth in 20 ms frames.
	CaptureBufferFrames int `json:"captureBufferFrames"`

	// LastGuildID and LastChannelID let the app reoffer the previous channel.
	LastGuildID   string `json:"lastGuildId"`
	LastChannelID string `json:"lastChannelId"`

	// LastCaptureDeviceID is remembered so a returning user does not have to
	// hunt for their virtual cable again.
	LastCaptureDeviceID string `json:"lastCaptureDeviceId"`

	// Queue is the working playlist, restored on the next run. Only the visible
	// order is kept: a shuffled order is regenerated on load, so a restart
	// reshuffles rather than replaying the previous run's sequence.
	Queue playlist.State `json:"queue"`
}

// DefaultSettings returns the settings a first-time user starts with.
func DefaultSettings() Settings {
	return Settings{
		VolumePercent:       100,
		Bitrate:             96_000,
		CaptureBufferFrames: 5,
	}
}

// file is the on-disk representation.
type file struct {
	Version int `json:"version"`

	// EncryptedToken holds the bot token as sealed by the platform. On Windows
	// this is a DPAPI blob tied to the user account, so copying the file to
	// another machine or user yields nothing usable.
	EncryptedToken string `json:"encryptedToken,omitempty"`

	Settings Settings `json:"settings"`
}

// Store reads and writes the configuration file.
type Store struct {
	path string

	mu   sync.RWMutex
	data file
}

// ErrNoToken is returned when no bot token has been saved yet.
var ErrNoToken = errors.New("no bot token has been saved")

// Open loads the configuration, creating an empty one if none exists.
func Open() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate configuration directory: %w", err)
	}
	return OpenAt(filepath.Join(dir, appDir, "config.json"))
}

// OpenAt loads the configuration from an explicit path.
func OpenAt(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: file{Version: currentVersion, Settings: DefaultSettings()},
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var loaded file
	if err := json.Unmarshal(raw, &loaded); err != nil {
		// A corrupt file must not brick the app. Starting fresh loses only
		// preferences and a token the user can paste again.
		return s, nil
	}

	s.data = loaded
	s.data.Version = currentVersion
	s.data.Settings = withDefaults(loaded.Settings)
	return s, nil
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// HasToken reports whether a token has been saved.
func (s *Store) HasToken() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.EncryptedToken != ""
}

// Token decrypts and returns the saved bot token.
func (s *Store) Token() (string, error) {
	s.mu.RLock()
	blob := s.data.EncryptedToken
	s.mu.RUnlock()

	if blob == "" {
		return "", ErrNoToken
	}

	sealed, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", fmt.Errorf("stored token is corrupt: %w", err)
	}

	token, err := unsealSecret(sealed)
	if err != nil {
		// This is what a config file copied from another machine or user
		// account looks like, so say so rather than reporting a crypto error.
		return "", fmt.Errorf("stored token could not be read; it may have been saved by a different Windows user: %w", err)
	}
	return string(token), nil
}

// SetToken encrypts and saves the bot token.
func (s *Store) SetToken(token string) error {
	sealed, err := sealSecret([]byte(token))
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}

	s.mu.Lock()
	s.data.EncryptedToken = base64.StdEncoding.EncodeToString(sealed)
	s.mu.Unlock()

	return s.Save()
}

// ClearToken forgets the saved token.
func (s *Store) ClearToken() error {
	s.mu.Lock()
	s.data.EncryptedToken = ""
	s.mu.Unlock()
	return s.Save()
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Settings
}

// SetSettings replaces the settings and writes them out.
func (s *Store) SetSettings(settings Settings) error {
	s.mu.Lock()
	s.data.Settings = withDefaults(settings)
	s.mu.Unlock()
	return s.Save()
}

// Update applies a change to the settings and writes them out.
func (s *Store) Update(fn func(*Settings)) error {
	s.mu.Lock()
	fn(&s.data.Settings)
	s.data.Settings = withDefaults(s.data.Settings)
	s.mu.Unlock()
	return s.Save()
}

// Save writes the configuration to disk.
func (s *Store) Save() error {
	s.mu.RLock()
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}

	// Write to a sibling and rename, so an interrupted save cannot leave a
	// half-written file that fails to parse on the next launch.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write configuration: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("restrict configuration permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close configuration: %w", err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("install configuration to %s: %w", s.path, err)
	}
	return nil
}

// withDefaults fills in anything missing or out of range, so a hand-edited or
// partially written file cannot leave the app with a zero bitrate or a silent
// volume it never asked for.
func withDefaults(s Settings) Settings {
	defaults := DefaultSettings()
	if s.VolumePercent <= 0 {
		s.VolumePercent = defaults.VolumePercent
	}
	if s.Bitrate <= 0 {
		s.Bitrate = defaults.Bitrate
	}
	if s.CaptureBufferFrames <= 0 {
		s.CaptureBufferFrames = defaults.CaptureBufferFrames
	}
	s.Queue = withQueueDefaults(s.Queue)
	return s
}

// withQueueDefaults drops queue entries a hand-edited or older file could carry
// that the playlist could not make sense of. The playlist package repeats these
// checks when it loads the state, but doing them here too means a bad entry is
// dropped from the file on the next save rather than lingering in it.
func withQueueDefaults(q playlist.State) playlist.State {
	tracks := q.Tracks[:0:0]
	for _, t := range q.Tracks {
		if t.Path == "" || t.ID == "" {
			continue
		}
		tracks = append(tracks, t)
	}
	q.Tracks = tracks

	if !q.Repeat.Valid() {
		q.Repeat = playlist.RepeatOff
	}

	// A current id naming no entry would leave the player pointing at nothing.
	found := false
	for _, t := range q.Tracks {
		if t.ID == q.CurrentID {
			found = true
			break
		}
	}
	if !found {
		q.CurrentID = ""
	}
	return q
}
