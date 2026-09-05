# Discord Audio Streamer

A small Windows desktop app for pushing audio into a Discord voice channel through your own
bot. Two modes:

- **Player** — pick an audio file (mp3, wav, flac, ogg, m4a, …) and play it into voice with
  play/pause/stop, seek and a volume slider.
- **Streamer** — capture your desktop audio through a virtual audio cable and stream it live,
  also with a volume slider.

The app ships without a bot token. On first run it walks you through creating a Discord
application, copying the bot token, and inviting the bot to your server with the right voice
permissions. The token is stored encrypted on disk and can be changed later from Settings.

## How the audio path works

ffmpeg is used only to *decode or capture* — never to encode. It hands us raw PCM
(`s16le`, 48 kHz, stereo), we scale the amplitude ourselves (that is what makes the volume
slider work in real time), encode to Opus in-process, and pace frames onto the wire against a
monotonic clock so timing cannot drift.

```
ffmpeg ──► PCM frames (20 ms / 3840 bytes) ──► gain ──► ring buffer ──► Opus ──► Discord
```

A ring buffer absorbs the mismatch between whatever rate the OS hands us capture data and the
strict 20 ms cadence Opus expects: it drops the oldest frame on overflow and emits a silence
frame on underrun.

## Requirements for desktop streaming

ffmpeg has no WASAPI loopback input, so capturing "what my speakers are playing" needs a
virtual audio device. Either works:

- [VB-Audio Virtual Cable](https://vb-audio.com/Cable/) — route the apps you want to share to
  `CABLE Input`, then pick `CABLE Output` in this app.
- [VoiceMeeter](https://vb-audio.com/Voicemeeter/) — more flexible; lets you keep hearing the
  audio yourself while sharing it.

## Development

See [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

## License

MIT
