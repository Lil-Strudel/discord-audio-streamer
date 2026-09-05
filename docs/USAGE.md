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

## Playing a file

Connect, pick a server and a voice channel, press **Join**, then choose a file.
Play, pause, stop, seek and volume all work as you would expect. mp3, wav, flac,
ogg, opus, m4a and most other audio formats are supported.

## Streaming your desktop audio

Windows does not let a program record what your speakers are playing. To share
desktop audio you need a **virtual audio cable**, which appears to Windows as a
recording device:

- **[VB-Audio Virtual Cable](https://vb-audio.com/Cable/)** is the simple option.
  Send the apps you want to share to `CABLE Input`, then pick `CABLE Output` in
  this app.
- **[VoiceMeeter](https://vb-audio.com/Voicemeeter/)** is more work to set up but
  lets you keep hearing the audio yourself while sharing it, and mix several
  sources.

Once one is installed, open **Stream desktop audio**, pick the device, and press
**Start streaming**. Devices that look like desktop audio are labelled for you.

Only one thing plays at a time: starting a stream stops a file, and vice versa.

## The volume slider

Volume is applied to the audio before it is sent, so it changes what listeners
hear, not what you hear. It goes above 100% for quiet source material, but loud
material will distort up there — the number turns amber to warn you.

## If something sounds wrong

Open **Buffering and stream health** under the streamer.

- **Dropped frames** mean audio arrived faster than it could be sent. A busy
  machine or a sample-rate mismatch in your virtual cable will do this.
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
