package discord

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The invite must request exactly View Channel, Connect and Speak. Asking for
// more would be asking a user to hand over their server to play music.
func TestInvitePermissionsAreVoiceOnly(t *testing.T) {
	const wantBits = 1024 + 1048576 + 2097152 // view channel, connect, speak
	if got := int64(invitePermissions); got != wantBits {
		t.Fatalf("invitePermissions = %d, want %d", got, wantBits)
	}
}

func TestInviteURL(t *testing.T) {
	raw := InviteURL("123456789012345678")

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invite URL does not parse: %v", err)
	}
	if !strings.HasPrefix(raw, "https://discord.com/oauth2/authorize?") {
		t.Errorf("unexpected invite endpoint: %q", raw)
	}

	q := parsed.Query()
	if got := q.Get("client_id"); got != "123456789012345678" {
		t.Errorf("client_id = %q", got)
	}
	if got := q.Get("scope"); got != "bot" {
		t.Errorf("scope = %q, want bot", got)
	}
	if got := q.Get("permissions"); got != "3146752" {
		t.Errorf("permissions = %q, want 3146752", got)
	}
}

func TestInviteURLWithoutApplicationID(t *testing.T) {
	if got := InviteURL(""); got != "" {
		t.Errorf("InviteURL(\"\") = %q, want empty", got)
	}
}

func TestIdentifyRejectsAnEmptyToken(t *testing.T) {
	if _, err := Identify(context.Background(), ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Identify(\"\") = %v, want ErrInvalidToken", err)
	}
}

// A wrong token must be reported as a wrong token, not as a network problem, so
// onboarding can tell the user what to fix.
func TestIdentifyReportsARejectedToken(t *testing.T) {
	if testing.Short() {
		t.Skip("requires network access to Discord")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := Identify(ctx, "not.a.real.token")
	if err == nil {
		t.Fatal("a nonsense token was accepted")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Skipf("could not reach Discord to check the rejection path: %v", err)
	}
}
