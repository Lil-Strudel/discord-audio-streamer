// Command DiscordAudioStreamer plays audio files and desktop audio into a
// Discord voice channel through a bot the user supplies.
package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/logging"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Set up file logging before anything else, so a failure during startup is
	// recorded rather than lost to a GUI build's missing console.
	logger, closeLog, logErr := logging.Setup(os.Getenv("DAS_DEBUG") != "")
	defer closeLog()
	slog.SetDefault(logger)

	if logErr != nil {
		logger.Error("could not open the log file", slog.Any("err", logErr))
	}

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
		DragAndDrop: &options.DragAndDrop{
			// Dropping files onto the queue is the quickest way to fill it.
			// Wails hands the frontend real filesystem paths, which is the only
			// form of any use here: everything downstream is an ffmpeg process
			// reading a file, not a browser File object.
			EnableFileDrop: true,
		},
		Windows: &windows.Options{
			// The app is mostly dark; matching the theme avoids a white flash
			// while the webview loads.
			Theme: windows.Dark,
		},
		Bind: []any{app},
	})
	if err != nil {
		logger.Error("application exited with an error", slog.Any("err", err))
		closeLog()
		os.Exit(1)
	}

	logger.Info("exiting normally")
}
