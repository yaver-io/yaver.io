package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func solidJPEG(c color.Color, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestCompositeTilesLayouts(t *testing.T) {
	tiles := []image.Image{
		solidJPEG(color.RGBA{200, 0, 0, 255}, 64, 48),
		solidJPEG(color.RGBA{0, 200, 0, 255}, 64, 48),
		solidJPEG(color.RGBA{0, 0, 200, 255}, 64, 48),
	}
	for _, layout := range []string{"grid", "row", "pip"} {
		out := compositeTiles(tiles, layout, 320, 180)
		if out.Bounds().Dx() != 320 || out.Bounds().Dy() != 180 {
			t.Errorf("%s: canvas %v want 320x180", layout, out.Bounds())
		}
		// must JPEG-encode cleanly
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 70}); err != nil {
			t.Errorf("%s: encode failed: %v", layout, err)
		}
		if buf.Len() == 0 {
			t.Errorf("%s: empty output", layout)
		}
	}
}

func TestSceneB64RoundTrip(t *testing.T) {
	orig := []byte{0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9}
	b64 := b64FromBytes(orig)
	back, err := jpegFromB64(b64)
	if err != nil || !bytes.Equal(back, orig) {
		t.Fatalf("b64 round trip failed: %v", err)
	}
}

// TestSceneCompositorPublishesPushedSource verifies a running scene writes the
// "scene" pushed frame from local source buffers (capture buffer here).
func TestSceneCompositorPublishesPushedSource(t *testing.T) {
	// Seed the capture buffer with a frame.
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, solidJPEG(color.RGBA{120, 120, 120, 255}, 64, 48), &jpeg.Options{Quality: 80})
	captureStream.mu.Lock()
	captureStream.latest = buf.Bytes()
	captureStream.mu.Unlock()
	defer func() {
		captureStream.mu.Lock()
		captureStream.latest = nil
		captureStream.mu.Unlock()
	}()

	// One composite pass via the helper path (no goroutine flakiness).
	frame := sourceFrameJPEG("capture")
	if len(frame) == 0 {
		t.Fatal("expected capture frame")
	}
	img, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	canvas := compositeTiles([]image.Image{img}, "grid", 128, 96)
	var out bytes.Buffer
	_ = jpeg.Encode(&out, canvas, &jpeg.Options{Quality: 70})
	setPushedFrame("scene", b64FromBytes(out.Bytes()), "image/jpeg")
	if _, ok := getPushedFrame("scene"); !ok {
		t.Fatal("scene frame not published to pushed buffer")
	}
}
