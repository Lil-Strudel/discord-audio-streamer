package discord

import (
	"cmp"
	"iter"
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// Guild is a server the bot has been added to.
type Guild struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IconURL  string `json:"iconUrl"`
	Channels int    `json:"channels"`
}

// Channel is a voice channel the bot can see.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Position mirrors Discord's own ordering, so the list in the app matches
	// what the user sees in Discord itself.
	Position int `json:"position"`

	// Stage marks a stage channel, which behaves differently enough that the
	// UI is better off labelling it than silently offering it as equivalent.
	Stage bool `json:"stage"`
}

// Guilds lists the servers the bot is in, alphabetically.
//
// This reads the gateway cache rather than the REST API: the bot receives a
// GuildCreate for every server it is in while connecting, so the data is
// already here, and a REST call would only add latency and rate limit pressure.
func (c *Client) Guilds() []Guild {
	c.mu.Lock()
	client := c.bot
	c.mu.Unlock()

	if client == nil {
		return nil
	}

	var guilds []Guild
	for g := range client.Caches.Guilds() {
		guilds = append(guilds, Guild{
			ID:       g.ID.String(),
			Name:     g.Name,
			IconURL:  guildIconURL(g.Guild),
			Channels: len(voiceChannelsIn(client.Caches.Channels(), g.ID)),
		})
	}

	slices.SortFunc(guilds, func(a, b Guild) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return guilds
}

// VoiceChannels lists the voice channels of one server, in Discord's own order.
func (c *Client) VoiceChannels(guildID string) []Channel {
	c.mu.Lock()
	client := c.bot
	c.mu.Unlock()

	if client == nil {
		return nil
	}

	id, err := snowflake.Parse(guildID)
	if err != nil {
		return nil
	}

	channels := voiceChannelsIn(client.Caches.Channels(), id)
	slices.SortFunc(channels, func(a, b Channel) int {
		if n := cmp.Compare(a.Position, b.Position); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return channels
}

func voiceChannelsIn(all iter.Seq[discord.GuildChannel], guildID snowflake.ID) []Channel {
	var channels []Channel
	for ch := range all {
		if ch.GuildID() != guildID {
			continue
		}
		// Stage channels are included: a bot can broadcast into one, and a user
		// whose server is set up that way would otherwise see an empty list.
		switch ch.Type() {
		case discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice:
		default:
			continue
		}
		channels = append(channels, Channel{
			ID:       ch.ID().String(),
			Name:     ch.Name(),
			Position: ch.Position(),
			Stage:    ch.Type() == discord.ChannelTypeGuildStageVoice,
		})
	}
	return channels
}

func guildIconURL(g discord.Guild) string {
	if url := g.IconURL(); url != nil {
		return *url
	}
	return ""
}
