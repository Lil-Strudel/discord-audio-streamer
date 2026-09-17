// Package subproc holds the platform details of launching a helper process.
//
// It exists because more than one package spawns command-line tools — ffmpeg to
// decode, yt-dlp to resolve links — and every one of them has to suppress the
// console window on Windows. Getting that wrong is not subtle: the app is a GUI
// binary with no console of its own, so a single missed call flashes a black box
// on screen at every play, seek and probe.
package subproc
