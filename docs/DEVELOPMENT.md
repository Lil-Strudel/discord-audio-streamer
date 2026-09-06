# Development

The release target is Windows, but the whole app runs on Linux for development.
The only platform-specific pieces are desktop capture (Core Audio on Windows,
PulseAudio here), token encryption (DPAPI on Windows, a plain owner-only file
here) and stderr capture for crash logs; everything else is shared.

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
- `internal/wasapi` keeps its PCM conversion — sample formats, channel downmix
  and resampling — in a file with no Windows dependency, so the fiddliest part of
  the one package that cannot run in development is still covered by tests.

Tests that need the network are skipped under `-short`.

## Windows audio capture

Capture on Windows does not go through ffmpeg. ffmpeg's only audio input on that
platform is DirectShow, and DirectShow enumerates recording devices only — the
speakers can never appear in its listing, which is the reason the usual advice
for streaming desktop audio is to install a virtual audio cable first.

`internal/wasapi` talks to Core Audio instead. A render endpoint opened with
`AUDCLNT_STREAMFLAGS_LOOPBACK` yields the same mix Windows is sending to the
device, so every output and input on the machine is streamable with nothing
installed.

Two things about that package are worth knowing before editing it:

- **It must not depend on cgo.** No machine here has a mingw toolchain, so the
  only way to check Windows-only code is to type-check it:

  ```sh
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/wasapi/
  ```

  That works only because the package imports nothing that needs a C compiler,
  which is also why it redeclares the pipeline's sample rate and channel count
  instead of importing `internal/audio`. `TestFormatMatchesPipeline` is the guard
  on that duplication.

- **A loopback endpoint that is playing nothing delivers no packets at all**, not
  silent ones. The capture loop manufactures silence to cover those stretches;
  without it the pipeline would starve every time the music stopped, and the
  stream-health readout would fill with underruns that mean nothing.

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
tab and leave `release_tag` empty; it uploads the zip as an artifact, which
needs a GitHub login to download.

### Cutting a release

A downloadable release is only created when the workflow has a tag to attach it
to. Either push one:

```sh
git tag v0.1.0
git push origin v0.1.0
```

or run the workflow from the Actions tab with `release_tag` set to `v0.1.0`,
which tags the commit and publishes in one go. The job needs
`permissions: contents: write` for this, since the default `GITHUB_TOKEN` is
read-only.

### Runtime dependencies

The first release shipped an exe that died on startup with
`libopus-0.dll was not found`: mingw had linked libopus dynamically and nothing
copied the DLL into the zip. Two things now prevent that.

The build prefers the static libopus, deleting `libopus.dll.a` from the MINGW64
prefix so `-lopus` can only resolve to the archive. `libdave` stays dynamic —
Discord only ships it as a DLL.

The build also runs in MSYS2's **UCRT64** environment rather than MINGW64. That
is not a style preference. `libdave.dll` is built by Discord with MSVC and links
its C runtime statically, and `dave.h` asks the caller to release the byte
arrays it returns with `free()` — which the Go bindings duly do, on every DAVE
handshake. A MINGW64 build takes `free()` from the legacy `msvcrt.dll`, whose
heap is not the one those arrays were allocated from, so the release corrupts
the heap and the process dies on joining a voice channel. UCRT64 links the same
universal CRT that libdave was built against. CI asserts this by failing if the
finished executable imports `msvcrt.dll`.

On Linux the same code is fine, because a shared library there allocates from
the process's own libc heap.

`scripts/bundle_windows_deps.sh` then walks the import tables of everything in
the payload, copies in any DLL it finds in `/mingw64/bin`, and fails the build
if a dependency is neither bundled, part of Windows, nor an API set. It is the
backstop: whatever the linker decides to pull in, the zip either contains it or
the build stops. To check a payload by hand on Linux:

```sh
DAS_SYSTEM_DIR=/path/to/a/System32 ./scripts/bundle_windows_deps.sh <payload-dir>
```

## Logs

`internal/logging` writes to `<user config dir>/DiscordAudioStreamer/logs/`,
rotating the previous run to `app.previous.log` on each start. A release build
on Windows is a GUI binary with no console, so this is the only place its output
goes.

On Windows it also calls `SetStdHandle(STD_ERROR_HANDLE, ...)` to point the
process's standard error at that file. The Go runtime looks that handle up on
every write, so panics, fatal errors and the report for a fault inside one of
the C libraries all land in the log — output no amount of logging from Go could
otherwise catch, and the only evidence available for a crash on a machine you
cannot attach a debugger to.
