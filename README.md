# Discord Audio Streamer

A small Windows desktop app for pushing audio into a Discord voice channel through your own
bot. Two modes:

- **Player** — queue audio files (mp3, wav, flac, ogg, m4a, …) or YouTube links and play
  them into voice with play/pause/stop, seek and a volume slider.
- **Streamer** — capture any audio device on the machine, speakers included, and stream it
  live, also with a volume slider.

The app ships without a bot token. On first run it walks you through creating a Discord
application, copying the bot token, and inviting the bot to your server with the right voice
permissions. The token is stored encrypted on disk and can be changed later from Settings.

## How the audio path works

YouTube links go through yt-dlp, which is asked only *where* the audio is, never to
download it. ffmpeg fetches and decodes that address itself, which is what keeps the
seek bar working: seeking is a range request rather than a re-download.

ffmpeg is used only to *decode* files — never to encode, and never for capture on Windows. It hands us raw PCM
(`s16le`, 48 kHz, stereo), we scale the amplitude ourselves (that is what makes the volume
slider work in real time), encode to Opus in-process, and pace frames onto the wire against a
monotonic clock so timing cannot drift.

```
ffmpeg ──► PCM frames (20 ms / 3840 bytes) ──► gain ──► ring buffer ──► Opus ──► Discord
```

A ring buffer absorbs the mismatch between whatever rate the OS hands us capture data and the
strict 20 ms cadence Opus expects: it drops the oldest frame on overflow and emits a silence
frame on underrun.

## Getting it

Grab the zip from the [latest release](../../releases/latest), unpack it, and run
`DiscordAudioStreamer.exe`. Keep the files together: `libdave.dll` has to sit
next to the exe. [`docs/USAGE.md`](docs/USAGE.md) is the guide that ships inside
the zip.

## Desktop streaming

Capture on Windows goes through Core Audio directly rather than through ffmpeg, because
ffmpeg's only audio input there is DirectShow and DirectShow cannot see playback devices at
all. Core Audio can open a render endpoint in *loopback* mode, so the app lists every output
and input on the machine and streams any of them — speakers included, with nothing extra to
install.

Virtual devices such as [VB-Audio Virtual Cable](https://vb-audio.com/Cable/) and
[VoiceMeeter](https://vb-audio.com/Voicemeeter/) still appear in the list and still work, for
anyone who already has their routing set up that way.

## Development

See [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

## License

MIT
