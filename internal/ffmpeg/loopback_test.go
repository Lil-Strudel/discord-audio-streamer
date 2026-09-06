package ffmpeg

import "testing"

func TestLooksLikeLoopbackIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{
		"CABLE Output (VB-Audio Virtual Cable)",
		"voicemeeter aux output",
		"Stereo Mix (Realtek)",
	} {
		if !looksLikeLoopback(name) {
			t.Errorf("%q was not recognised as carrying desktop audio", name)
		}
	}
	for _, name := range []string{"Microphone (Realtek Audio)", "Line In", "Headset Earphone"} {
		if looksLikeLoopback(name) {
			t.Errorf("%q was wrongly recognised as carrying desktop audio", name)
		}
	}
}
