package discord

import (
	"context"
	"errors"
	"testing"
)

// The client must refuse work rather than panic when nothing is connected,
// since the UI can call any of these before onboarding is finished.
func TestDisconnectedClientIsInert(t *testing.T) {
	c := New(nil)

	if state := c.State(); state.Connected || state.InVoice {
		t.Errorf("a fresh client reports %+v", state)
	}
	if guilds := c.Guilds(); guilds != nil {
		t.Errorf("Guilds = %+v, want nil", guilds)
	}
	if channels := c.VoiceChannels("123"); channels != nil {
		t.Errorf("VoiceChannels = %+v, want nil", channels)
	}
	if _, ok := c.PacerStats(); ok {
		t.Error("PacerStats reported statistics with no sender")
	}
	if c.EncryptionReady() {
		t.Error("EncryptionReady reported true with no voice connection")
	}
	if err := c.Leave(context.Background()); err != nil {
		t.Errorf("Leave on a disconnected client = %v, want nil", err)
	}

	// Must not panic or hang.
	c.Disconnect(context.Background())
}

func TestJoinWithoutConnectionFails(t *testing.T) {
	c := New(nil)
	err := c.Join(context.Background(), "123456789012345678", "876543210987654321")
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Join = %v, want ErrNotConnected", err)
	}
}

func TestJoinRejectsMalformedIDs(t *testing.T) {
	c := New(nil)
	if err := c.Join(context.Background(), "not-a-snowflake", "123"); err == nil {
		t.Fatal("Join accepted a malformed server id")
	}
	if err := c.Join(context.Background(), "123456789012345678", "nope"); err == nil {
		t.Fatal("Join accepted a malformed channel id")
	}
}

func TestVoiceChannelsRejectsMalformedGuildID(t *testing.T) {
	c := New(nil)
	if got := c.VoiceChannels("nonsense"); got != nil {
		t.Fatalf("VoiceChannels = %+v, want nil", got)
	}
}
