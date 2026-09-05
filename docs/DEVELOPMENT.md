# Development

The release target is Windows, but the whole app runs on Linux for development. The only
platform-specific piece is the desktop-capture source (DirectShow on Windows, PulseAudio on
Linux); everything else is shared.

## Prerequisites (Arch Linux)

```sh
sudo pacman -S --needed webkit2gtk-4.1 gtk3 opus pkgconf ffmpeg nodejs npm
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

`libdave` (Discord's E2EE library) is linked dynamically and is not in any distro repo. Install
Discord's prebuilt binary with the helper script vendored from godave:

```sh
./scripts/libdave_install.sh v1.1.0
export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig:$PKG_CONFIG_PATH"
export LD_LIBRARY_PATH="$HOME/.local/lib:$LD_LIBRARY_PATH"
```

Verify both C dependencies resolve before building:

```sh
pkg-config --modversion opus dave
```

## Running

```sh
wails dev
```

## Tests

The audio pipeline (framing, gain, ring buffer, pacing) has no dependency on Discord or ffmpeg
and is covered by plain unit tests:

```sh
go test ./internal/...
```

## Building the Windows release

Don't build it here — cross-compiling Wails plus two CGO libraries from Linux is not worth the
trouble. Push a tag and let `.github/workflows/build.yml` produce the zip on a `windows-latest`
runner, which is the same recipe godave's own CI uses.
