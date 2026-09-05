# Bundled ffmpeg

`ffmpeg.gz` is fetched at build time by `scripts/fetch_ffmpeg.sh` and is
deliberately not committed: it is roughly 40 MB compressed, and a binary that
large has no business in a git history.

It is only read by builds that set the `embedffmpeg` tag. An ordinary
development build resolves ffmpeg from `PATH` instead, so this directory can
stay empty while working on the app.
