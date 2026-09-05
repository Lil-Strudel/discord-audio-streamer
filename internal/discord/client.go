package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/pacer"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pipeline"
)

// Timeouts for the operations the UI waits on.
const (
	gatewayTimeout = 30 * time.Second
	joinTimeout    = 30 * time.Second
	leaveTimeout   = 10 * time.Second
)

// State is what the UI shows about the connection.
type State struct {
	Connected bool   `json:"connected"`
	BotName   string `json:"botName"`
	BotAvatar string `json:"botAvatar"`
	GuildID   string `json:"guildId"`
	ChannelID string `json:"channelId"`
	InVoice   bool   `json:"inVoice"`
}

// Client owns the bot's gateway connection and its voice session.
type Client struct {
	logger *slog.Logger

	mu       sync.Mutex
	bot      *bot.Client
	conn     voice.Conn
	pipe     *pipeline.Pipeline
	guildID  snowflake.ID
	chanID   snowflake.ID
	info     BotInfo
	ready    chan struct{}
	readyOne sync.Once

	// sender is captured as disgo builds it, so the UI can read pacing stats
	// without disgo needing to expose the sender it created.
	sender atomic.Pointer[pacer.Sender]
}

// New returns a disconnected Client.
func New(logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{logger: logger.With(slog.String("component", "discord"))}
}

// ErrNotConnected is returned when an operation needs a live gateway.
var ErrNotConnected = errors.New("not connected to Discord")

// ErrNotInVoice is returned when an operation needs a voice channel.
var ErrNotInVoice = errors.New("not in a voice channel")

// Connect opens the gateway and waits until the bot's guilds have arrived.
//
// The pipeline is attached to whichever voice channel is joined later; it is
// passed in here because the voice connection has to be built with the audio
// sender already wired, not adjusted afterwards.
func (c *Client) Connect(ctx context.Context, token string, pipe *pipeline.Pipeline) error {
	info, err := Identify(ctx, token)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.bot != nil {
		c.mu.Unlock()
		return errors.New("already connected")
	}
	c.mu.Unlock()

	ready := make(chan struct{})
	var readyOnce sync.Once

	client, err := disgo.New(token,
		bot.WithLogger(c.logger),
		bot.WithGatewayConfigOpts(
			// Neither intent is privileged, so nothing has to be enabled in the
			// developer portal beyond creating the bot. Worth keeping that way:
			// it is one less step in onboarding that can go wrong.
			gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildVoiceStates),
		),
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagGuilds|cache.FlagChannels|cache.FlagVoiceStates),
		),
		bot.WithVoiceManagerConfigOpts(
			// End-to-end encryption. Discord requires it for voice, and
			// golibdave is the binding to Discord's own implementation.
			voice.WithDaveSessionCreateFunc(golibdave.NewSession),
			voice.WithDaveSessionLogger(c.logger),
			// Replace disgo's sender with one that cannot drift.
			voice.WithConnConfigOpts(voice.WithConnAudioSenderCreateFunc(c.newSender)),
		),
		bot.WithEventListenerFunc(func(_ *events.Ready) {
			readyOnce.Do(func() { close(ready) })
		}),
	)
	if err != nil {
		return fmt.Errorf("create Discord client: %w", err)
	}

	openCtx, cancel := context.WithTimeout(ctx, gatewayTimeout)
	defer cancel()

	if err := client.OpenGateway(openCtx); err != nil {
		client.Close(context.Background())
		return fmt.Errorf("connect to Discord: %w", err)
	}

	select {
	case <-ready:
	case <-openCtx.Done():
		client.Close(context.Background())
		return fmt.Errorf("Discord did not finish the handshake: %w", openCtx.Err())
	}

	c.mu.Lock()
	c.bot = client
	c.pipe = pipe
	c.info = info
	c.ready = ready
	c.mu.Unlock()

	c.logger.Info("connected to Discord", slog.String("bot", info.Name))
	return nil
}

// newSender builds the paced audio sender and remembers it, so pacing stats can
// be surfaced without disgo having to hand the sender back.
func (c *Client) newSender(logger *slog.Logger, provider voice.OpusFrameProvider, conn voice.Conn) voice.AudioSender {
	sender := pacer.New(logger, provider, conn)
	if s, ok := sender.(*pacer.Sender); ok {
		c.sender.Store(s)
	}
	return sender
}

// Disconnect leaves any voice channel and closes the gateway.
func (c *Client) Disconnect(ctx context.Context) {
	c.mu.Lock()
	client, conn := c.bot, c.conn
	c.bot, c.conn, c.pipe = nil, nil, nil
	c.guildID, c.chanID = 0, 0
	c.info = BotInfo{}
	c.mu.Unlock()

	c.sender.Store(nil)

	if conn != nil {
		closeCtx, cancel := context.WithTimeout(ctx, leaveTimeout)
		conn.Close(closeCtx)
		cancel()
	}
	if client != nil {
		closeCtx, cancel := context.WithTimeout(ctx, leaveTimeout)
		client.Close(closeCtx)
		cancel()
	}
}

// Join connects to a voice channel and attaches the audio pipeline.
func (c *Client) Join(ctx context.Context, guildID, channelID string) error {
	guild, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	channel, err := snowflake.Parse(channelID)
	if err != nil {
		return fmt.Errorf("invalid channel id: %w", err)
	}

	c.mu.Lock()
	client, pipe, existing := c.bot, c.pipe, c.conn
	c.mu.Unlock()

	if client == nil {
		return ErrNotConnected
	}

	// Moving between channels reuses the connection for the guild it is already
	// in; a different guild needs a fresh one.
	if existing != nil && existing.GuildID() != guild {
		leaveCtx, cancel := context.WithTimeout(ctx, leaveTimeout)
		existing.Close(leaveCtx)
		cancel()
		existing = nil
	}

	conn := existing
	if conn == nil {
		conn = client.VoiceManager.CreateConn(guild)
	}

	joinCtx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()

	// Deafen the bot. It never listens to anything, and being deafened tells
	// everyone else in the channel exactly that.
	if err := conn.Open(joinCtx, channel, false, true); err != nil {
		return fmt.Errorf("join voice channel: %w", err)
	}

	// Attaching the provider is what starts the sender; disgo closes any
	// previous sender for this connection as part of the swap.
	conn.SetOpusFrameProvider(pipe)

	c.mu.Lock()
	c.conn, c.guildID, c.chanID = conn, guild, channel
	c.mu.Unlock()

	c.logger.Info("joined voice channel",
		slog.String("guild", guildID), slog.String("channel", channelID))
	return nil
}

// Leave disconnects from the voice channel but stays on the gateway.
func (c *Client) Leave(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.conn, c.guildID, c.chanID = nil, 0, 0
	c.mu.Unlock()

	if conn == nil {
		return nil
	}

	c.sender.Store(nil)

	closeCtx, cancel := context.WithTimeout(ctx, leaveTimeout)
	defer cancel()
	conn.Close(closeCtx)
	return nil
}

// State describes the connection for the UI.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := State{
		Connected: c.bot != nil,
		BotName:   c.info.Name,
		BotAvatar: c.info.AvatarURL,
		InVoice:   c.conn != nil,
	}
	if c.guildID != 0 {
		state.GuildID = c.guildID.String()
	}
	if c.chanID != 0 {
		state.ChannelID = c.chanID.String()
	}
	return state
}

// Info returns the identity of the connected bot.
func (c *Client) Info() BotInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// PacerStats returns the sender's timing statistics, if audio is flowing.
func (c *Client) PacerStats() (pacer.Stats, bool) {
	if s := c.sender.Load(); s != nil {
		return s.Stats(), true
	}
	return pacer.Stats{}, false
}

// EncryptionReady reports whether the DAVE session has an active epoch. While
// this is false the pacer holds frames back rather than sending them in the
// clear, so the UI can explain a brief silence after joining.
func (c *Client) EncryptionReady() bool {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return false
	}
	dave := conn.DAVE()
	return dave == nil || dave.Ready()
}
