package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/config"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/discord"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/logging"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pipeline"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/playlist"
)

// Events emitted to the frontend.
const (
	// eventStatus fires whenever the shape of the UI should change: connected,
	// joined, a track loaded, playback started or stopped.
	eventStatus = "status"

	// eventTelemetry fires continuously while audio is flowing, carrying the
	// playback position, levels and buffer health.
	eventTelemetry = "telemetry"

	// eventError carries a message worth showing the user.
	eventError = "error"

	// eventQueue fires when the playlist changes. It is separate from the
	// status because a queue can hold hundreds of tracks, and re-sending all of
	// them every time the volume knob moves would be wasteful.
	eventQueue = "queue"
)

// telemetryInterval is how often the meter and position are pushed. Fast enough
// for a level meter to look alive, slow enough not to flood the webview bridge.
const telemetryInterval = 100 * time.Millisecond

// Mode is which audio path is active. Only one may run at a time: they would
// otherwise fight over a single voice connection and the user would hear both.
type Mode string

const (
	ModeIdle    Mode = "idle"
	ModePlayer  Mode = "player"
	ModeCapture Mode = "capture"
)

// Status is the whole view state, delivered in one call so the UI never has to
// stitch together several round trips that could disagree with each other.
type Status struct {
	HasToken  bool            `json:"hasToken"`
	Connected bool            `json:"connected"`
	InVoice   bool            `json:"inVoice"`
	BotName   string          `json:"botName"`
	BotAvatar string          `json:"botAvatar"`
	InviteURL string          `json:"inviteUrl"`
	GuildID   string          `json:"guildId"`
	ChannelID string          `json:"channelId"`
	Mode      Mode            `json:"mode"`
	Playing   bool            `json:"playing"`
	Paused    bool            `json:"paused"`
	Track     *playlist.Track `json:"track"`
	DeviceID  string          `json:"deviceId"`
	Settings  config.Settings `json:"settings"`
	FFmpegErr string          `json:"ffmpegError"`

	// ServersLoaded distinguishes a bot that is in no servers from one whose
	// server list is still arriving, which look identical otherwise.
	ServersLoaded bool `json:"serversLoaded"`
}

// Telemetry is the fast-moving state behind the meters and the seek bar.
type Telemetry struct {
	PositionMs      int64   `json:"positionMs"`
	RMS             float64 `json:"rms"`
	Peak            float64 `json:"peak"`
	BufferedFrames  int     `json:"bufferedFrames"`
	BufferCapacity  int     `json:"bufferCapacity"`
	DroppedFrames   uint64  `json:"droppedFrames"`
	Underruns       uint64  `json:"underruns"`
	FramesSent      uint64  `json:"framesSent"`
	FramesHeld      uint64  `json:"framesHeld"`
	Resyncs         uint64  `json:"resyncs"`
	LatenessMs      float64 `json:"latenessMs"`
	MaxLatenessMs   float64 `json:"maxLatenessMs"`
	EncryptionReady bool    `json:"encryptionReady"`
}

// App is the surface bound to the frontend. Every exported method here is
// callable from TypeScript.
type App struct {
	ctx    context.Context
	logger *slog.Logger

	store  *config.Store
	client *discord.Client

	// queue is safe to use without a.mu: it guards itself, and nothing needs a
	// queue edit and a pipeline change to happen as one atomic step.
	queue *playlist.List

	mu       sync.Mutex
	pipe     *pipeline.Pipeline
	mode     Mode
	track    *playlist.Track
	deviceID string
	// trackPath lets a seek restart ffmpeg at a new offset, which is the only
	// way to move within a one-way decode pipe.
	trackPath string

	telemetryStop func()
	startupErr    string
}

// NewApp creates the bound application object.
func NewApp(logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		logger: logger,
		client: discord.New(logger),
		mode:   ModeIdle,
		// A real queue is restored from the configuration during startup; this
		// empty one keeps the bound methods safe to call before that happens.
		queue: playlist.New(playlist.State{}),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	store, err := config.Open()
	if err != nil {
		a.logger.Error("could not open the configuration", slog.Any("err", err))
		a.startupErr = "Settings could not be loaded, so nothing will be remembered between runs: " + err.Error()
		store, _ = config.OpenAt("")
	}
	a.store = store
	a.queue = playlist.New(store.Settings().Queue)

	// Resolve ffmpeg once at startup rather than at the first play, so a broken
	// installation is reported while the user is still reading the setup screen
	// instead of when they press play.
	if _, err := ffmpeg.Resolve(); err != nil {
		a.logger.Error("ffmpeg is unavailable", slog.Any("err", err))
		a.startupErr = err.Error()
	}
}

func (a *App) shutdown(context.Context) {
	a.stopTelemetry()

	a.mu.Lock()
	pipe := a.pipe
	a.pipe = nil
	a.mu.Unlock()

	if pipe != nil {
		pipe.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.client.Disconnect(ctx)
}

// ---------------------------------------------------------------- onboarding

// HasToken reports whether a bot token has been saved, which decides whether
// the app opens on onboarding or on the player.
func (a *App) HasToken() bool { return a.store.HasToken() }

// ValidateToken checks a token with Discord without saving it, so the
// onboarding wizard can show the bot's name and avatar as confirmation before
// the user commits.
func (a *App) ValidateToken(token string) (discord.BotInfo, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	return discord.Identify(ctx, token)
}

// SaveToken validates and then stores a bot token.
func (a *App) SaveToken(token string) (discord.BotInfo, error) {
	info, err := a.ValidateToken(token)
	if err != nil {
		return discord.BotInfo{}, err
	}
	if err := a.store.SetToken(token); err != nil {
		return discord.BotInfo{}, fmt.Errorf("could not save the token: %w", err)
	}
	a.emitStatus()
	return info, nil
}

// ClearToken forgets the saved token and disconnects.
func (a *App) ClearToken() error {
	if err := a.Disconnect(); err != nil {
		return err
	}
	if err := a.store.ClearToken(); err != nil {
		return err
	}
	a.emitStatus()
	return nil
}

// OpenURL opens a link in the user's browser. The webview refuses to navigate
// away from the app, so the Discord developer portal and the invite link have
// to be handed to the real browser.
func (a *App) OpenURL(url string) { wruntime.BrowserOpenURL(a.ctx, url) }

// LogPath returns the file this run is logging to, for the UI to show.
func (a *App) LogPath() string {
	path, err := logging.Path()
	if err != nil {
		return ""
	}
	return path
}

// OpenLogFolder reveals the log directory in the system file manager.
//
// The logs are the only way to explain a crash after the fact, so finding them
// cannot require knowing where an application stores its configuration.
func (a *App) OpenLogFolder() error {
	dir, err := logging.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	return openInFileManager(dir)
}

// ---------------------------------------------------------------- connection

// Connect opens the gateway using the saved token.
func (a *App) Connect() error {
	token, err := a.store.Token()
	if err != nil {
		return err
	}

	settings := a.store.Settings()
	pipe, err := pipeline.New(a.logger, settings.VolumePercent, settings.Bitrate)
	if err != nil {
		return fmt.Errorf("could not start the audio pipeline: %w", err)
	}
	pipe.SetOnTrackEnd(a.onTrackEnd)

	// Wide enough for the gateway handshake and the wait for the server list
	// that follows it, with room for a slow network on top.
	ctx, cancel := context.WithTimeout(a.ctx, 75*time.Second)
	defer cancel()

	if err := a.client.Connect(ctx, token, pipe); err != nil {
		pipe.Close()
		return err
	}

	a.mu.Lock()
	previous := a.pipe
	a.pipe = pipe
	a.mu.Unlock()

	if previous != nil {
		previous.Close()
	}

	// If the servers were still arriving when Connect gave up waiting, refresh
	// the UI once they land rather than leaving a short list on screen.
	if !a.client.GuildsLoaded() {
		go a.awaitGuilds()
	}

	a.emitStatus()
	return nil
}

// awaitGuilds refreshes the UI when a slow server list finally completes.
func (a *App) awaitGuilds() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if a.client.AwaitGuilds(ctx) {
		a.logger.Info("server list finished loading")
		a.emitStatus()
	}
}

// Disconnect leaves voice and closes the gateway.
func (a *App) Disconnect() error {
	a.stopTelemetry()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.client.Disconnect(ctx)

	a.mu.Lock()
	pipe := a.pipe
	a.pipe = nil
	a.mode = ModeIdle
	a.track = nil
	a.trackPath = ""
	a.mu.Unlock()

	if pipe != nil {
		pipe.Close()
	}

	a.emitStatus()
	return nil
}

// ListGuilds returns the servers the bot is in.
func (a *App) ListGuilds() []discord.Guild { return a.client.Guilds() }

// ListVoiceChannels returns the voice channels of one server.
func (a *App) ListVoiceChannels(guildID string) []discord.Channel {
	return a.client.VoiceChannels(guildID)
}

// JoinChannel connects the bot to a voice channel.
func (a *App) JoinChannel(guildID, channelID string) error {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()

	if err := a.client.Join(ctx, guildID, channelID); err != nil {
		return err
	}

	_ = a.store.Update(func(s *config.Settings) {
		s.LastGuildID, s.LastChannelID = guildID, channelID
	})

	a.startTelemetry()
	a.emitStatus()
	return nil
}

// LeaveChannel disconnects from voice but stays on the gateway.
func (a *App) LeaveChannel() error {
	a.stopSource()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.client.Leave(ctx); err != nil {
		return err
	}

	a.stopTelemetry()
	a.emitStatus()
	return nil
}

// ------------------------------------------------------------------- status

// Status returns the complete view state.
func (a *App) Status() Status {
	state := a.client.State()
	info := a.client.Info()

	a.mu.Lock()
	mode, track, deviceID := a.mode, a.track, a.deviceID
	pipe := a.pipe
	a.mu.Unlock()

	status := Status{
		HasToken:  a.store.HasToken(),
		Connected: state.Connected,
		InVoice:   state.InVoice,
		BotName:   state.BotName,
		BotAvatar: state.BotAvatar,
		InviteURL: info.InviteURL,
		GuildID:   state.GuildID,
		ChannelID: state.ChannelID,
		Mode:      mode,
		Track:     track,
		DeviceID:  deviceID,
		Settings:  a.store.Settings(),
		FFmpegErr: a.startupErr,

		ServersLoaded: a.client.GuildsLoaded(),
	}
	if pipe != nil {
		status.Playing = pipe.Active() && !pipe.Paused()
		status.Paused = pipe.Paused()
	}
	return status
}
