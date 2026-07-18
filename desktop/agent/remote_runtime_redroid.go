package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/yaver-io/agent/studio"
)

const remoteRuntimeRedroidTargetID = "android-redroid"

type redroidRuntimeTarget struct{}

func redroidRuntimeSurface() *studio.RedroidSurface {
	width, height, dpi := 1080, 2340, 440
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("YAVER_REDROID_WIDTH"))); err == nil && v > 0 {
		width = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("YAVER_REDROID_HEIGHT"))); err == nil && v > 0 {
		height = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("YAVER_REDROID_DPI"))); err == nil && v > 0 {
		dpi = v
	}
	name := strings.TrimSpace(os.Getenv("YAVER_REDROID_CONTAINER"))
	if name == "" {
		name = "yaver-remote-redroid"
	}
	image := strings.TrimSpace(os.Getenv("YAVER_REDROID_IMAGE"))
	if image == "" {
		image = "redroid/redroid:13.0.0-latest"
	}
	workDir := strings.TrimSpace(os.Getenv("YAVER_REDROID_HOST_WORKDIR"))
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "yaver-redroid-runtime-data")
	}
	return &studio.RedroidSurface{
		R:           studio.LocalRunner{},
		Name:        name,
		Image:       image,
		HostWorkDir: workDir,
		Width:       width,
		Height:      height,
		DPI:         dpi,
	}
}

func (redroidRuntimeTarget) Attach(ctx context.Context) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("redroid requires a Linux host with Docker and binder support")
	}
	surface := redroidRuntimeSurface()
	if err := surface.EnsureReady(ctx); err != nil {
		return "", err
	}
	return surface.Name, nil
}

func (redroidRuntimeTarget) Tap(ctx context.Context, _ string, x, y int) error {
	return redroidRuntimeSurface().Driver().Tap(ctx, x, y)
}

func (redroidRuntimeTarget) Swipe(ctx context.Context, _ string, x1, y1, x2, y2, durationMs int) error {
	if durationMs <= 0 {
		durationMs = 250
	}
	_, err := redroidRuntimeSurface().AndroidShell(ctx, fmt.Sprintf("input swipe %d %d %d %d %d", x1, y1, x2, y2, durationMs))
	return err
}

func (redroidRuntimeTarget) Text(ctx context.Context, _ string, text string) error {
	return redroidRuntimeSurface().Driver().Type(ctx, text)
}

func (redroidRuntimeTarget) Key(ctx context.Context, _ string, key string) error {
	return redroidRuntimeSurface().Driver().Key(ctx, key)
}

func (redroidRuntimeTarget) Screenshot(ctx context.Context, _ string, pngPath string) error {
	buf, err := redroidRuntimeSurface().Driver().Screenshot(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(pngPath, buf, 0o600)
}

func (redroidRuntimeTarget) Dims(context.Context, string) DeviceDims {
	surface := redroidRuntimeSurface()
	rot := "portrait"
	if surface.Width > surface.Height {
		rot = "landscape"
	}
	return DeviceDims{Width: surface.Width, Height: surface.Height, Scale: (surface.DPI + 79) / 160, Rotation: rot}
}

func (redroidRuntimeTarget) SpawnCapture(context.Context, string) (*exec.Cmd, io.ReadCloser, error) {
	return nil, nil, fmt.Errorf("android-redroid uses JPEG frame streaming; RTP H.264 capture is not wired yet")
}

func (redroidRuntimeTarget) NewNALReader(io.Reader) (nalSource, error) {
	return nil, fmt.Errorf("android-redroid uses JPEG frame streaming; RTP H.264 capture is not wired yet")
}

func (redroidRuntimeTarget) CanEncodeRTPH264() bool { return false }

func probeRedroidTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               remoteRuntimeRedroidTargetID,
		Label:            "Android redroid over WebRTC",
		Platform:         "android",
		RuntimeHostClass: "linux-redroid",
		HostOS:           runtime.GOOS,
		RequiredCLI:      "docker + redroid",
	}
	if runtime.GOOS != "linux" {
		target.Enabled = false
		target.Reason = "Requires a Linux host with Docker and Android binder support."
		return target
	}
	if _, err := exec.LookPath("docker"); err != nil {
		target.Enabled = false
		target.Reason = "docker not found. Install Docker on the redroid host."
		return target
	}
	target.Enabled = true
	return target
}

// Pinch on redroid. redroid IS Android, so it uses the same
// `input motionevent` multi-touch path as a physical device or emulator —
// the container boundary changes nothing about how touches are injected.
// Passing an empty deviceID lets the helper target the single attached
// container, matching how Tap/Swipe already behave here.
func (redroidRuntimeTarget) Pinch(ctx context.Context, deviceID string, x, y int, scale float64, durationMs int) error {
	return androidPinchViaUiautomator(ctx, deviceID, x, y, scale, durationMs)
}
