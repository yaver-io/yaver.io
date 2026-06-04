//go:build darwin && cgo

package ghost

// macOS screen capture via CoreGraphics. The cgo type-juggling lives in a small
// C helper so the Go side just gets a malloc'd BGRA buffer + geometry.
//
// Requires cgo (the agent must be built with CGO_ENABLED=1 to ship the mac
// ghost; the default CGO_ENABLED=0 release falls back to the unsupported stub).
// Needs Screen Recording permission granted to the host process on first use.

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <stdlib.h>
#include <string.h>

// captureMain grabs the main display into a freshly malloc'd buffer.
// Returns 0 on success; caller frees *outBuf via free().
static int ghost_capture_main(unsigned char **outBuf, int *outW, int *outH, int *outBPR) {
    CGImageRef img = CGDisplayCreateImage(CGMainDisplayID());
    if (!img) return 1;
    int w = (int)CGImageGetWidth(img);
    int h = (int)CGImageGetHeight(img);
    int bpr = (int)CGImageGetBytesPerRow(img);
    CGDataProviderRef prov = CGImageGetDataProvider(img);
    CFDataRef data = CGDataProviderCopyData(prov);
    if (!data) { CGImageRelease(img); return 2; }
    long len = CFDataGetLength(data);
    unsigned char *buf = (unsigned char *)malloc(len);
    if (!buf) { CFRelease(data); CGImageRelease(img); return 3; }
    memcpy(buf, CFDataGetBytePtr(data), len);
    CFRelease(data);
    CGImageRelease(img);
    *outBuf = buf; *outW = w; *outH = h; *outBPR = bpr;
    return 0;
}

static void ghost_main_size(int *w, int *h) {
    CGDirectDisplayID d = CGMainDisplayID();
    *w = (int)CGDisplayPixelsWide(d);
    *h = (int)CGDisplayPixelsHigh(d);
}

// Logical size in POINTS (CGEvent / input coordinate space). On a retina
// display this is smaller than the captured pixel size by the backing scale.
static void ghost_logical_size(int *w, int *h) {
    CGRect r = CGDisplayBounds(CGMainDisplayID());
    *w = (int)r.size.width;
    *h = (int)r.size.height;
}
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	xdraw "golang.org/x/image/draw"
)

const platformSupported = true

type macScreen struct{}

func newScreen() (Screen, error) { return macScreen{}, nil }

func (macScreen) Displays() ([]Display, error) {
	// Report LOGICAL points so screenshot dims == input (CGEvent) coordinate
	// space — the ghost's click coords then map 1:1 to the screen.
	var w, h C.int
	C.ghost_logical_size(&w, &h)
	return []Display{{Index: 0, X: 0, Y: 0, Width: int(w), Height: int(h), Primary: true}}, nil
}

func (macScreen) Capture(display int) (image.Image, error) {
	if display != 0 {
		return nil, fmt.Errorf("ghost: only the primary display (0) is captured in phase 2 macOS: %w", ErrUnsupported)
	}
	var buf *C.uchar
	var w, h, bpr C.int
	rc := C.ghost_capture_main(&buf, &w, &h, &bpr)
	if rc != 0 {
		return nil, fmt.Errorf("ghost: CGDisplayCreateImage failed (rc=%d); grant Screen Recording permission to the host process", int(rc))
	}
	defer C.free(unsafe.Pointer(buf))

	width, height, stride := int(w), int(h), int(bpr)
	src := unsafe.Slice((*byte)(unsafe.Pointer(buf)), stride*height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			si := y*stride + x*4
			di := y*img.Stride + x*4
			// CoreGraphics little-endian BGRA.
			img.Pix[di+0] = src[si+2] // R
			img.Pix[di+1] = src[si+1] // G
			img.Pix[di+2] = src[si+0] // B
			img.Pix[di+3] = 255
		}
	}
	// Retina: downscale captured backing pixels to logical points so click
	// coordinates (which CGEvent interprets as points) map 1:1 to the image.
	var lw, lh C.int
	C.ghost_logical_size(&lw, &lh)
	if int(lw) > 0 && int(lh) > 0 && (int(lw) != width || int(lh) != height) {
		dst := image.NewRGBA(image.Rect(0, 0, int(lw), int(lh)))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)
		return dst, nil
	}
	return img, nil
}
