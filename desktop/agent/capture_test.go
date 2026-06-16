package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"
)

func TestCaptureClampFps(t *testing.T) {
	cases := map[int]int{0: 6, -5: 6, 6: 6, 20: 15, 10: 10}
	for in, want := range cases {
		if got := captureClampFps(in); got != want {
			t.Errorf("captureClampFps(%d)=%d want %d", in, got, want)
		}
	}
}

func TestFfmpegInputArgs(t *testing.T) {
	args := ffmpegInputArgs("/dev/video0", 6, 1280, 720)
	joined := ""
	for _, a := range args {
		joined += a + " "
	}
	// At minimum the device must be present as the -i input.
	found := false
	for i, a := range args {
		if a == "-i" && i+1 < len(args) && args[i+1] == "/dev/video0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -i /dev/video0 in args: %v", args)
	}
}

// TestCaptureReadLoopSplitsJPEGs feeds two concatenated JPEG frames through the
// MJPEG splitter and confirms the latest-frame buffer ends up holding a valid,
// decodable JPEG — no ffmpeg, no capture card.
func TestCaptureReadLoopSplitsJPEGs(t *testing.T) {
	mkJPEG := func(c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, c)
			}
		}
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
		return buf.Bytes()
	}
	f1 := mkJPEG(color.RGBA{200, 50, 50, 255})
	f2 := mkJPEG(color.RGBA{50, 200, 50, 255})
	stream := append(append([]byte{}, f1...), f2...)

	g := &captureStreamer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.readLoop(ctx, bytes.NewReader(stream)); close(done) }()

	// Wait for the reader to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(g.frame()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	last := g.frame()
	if len(last) == 0 {
		t.Fatal("no frame captured from the MJPEG splitter")
	}
	if _, err := jpeg.Decode(bytes.NewReader(last)); err != nil {
		t.Fatalf("latest frame is not a valid JPEG: %v", err)
	}
}

func TestCaptureStatusShape(t *testing.T) {
	g := &captureStreamer{}
	st := g.status()
	for _, k := range []string{"running", "device", "fps", "streamUrl", "frameUrl", "ffmpeg"} {
		if _, ok := st[k]; !ok {
			t.Errorf("status missing key %q", k)
		}
	}
}
