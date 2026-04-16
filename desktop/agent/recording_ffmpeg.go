package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// recording_ffmpeg.go — screen-capture via ffmpeg. Picks the right
// platform-specific input format at runtime:
//
//   darwin  → -f avfoundation -i "1:none"   (screen capture, no audio)
//   linux   → -f x11grab -i "$DISPLAY"
//   windows → -f gdigrab   -i desktop
//
// We keep audio off on purpose — recordings are silent match reports,
// not tutorials, so we avoid the microphone privacy prompt on macOS
// and the latency overhead of audio mux. A future flag could flip it.

type ffmpegScreenDriver struct{}

func (d *ffmpegScreenDriver) Name() string { return "ffmpeg-screen" }

func (d *ffmpegScreenDriver) Available() (bool, string) {
	_, ok := ffmpegLookup()
	if !ok {
		return false, "ffmpeg not found on PATH — install with `yaver install ffmpeg`"
	}
	// Platform-specific preconditions.
	switch runtime.GOOS {
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false, "no DISPLAY/WAYLAND_DISPLAY — headless Linux cannot x11grab"
		}
	case "darwin":
		// macOS requires Screen Recording permission. We can't test it
		// without actually starting a recorder, but we warn in the
		// availability string so yaver doctor can nudge the user.
		return true, "macOS: if first capture fails, grant Screen Recording permission to your terminal in System Settings → Privacy & Security"
	}
	return true, ""
}

func (d *ffmpegScreenDriver) Start(ctx context.Context, outPath, runID, taskID string) (driverState, error) {
	args := d.argsForPlatform(outPath)
	if args == nil {
		return driverState{}, fmt.Errorf("unsupported platform for ffmpeg screen capture: %s", runtime.GOOS)
	}
	cmd := exec.Command("ffmpeg", args...)
	// Detach from the parent process group so Ctrl-C on the agent
	// doesn't also kill the ffmpeg child prematurely — we want to
	// send it SIGINT explicitly in Stop().
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Pipe ffmpeg's stderr to /dev/null so the agent logs stay clean;
	// ffmpeg prints status lines every second on stderr.
	cmd.Stdout = nil
	cmd.Stderr = nil

	// ffmpeg reads from stdin to accept the "q" quit signal; we keep
	// a pipe so Stop can write "q\n" as a clean way to finalize.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return driverState{}, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return driverState{}, err
	}
	// Drop the read end immediately; we only ever write to the pipe
	// in Stop(). The reference is stored on the cmd so the fd stays
	// open until Stop.
	_ = stdin
	_ = ctx // ctx reserved for future use (e.g. abort on cancel)
	return driverState{Cmd: cmd, OutPath: outPath}, nil
}

func (d *ffmpegScreenDriver) Stop(state driverState) error {
	if state.Cmd == nil || state.Cmd.Process == nil {
		return fmt.Errorf("ffmpeg-screen: no process to stop")
	}
	// Send SIGINT so ffmpeg finalizes the mp4 container (MOOV atom,
	// last frames flushed). SIGKILL produces a broken file.
	_ = state.Cmd.Process.Signal(syscall.SIGINT)

	done := make(chan error, 1)
	go func() { done <- state.Cmd.Wait() }()

	select {
	case err := <-done:
		_ = err // non-zero exit is fine; ffmpeg returns 255 on SIGINT
		return nil
	case <-time.After(10 * time.Second):
		// Ffmpeg got stuck — fall back to SIGKILL.
		_ = state.Cmd.Process.Kill()
		<-done
		return fmt.Errorf("ffmpeg-screen: stop timed out, sent SIGKILL (mp4 may be truncated)")
	}
}

// argsForPlatform returns the ffmpeg argv (without the binary) for the
// current OS, or nil if unsupported. Uses sane encoding defaults:
//
//   -c:v libx264 -preset ultrafast -crf 28 -r 15
//
// Which trades size for CPU — a recording while autodev is running
// shouldn't fight the dev for cycles. 15 fps keeps videos small
// (~2MB/minute at 1080p) and snappy enough for match-report playback.
func (d *ffmpegScreenDriver) argsForPlatform(outPath string) []string {
	common := []string{
		"-y",
		"-loglevel", "error",
	}
	video := []string{
		"-r", "15",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outPath,
	}
	switch runtime.GOOS {
	case "darwin":
		return append(append(common,
			"-f", "avfoundation",
			"-framerate", "15",
			"-i", "1:none", // main display, no audio
		), video...)
	case "linux":
		display := os.Getenv("DISPLAY")
		if display == "" {
			display = ":0.0"
		}
		return append(append(common,
			"-f", "x11grab",
			"-framerate", "15",
			"-i", display,
		), video...)
	case "windows":
		return append(append(common,
			"-f", "gdigrab",
			"-framerate", "15",
			"-i", "desktop",
		), video...)
	}
	return nil
}
