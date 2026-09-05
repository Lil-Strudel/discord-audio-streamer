// Command DiscordAudioStreamer plays audio files and desktop audio into a
// Discord voice channel through a bot the user supplies.
package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(),
	}))
	slog.SetDefault(logger)

	app := NewApp(logger)

	err := wails.Run(&options.App{
		Title:  "Discord Audio Streamer",
		Width:  980,
		Height: 720,
		// Below this the transport controls and the device picker start to
		// overlap; there is no reason to let the window get that small.
		MinWidth:  760,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Windows: &windows.Options{
			// The app is mostly dark; matching the theme avoids a white flash
			// while the webview loads.
			Theme: windows.Dark,
		},
		Bind: []any{app},
	})
	if err != nil {
		logger.Error("application exited with an error", slog.Any("err", err))
		os.Exit(1)
	}
}

// logLevel raises verbosity when DAS_DEBUG is set, which is the only way to get
// at logs from a GUI build that has no console attached.
func logLevel() slog.Level {
	if os.Getenv("DAS_DEBUG") != "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
