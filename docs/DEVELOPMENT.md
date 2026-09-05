# Development

The release target is Windows, but the whole app runs on Linux for development.
The only platform-specific pieces are desktop capture (DirectShow on Windows,
PulseAudio here) and token encryption (DPAPI on Windows, a plain owner-only file
here); everything else is shared.

## Prerequisites

On Arch:

```sh
sudo pacman -S --needed webkit2gtk-4.1 gtk3 opus pkgconf ffmpeg nodejs npm
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
```

`libdave`, Discord's end-to-end encryption library, is linked dynamically and is
not in any distribution's repositories. Install Discord's prebuilt binary using
the script vendored from godave:

```sh
NON_INTERACTIVE=1 ./scripts/libdave_install.sh v1.1.0
```

That installs into `~/.local` and writes a pkg-config file. Add this to your
shell profile:

```sh
export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig:$PKG_CONFIG_PATH"
```

`LD_LIBRARY_PATH` is not needed: the generated `dave.pc` sets an rpath.

Check both C dependencies resolve before building anything:

```sh
pkg-config --modversion opus dave
```

## Build tags

Two matter, and forgetting either produces a confusing result rather than an
error:

- **`webkit2_41`** — required on Linux. Arch has dropped webkit2gtk 4.0, which
  Wails still defaults to.
- **`embedffmpeg`** — bundles ffmpeg into the executable. Release builds only;
  without it ffmpeg is taken from `PATH`, which is what you want in development.

Note that a plain `go build` succeeds without compiling Wails' desktop backend
at all, because that lives behind Wails' own `desktop` tag. It proves the code
compiles; it does not produce a working window. Use `wails` for that.

## Running

```sh
wails dev -tags webkit2_41
```

To check a production build:

```sh
wails build -tags webkit2_41 && ./build/bin/DiscordAudioStreamer
```

Set `DAS_DEBUG=1` for debug logging; a packaged GUI build has no console, so
this is the only way to see what it is doing.

## Tests

```sh
go test -race ./internal/...
```

The audio pipeline needs neither Discord nor a capture device and is covered by
plain unit tests. Above that:

- `internal/ffmpeg` drives the real ffmpeg binary, generating its own test
  fixtures with ffmpeg's signal generator rather than committing audio files.
- `internal/pipeline` has integration tests that decode a real file, encode it,
  and pace it into a fake voice connection, which is the seam the unit tests
  cannot reach.
- The Windows DirectShow parser is built and tested on every platform, since it
  is the one piece that cannot be exercised in development.

Tests that need the network are skipped under `-short`.

## Bindings

`app.go` and `app_audio.go` are the whole surface exposed to the frontend. After
changing a bound method's signature, regenerate the TypeScript:

```sh
wails generate module -tags webkit2_41
```

`Telemetry` is the exception: it is only ever pushed as an event, never returned
from a bound method, so it has no generated binding and is declared by hand in
`frontend/src/lib/types.ts`. Keep the two in step.

## Building the Windows release

Don't build it here. Cross-compiling Wails plus two CGO libraries from Linux is
not worth the trouble, and Discord ships libdave for Windows as an MSVC import
library that mingw cannot be relied on to link directly.

Push a tag and let `.github/workflows/build.yml` produce the zip on a
`windows-latest` runner. That job also documents the two non-obvious parts of the
Windows toolchain: building a mingw import library from `libdave.dll` with
`gendef`, and staging everything into the MINGW64 prefix because MSYS2's
pkg-config splits `PKG_CONFIG_PATH` on `:`, which collides with drive letters.

To produce a build without tagging, run the workflow manually from the Actions
tab; it uploads the zip as an artifact.
