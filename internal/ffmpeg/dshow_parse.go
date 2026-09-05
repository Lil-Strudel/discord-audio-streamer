package ffmpeg

import (
	"regexp"
	"strings"
)

// This parser is built on every platform, not just Windows, so that the one
// piece of device handling that cannot be exercised on a development machine is
// still covered by tests.

// Windows has no way for ffmpeg to record what the speakers are playing:
// ffmpeg has no WASAPI input, so capture goes through DirectShow, which only
// sees real input devices. Desktop audio therefore requires a virtual audio
// cable (VB-Audio Virtual Cable, VoiceMeeter) that presents the desktop mix as
// an input. These patterns identify those devices so the UI can recommend them.
var loopbackHints = []string{
	"cable output",
	"voicemeeter",
	"vb-audio",
	"virtual cable",
	"stereo mix",
	"what u hear",
	"wave out mix",
}

var (
	// [dshow @ 0000...] "CABLE Output (VB-Audio Virtual Cable)" (audio)
	dshowNameRe = regexp.MustCompile(`^\[dshow @ [^\]]*\]\s+"(.+)"(?:\s+\((audio|video)\))?\s*$`)

	// [dshow @ 0000...]   Alternative name "@device_cm_{...}\wave_{...}"
	dshowAltRe = regexp.MustCompile(`^\[dshow @ [^\]]*\]\s+Alternative name\s+"(.+)"\s*$`)

	// [dshow @ 0000...] DirectShow audio devices
	dshowSectionRe = regexp.MustCompile(`DirectShow (audio|video) devices`)
)

// parseDshowDevices extracts audio inputs from the stderr of
// `ffmpeg -list_devices true -f dshow -i dummy`.
//
// ffmpeg prints a device's friendly name and then, on the next line, its
// alternative name, so a device is only complete once the following line has
// been seen. Older builds group devices under "DirectShow audio devices" and
// "DirectShow video devices" headers; newer ones tag each line with (audio) or
// (video). Both are handled, since which one a user's bundled ffmpeg produces
// is not something we control.
func parseDshowDevices(lines []string) []CaptureDevice {
	var (
		devices []CaptureDevice
		pending *CaptureDevice
	)
	inAudio := true

	flush := func() {
		if pending != nil {
			devices = append(devices, *pending)
			pending = nil
		}
	}

	for _, line := range lines {
		if m := dshowSectionRe.FindStringSubmatch(line); m != nil {
			flush()
			inAudio = m[1] == "audio"
			continue
		}

		if m := dshowAltRe.FindStringSubmatch(line); m != nil {
			if pending != nil {
				// Prefer the alternative name: it is a stable device path,
				// while friendly names can repeat across two identical cards
				// and can contain characters that confuse -i parsing.
				pending.ID = m[1]
			}
			continue
		}

		if m := dshowNameRe.FindStringSubmatch(line); m != nil {
			flush()

			// An explicit per-line tag always wins over the section header.
			switch m[2] {
			case "audio":
				inAudio = true
			case "video":
				inAudio = false
			}
			if !inAudio {
				continue
			}

			name := m[1]
			pending = &CaptureDevice{
				ID:         name,
				Name:       name,
				IsLoopback: looksLikeLoopback(name),
			}
		}
	}
	flush()

	return devices
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
