// Package discord owns the bot connection: validating a token, listing the
// servers and voice channels the bot can see, and joining one with the audio
// pipeline attached.
package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

// invitePermissions is the permission bitfield the bot needs, and no more:
// View Channel, Connect and Speak. Asking for administrator, as many guides
// do, would be asking a user to hand over their whole server to play music.
const invitePermissions = discord.PermissionViewChannel |
	discord.PermissionConnect |
	discord.PermissionSpeak

// BotInfo identifies the bot behind a token, so onboarding can show the user
// what they just pasted rather than only saying "ok".
type BotInfo struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	AvatarURL     string `json:"avatarUrl"`
	InviteURL     string `json:"inviteUrl"`
}

// ErrInvalidToken is returned when Discord rejects the token.
var ErrInvalidToken = errors.New("Discord rejected this bot token")

// Identify checks a token against Discord and describes the bot it belongs to.
//
// This is a REST call only. Validating without opening a gateway connection
// means onboarding can tell a user their token is wrong immediately, instead of
// leaving them staring at a connection that silently never completes.
func Identify(ctx context.Context, token string) (BotInfo, error) {
	if token == "" {
		return BotInfo{}, ErrInvalidToken
	}

	client := rest.New(rest.NewClient(token))
	app, err := client.GetCurrentApplication(rest.WithCtx(ctx))
	if err != nil {
		var restErr *rest.Error
		if errors.As(err, &restErr) && restErr.Response != nil &&
			restErr.Response.StatusCode == http.StatusUnauthorized {
			return BotInfo{}, ErrInvalidToken
		}
		return BotInfo{}, fmt.Errorf("could not reach Discord: %w", err)
	}

	info := BotInfo{
		ApplicationID: app.ID.String(),
		Name:          app.Name,
		InviteURL:     InviteURL(app.ID.String()),
	}
	if app.Bot != nil {
		info.Name = app.Bot.Username
		info.AvatarURL = app.Bot.EffectiveAvatarURL()
	}
	return info, nil
}

// InviteURL builds the OAuth2 link that adds the bot to a server with voice
// permissions already selected, so the user never has to assemble one or tick
// the right boxes themselves.
func InviteURL(applicationID string) string {
	if applicationID == "" {
		return ""
	}
	query := url.Values{
		"client_id":   {applicationID},
		"scope":       {"bot"},
		"permissions": {strconv.FormatInt(int64(invitePermissions), 10)},
	}
	return "https://discord.com/oauth2/authorize?" + query.Encode()
}
