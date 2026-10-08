package main

// remote_runtime_physical_kvm.go adapts the physical v0 appliance to the same
// runtimeTarget contract used by browser windows, simulators, phones, and the
// host desktop. Existing web/mobile/TV/spatial viewers therefore get the same
// session, WebRTC/JPEG, lease, chat-runner, and voice orchestration paths.

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"time"
)

type physicalPCKVMTarget struct{}

// JPEGFrame lets the generic remote-runtime JPEG transport forward the UVC
// decoder's current frame without a JPEG -> PNG file -> JPEG round trip.
func (physicalPCKVMTarget) JPEGFrame(context.Context, string) ([]byte, int, int, error) {
	captureStream.mu.Lock()
	defer captureStream.mu.Unlock()
	if len(captureStream.latest) == 0 || captureStream.lastFrameAt.IsZero() || time.Since(captureStream.lastFrameAt) > 3*time.Second {
		return nil, 0, 0, fmt.Errorf("KVM_CAPTURE_NO_FRAME: no fresh HDMI frame")
	}
	return append([]byte(nil), captureStream.latest...), captureStream.width, captureStream.height, nil
}

func probePhysicalPCKVMTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               physicalKVMTargetID,
		Label:            "Physical PC (Pi + capture + M5Stack)",
		Platform:         "physical-pc",
		RuntimeHostClass: "linux-arm64",
		HostOS:           "linux",
		Surface:          "desktop",
		DisplaySurface:   "physical-pc",
		RoleHint:         "Live HDMI view and exclusive USB keyboard/mouse control",
	}
	checks := []RemoteRuntimeCheck{}
	ffmpegOK := ffmpegPath() != ""
	checks = append(checks, RemoteRuntimeCheck{ID: "ffmpeg", Label: "Capture decoder", OK: ffmpegOK, Reason: boolReason(ffmpegOK, "ffmpeg is not installed")})
	_, captureErr := physicalKVM.selectedCaptureDevice()
	captureOK := captureErr == nil
	checks = append(checks, RemoteRuntimeCheck{ID: "uvc", Label: "HDMI capture", OK: captureOK, Reason: errString(captureErr)})
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	status, statusErr := physicalKVM.status(ctx)
	cancel()
	bridgeOK := statusErr == nil
	checks = append(checks, RemoteRuntimeCheck{ID: "m5", Label: "M5Stack input bridge", OK: bridgeOK, Reason: errString(statusErr)})
	checks = append(checks, RemoteRuntimeCheck{ID: "usb-hid", Label: "Target PC USB HID", OK: bridgeOK && status.USBReady, Reason: boolReason(bridgeOK && status.USBReady, "M5Stack is not enumerated by the target PC")})
	target.Checks = checks
	target.Enabled = ffmpegOK && captureOK && bridgeOK && status.USBReady
	if !target.Enabled {
		switch {
		case !ffmpegOK:
			target.Reason = "Install ffmpeg on the Pi, then retry."
		case !captureOK:
			target.Reason = "Connect a UVC HDMI capture card to the Pi."
		case !bridgeOK:
			target.Reason = "Pair the M5Stack input bridge with `yaver kvm pair`."
		default:
			target.Reason = "Connect the M5Stack USB HID cable directly to the target PC."
		}
	} else if !status.Armed {
		target.Reason = "View is ready. Press the M5Stack button to arm keyboard/mouse control for 60 seconds."
	}
	return target
}

func boolReason(ok bool, failure string) string {
	if ok {
		return ""
	}
	return failure
}

func (physicalPCKVMTarget) Attach(ctx context.Context) (string, error) {
	status, err := physicalKVM.status(ctx)
	if err != nil {
		return "", err
	}
	if !status.USBReady {
		return "", fmt.Errorf("KVM_USB_NOT_READY: connect the M5Stack directly to the target PC")
	}
	if !captureStream.running() {
		device, err := physicalKVM.selectedCaptureDevice()
		if err != nil {
			return "", err
		}
		if err := captureStream.start(device, 10, 1280, 720, 7); err != nil {
			return "", fmt.Errorf("KVM_CAPTURE_START_FAILED: %w", err)
		}
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if len(captureStream.frame()) > 0 {
			return status.DeviceID, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("KVM_CAPTURE_NO_FRAME: capture opened but no freshly decoded HDMI frame arrived")
}

func (physicalPCKVMTarget) Tap(ctx context.Context, _ string, x, y int) error {
	dims := captureFrameDims()
	if dims.Width <= 0 || dims.Height <= 0 {
		return fmt.Errorf("KVM_CAPTURE_NO_FRAME: cannot map input without a fresh frame")
	}
	return physicalKVM.actionCurrent(ctx, map[string]any{"kind": "tap", "x": x, "y": y, "width": dims.Width, "height": dims.Height})
}

func (physicalPCKVMTarget) Swipe(ctx context.Context, _ string, x1, y1, x2, y2, durationMS int) error {
	dims := captureFrameDims()
	if dims.Width <= 0 || dims.Height <= 0 {
		return fmt.Errorf("KVM_CAPTURE_NO_FRAME: cannot map input without a fresh frame")
	}
	return physicalKVM.actionCurrent(ctx, map[string]any{
		"kind": "drag", "x1": x1, "y1": y1, "x2": x2, "y2": y2,
		"width": dims.Width, "height": dims.Height, "durationMs": durationMS,
	})
}

func (physicalPCKVMTarget) Text(ctx context.Context, _ string, text string) error {
	if len(text) == 0 || len(text) > 256 {
		return fmt.Errorf("KVM_TEXT_BOUNDS: text input must be 1-256 bytes")
	}
	for _, value := range []byte(text) {
		if value > 0x7e || (value < 0x20 && value != '\n' && value != '\t') {
			return fmt.Errorf("KVM_TEXT_LAYOUT_UNSUPPORTED: v0 USB text supports US-layout printable ASCII, tab, and newline; use the target PC's paste/input method for Unicode")
		}
	}
	return physicalKVM.actionCurrent(ctx, map[string]any{"kind": "text", "text": text})
}

func (physicalPCKVMTarget) Key(ctx context.Context, _ string, key string) error {
	return physicalKVM.actionCurrent(ctx, map[string]any{"kind": "key", "key": key})
}

func (physicalPCKVMTarget) Pinch(context.Context, string, int, int, float64, int) error {
	return fmt.Errorf("%w: a physical PC exposes a mouse, not a multi-touch digitizer", errPinchUnsupported)
}

func (physicalPCKVMTarget) Navigate(context.Context, string, string) error {
	return fmt.Errorf("%w: use the runner to focus the address bar and type on the physical PC", errNavigateUnsupported)
}

func (physicalPCKVMTarget) Screenshot(_ context.Context, _ string, pngPath string) error {
	frame, _, _, err := (physicalPCKVMTarget{}).JPEGFrame(context.Background(), "")
	if err != nil {
		return err
	}
	image, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("decode capture JPEG: %w", err)
	}
	f, err := os.Create(pngPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, image)
}

func captureFrameDims() DeviceDims {
	captureStream.mu.Lock()
	defer captureStream.mu.Unlock()
	if captureStream.width <= 0 || captureStream.height <= 0 || captureStream.lastFrameAt.IsZero() || time.Since(captureStream.lastFrameAt) > 3*time.Second {
		return DeviceDims{}
	}
	return DeviceDims{Width: captureStream.width, Height: captureStream.height, Scale: 1, Rotation: "landscape"}
}

func (physicalPCKVMTarget) Dims(context.Context, string) DeviceDims { return captureFrameDims() }
func (physicalPCKVMTarget) SpawnCapture(context.Context, string) (*exec.Cmd, io.ReadCloser, error) {
	return nil, nil, fmt.Errorf("physical PC uses the shared capture-card JPEG fan-out")
}
func (physicalPCKVMTarget) NewNALReader(io.Reader) (nalSource, error) {
	return nil, fmt.Errorf("physical PC capture is JPEG in v0.1")
}
func (physicalPCKVMTarget) CanEncodeRTPH264() bool { return false }
