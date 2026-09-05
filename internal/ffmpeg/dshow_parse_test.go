package ffmpeg

import (
	"strings"
	"testing"
)

// Real output from a recent ffmpeg on a machine with a virtual cable installed.
const modernListing = `
[dshow @ 000001c4b4c0] "HD Pro Webcam C920" (video)
[dshow @ 000001c4b4c0]   Alternative name "@device_pnp_\\?\usb#vid_046d&pid_082d"
[dshow @ 000001c4b4c0] "Microphone (HD Pro Webcam C920)" (audio)
[dshow @ 000001c4b4c0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{B1F1A2B3}"
[dshow @ 000001c4b4c0] "CABLE Output (VB-Audio Virtual Cable)" (audio)
[dshow @ 000001c4b4c0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{C2A3B4C5}"
dummy: Immediate exit requested
`

// Older ffmpeg builds group devices under section headers instead of tagging
// each line, and a user's bundled ffmpeg is not something we control.
const legacyListing = `
[dshow @ 000001c4b4c0] DirectShow video devices
[dshow @ 000001c4b4c0]  "Integrated Camera"
[dshow @ 000001c4b4c0]     Alternative name "@device_pnp_\\?\usb#vid_5986"
[dshow @ 000001c4b4c0] DirectShow audio devices
[dshow @ 000001c4b4c0]  "Microphone (Realtek Audio)"
[dshow @ 000001c4b4c0]     Alternative name "@device_cm_{33D9A762}\wave_{AAAA}"
[dshow @ 000001c4b4c0]  "VoiceMeeter Output (VB-Audio VoiceMeeter VAIO)"
[dshow @ 000001c4b4c0]     Alternative name "@device_cm_{33D9A762}\wave_{BBBB}"
dummy: Immediate exit requested
`

func parseListing(t *testing.T, listing string) []CaptureDevice {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(listing, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return parseDshowDevices(lines)
}

func TestParseDshowSkipsVideoDevices(t *testing.T) {
	devices := parseListing(t, modernListing)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2 audio devices: %+v", len(devices), devices)
	}
	for _, d := range devices {
		if strings.Contains(d.Name, "Webcam C920") && !strings.Contains(d.Name, "Microphone") {
			t.Fatalf("video device leaked into the list: %q", d.Name)
		}
	}
}

// The friendly name is shown to the user but the alternative name is what gets
// passed to ffmpeg, because two identical sound cards produce two identical
// friendly names.
func TestParseDshowPrefersAlternativeNameAsID(t *testing.T) {
	devices := parseListing(t, modernListing)
	cable := devices[1]

	if cable.Name != "CABLE Output (VB-Audio Virtual Cable)" {
		t.Fatalf("Name = %q", cable.Name)
	}
	if !strings.HasPrefix(cable.ID, "@device_cm_") {
		t.Fatalf("ID = %q, want the alternative device path", cable.ID)
	}
	if cable.ID == cable.Name {
		t.Fatal("ID fell back to the friendly name despite an alternative name being present")
	}
}

func TestParseDshowFlagsLoopbackDevices(t *testing.T) {
	devices := parseListing(t, modernListing)
	if devices[0].IsLoopback {
		t.Fatalf("%q was flagged as loopback", devices[0].Name)
	}
	if !devices[1].IsLoopback {
		t.Fatalf("%q was not flagged as loopback", devices[1].Name)
	}
}

func TestParseDshowHandlesLegacySectionHeaders(t *testing.T) {
	devices := parseListing(t, legacyListing)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(devices), devices)
	}
	if devices[0].Name != "Microphone (Realtek Audio)" {
		t.Fatalf("first device = %q", devices[0].Name)
	}
	if !devices[1].IsLoopback {
		t.Fatalf("%q was not flagged as loopback", devices[1].Name)
	}
	if devices[1].ID != `@device_cm_{33D9A762}\wave_{BBBB}` {
		t.Fatalf("ID = %q", devices[1].ID)
	}
}

// A device with no alternative name still has to be usable.
func TestParseDshowFallsBackToFriendlyName(t *testing.T) {
	devices := parseListing(t, `[dshow @ 1] "Line In (Sound Card)" (audio)`)
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}
	if devices[0].ID != "Line In (Sound Card)" {
		t.Fatalf("ID = %q, want the friendly name as a fallback", devices[0].ID)
	}
}

func TestParseDshowIgnoresUnrelatedOutput(t *testing.T) {
	if devices := parseListing(t, "some other ffmpeg chatter\nand more"); len(devices) != 0 {
		t.Fatalf("got %+v, want nothing", devices)
	}
}

func TestLooksLikeLoopbackIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{
		"CABLE Output (VB-Audio Virtual Cable)",
		"voicemeeter aux output",
		"Stereo Mix (Realtek)",
	} {
		if !looksLikeLoopback(name) {
			t.Errorf("%q was not recognised as a loopback device", name)
		}
	}
	for _, name := range []string{"Microphone (Realtek Audio)", "Line In"} {
		if looksLikeLoopback(name) {
			t.Errorf("%q was wrongly recognised as a loopback device", name)
		}
	}
}
