package main

// store_compositor.go — pure-Go store-asset compositing.
//
// Two things device capture can't give you:
//   1. The Play FEATURE GRAPHIC (1024×500) — a composed marketing image, not a
//      screenshot. We generate it from the app name + tagline.
//   2. CAPTION-FRAMED marketing screenshots — the device shot placed on a
//      branded background with a headline, at the EXACT store size.
//
// Dependency-light: stdlib image + golang.org/x/image (draw + basicfont). Text
// is rendered small with the bitmap face then scaled up — not pixel-perfect
// typography, but a real, on-spec asset the user can replace later. Everything
// is deterministic (color derived from the app name) → unit-tested on dims.

import (
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// brandColor derives a stable, pleasant background color from the app name so
// every asset for an app is consistent without the user picking one.
func brandColor(seed string) color.RGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	v := h.Sum32()
	// Keep it mid-tone (readable white text): clamp each channel to 40..170.
	ch := func(shift uint) uint8 { return uint8(40 + (v>>shift)%130) }
	return color.RGBA{R: ch(0), G: ch(8), B: ch(16), A: 255}
}

// drawScaledText renders text with the bitmap face into a small buffer, then
// scales it to fill `target` (centered by the caller via target rect). White.
func drawScaledText(dst *image.RGBA, text string, target image.Rectangle) {
	if text == "" || target.Empty() {
		return
	}
	tw := len(text) * 7 // Face7x13 advance
	if tw < 1 {
		tw = 1
	}
	small := image.NewRGBA(image.Rect(0, 0, tw, 13))
	d := &font.Drawer{
		Dst:  small,
		Src:  image.NewUniform(color.White),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(0, 10),
	}
	d.DrawString(text)
	draw.CatmullRom.Scale(dst, target, small, small.Bounds(), draw.Over, nil)
}

func fillRect(dst *image.RGBA, c color.Color) {
	draw.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
}

// GenerateFeatureGraphic builds the Play feature graphic (default 1024×500).
func GenerateFeatureGraphic(appName, tagline string, w, h int) *image.RGBA {
	if w <= 0 {
		w = 1024
	}
	if h <= 0 {
		h = 500
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, brandColor(appName))
	name := appName
	if name == "" {
		name = "Your App"
	}
	// Title across the upper-middle, tagline beneath it.
	titleRect := image.Rect(w/10, h/4-h/8, w-w/10, h/4+h/8)
	drawScaledText(img, name, titleRect)
	if tagline != "" {
		tagRect := image.Rect(w/8, h/2+h/16, w-w/8, h/2+h/16+h/10)
		drawScaledText(img, tagline, tagRect)
	}
	return img
}

// ComposeMarketingScreenshot places `shot` on a branded canvas of exactly w×h
// with a caption band on top — the classic App Store marketing screenshot.
func ComposeMarketingScreenshot(shot image.Image, caption, seed string, w, h int) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(canvas, brandColor(seed))
	bandH := h / 8
	if caption != "" {
		drawScaledText(canvas, caption, image.Rect(w/12, bandH/4, w-w/12, bandH*3/4))
	}
	// Scale the device shot to fit below the band, preserving aspect.
	if shot != nil {
		sb := shot.Bounds()
		availW, availH := w-w/10, h-bandH-h/20
		scale := float64(availW) / float64(sb.Dx())
		if s2 := float64(availH) / float64(sb.Dy()); s2 < scale {
			scale = s2
		}
		dw, dh := int(float64(sb.Dx())*scale), int(float64(sb.Dy())*scale)
		x0 := (w - dw) / 2
		y0 := bandH + (availH-dh)/2
		dstRect := image.Rect(x0, y0, x0+dw, y0+dh)
		draw.CatmullRom.Scale(canvas, dstRect, shot, sb, draw.Over, nil)
	}
	return canvas
}

func writePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
