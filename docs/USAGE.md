# Discord Audio Streamer

Play audio files and your desktop audio into a Discord voice channel, through a
bot you own.

## Running it

Unzip the whole folder somewhere and run **DiscordAudioStreamer.exe**. Keep the
files together — `libdave.dll` next to the exe is what lets the app connect to
Discord voice, and the app will not start without it. There is nothing to
install.

Windows may warn that the app is from an unknown publisher, because it is not
code-signed. Choose **More info → Run anyway** if you trust where you got it.

## First run: creating your bot

The app ships without a bot, so the first thing it does is walk you through
making one. It takes about two minutes:

1. **Create an application** in the Discord developer portal and give it a name.
   That name is what people will see in the voice channel.
2. **Copy the bot token** from the Bot tab (press *Reset Token* first). Discord
   only ever shows a token once, so copy it before you leave the page.
3. **Paste it into the app.** It checks the token with Discord straight away and
   shows you the bot's name back, so you know it worked.
4. **Invite the bot** using the button the app gives you. The link asks for three
   permissions only: view channels, connect to voice, and speak.

You do not need to turn on any privileged intents.

Treat the token like a password: anyone who has it controls your bot. The app
stores it encrypted, tied to your Windows account, so another user on the same
computer cannot read it. You can change or remove it later from the ⚙ button.

## Playing files

Connect, pick a server and a voice channel, press **Join**, then fill the queue.
Play, pause, stop, seek and volume all work as you would expect. mp3, wav, flac,
ogg, opus, m4a and most other audio formats are supported.

### The queue

Four ways to add tracks:

- **Add files** picks one or several at once.
- **Add folder** takes everything under a folder, subfolders included.
- **Add link** takes a YouTube address. Paste one and press Enter.
- **Dragging** files or folders onto the window adds them too. This is for files
  on your computer; a link has to go in the **Add link** box.

Click a track's name to play it. Drag a row up or down to reorder it, or use the
× on the right to remove it. Names and durations appear a moment after a large
import: the app reads each file's tags in the background so the list shows up
immediately.

When a track finishes the next one starts by itself. The two buttons on the
right of the transport row change that:

- **🔀 Shuffle** plays in a random order. Whatever is playing keeps playing; only
  what comes after it is reordered. Turning it off restores the list order.
- **🔁 Repeat** cycles through off, repeat-all (start again from the top) and
  repeat-one 🔂 (the current track loops). Pressing next still moves on while
  repeat-one is set.

A track whose file has been moved or deleted is skipped with a message rather
than stopping the queue. The queue, the playback modes and your place in it are
remembered until the next run.

### YouTube links

Paste a link to a video and it joins the queue like any other track, with its
real title and length, and plays with the same controls including the seek bar.
Paste a link to a playlist — or to a video you were watching inside one — and
every video in it is added, up to two thousand.

Rows that came from YouTube are marked **YT** in the list.

A few things cannot be queued, and the app says so rather than adding a row that
would only fail later:

- **Live streams and premieres.** A stream has no length and never ends, so it
  cannot take its turn in a queue.
- **Private, deleted and age-restricted videos.** Inside a playlist these are
  skipped and counted; on their own the app reports what YouTube said.

Videos are fetched when they play rather than when you add them, so a long
playlist is queued in seconds. The first moments of a YouTube track take a little
longer to start than a file does, because the app has to look up where the audio
is.

If links stop working across the board, it is almost certainly because YouTube
changed something and the copy of the downloader inside the app has gone stale.
A newer release of this app fixes that.

## Streaming your desktop audio

Open **Stream desktop audio** and the device list shows everything Windows has,
in two groups:

- **Outputs** — your speakers, headphones, or anything else you play sound
  through. Pick one of these to stream exactly what you hear through it. This is
  almost always what you want, and it is what the app picks by default.
- **Inputs** — microphones, line inputs, and the receiving end of a virtual audio
  cable if you have one installed.

Pick a device and press **Start streaming**. No extra software is needed: the app
records your speakers through Windows itself.

If you already run **VoiceMeeter** or **VB-Audio Virtual Cable** and prefer to
keep your existing routing, those devices still show up and still work — a cable
is now an option rather than a requirement.

You will keep hearing the audio yourself either way; streaming an output does not
take it away from you.

Only one thing plays at a time: starting a stream stops a file, and vice versa.

## The volume slider

Volume is applied to the audio before it is sent, so it changes what listeners
hear, not what you hear. It goes above 100% for quiet source material, but loud
material will distort up there — the number turns amber to warn you.

## If something sounds wrong

Open **Buffering and stream health** under the streamer.

- **Dropped frames** mean audio arrived faster than it could be sent. A busy
  machine will do this.
- **Underruns** mean audio arrived too slowly and silence was sent instead.
  Raising the buffer helps at the cost of a little delay.
- **Clock resyncs** mean the app fell far behind schedule, usually because the
  machine was suspended or heavily loaded.

A few of any of these around a start or stop is normal.

If the bot joins but nobody hears anything for a second or two, that is the
end-to-end encryption handshake. The app holds audio back until it finishes
rather than sending it unencrypted; the status line says *securing…* while that
is happening.

## Echo and hearing yourself

How you route audio so that you can hear the channel without feeding it back
into the stream is up to your own device setup. The bot is deafened and never
listens to the channel, so it cannot create a loop by itself.

## If the app crashes or misbehaves

Every run writes a log file. Open **Settings** and choose **Open log folder**,
or go there directly:

```
%APPDATA%\DiscordAudioStreamer\logs\
```

`app.log` is the current run and `app.previous.log` is the one before it. After
a crash the useful file is usually `app.previous.log`, because reopening the app
starts a new `app.log`. Attach it to a bug report.

The log records what the app was doing, and captures the crash report itself if
the app dies outright.
