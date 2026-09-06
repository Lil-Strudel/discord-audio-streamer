package ffmpeg

import "strings"

// A virtual audio cable presents the desktop mix as an ordinary recording
// device. Those are no longer the only way to stream desktop audio on Windows —
// see the wasapi package — but a user who already has one routed is entitled to
// keep using it, and it should be labelled for what it is rather than looking
// like a microphone.
var loopbackHints = []string{
	"cable output",
	"voicemeeter",
	"vb-audio",
	"virtual cable",
	"stereo mix",
	"what u hear",
	"wave out mix",
}

func looksLikeLoopback(name string) bool {
	lower := strings.ToLower(name)
	for _, hint := range loopbackHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
