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
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// ghost_display_count returns the number of active displays.
static int ghost_display_count(void) {
    uint32_t count = 0;
    if (CGGetActiveDisplayList(0, NULL, &count) != kCGErrorSuccess) return 0;
    return (int)count;
}

// ghost_display_info fills a display's bounds in POINTS (the CGEvent / input
// coordinate space), its main-display flag, and its CGDirectDisplayID.
// Returns 0 on success, non-zero on failure or out-of-range index.
static int ghost_display_info(int idx, int *x, int *y, int *w, int *h, int *isMain, unsigned int *outID) {
    uint32_t count = 0;
    if (CGGetActiveDisplayList(0, NULL, &count) != kCGErrorSuccess) return 1;
    if (idx < 0 || (uint32_t)idx >= count) return 2;
    CGDirectDisplayID *ids = (CGDirectDisplayID *)malloc(sizeof(CGDirectDisplayID) * count);
    if (!ids) return 3;
    if (CGGetActiveDisplayList(count, ids, &count) != kCGErrorSuccess) { free(ids); return 4; }
    CGDirectDisplayID id = ids[idx];
    free(ids);
    CGRect r = CGDisplayBounds(id);
    *x = (int)r.origin.x; *y = (int)r.origin.y;
    *w = (int)r.size.width; *h = (int)r.size.height;
    *isMain = (id == CGMainDisplayID()) ? 1 : 0;
    *outID = (unsigned int)id;
    return 0;
}

// ghost_capture_display grabs ONE display into a freshly malloc'd BGRA buffer.
// Returns 0 on success; caller frees *outBuf via free().
static int ghost_capture_display(unsigned int displayID, unsigned char **outBuf, int *outW, int *outH, int *outBPR) {
    CGImageRef img = CGDisplayCreateImage((CGDirectDisplayID)displayID);
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

// Logical size of the MAIN display in points, used only as a fallback when
// enumeration is unavailable.
static void ghost_main_logical_size(int *w, int *h) {
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

// Displays enumerates every active display, in POINTS (the CGEvent / input
// coordinate space) with virtual-desktop X/Y offsets, so screenshot dims match
// click coordinates 1:1 and a multi-monitor click lands on the right screen.
func (macScreen) Displays() ([]Display, error) {
	n := int(C.ghost_display_count())
	out := make([]Display, 0, n)
	for i := 0; i < n; i++ {
		var x, y, w, h, isMain C.int
		var id C.uint
		if C.ghost_display_info(C.int(i), &x, &y, &w, &h, &isMain, &id) != 0 {
			continue
		}
		out = append(out, Display{Index: i, X: int(x), Y: int(y), Width: int(w), Height: int(h), Primary: isMain == 1})
	}
	if len(out) == 0 {
		// Enumeration failed (rare). Fall back to the main display so the ghost
		// still works, and flag it as primary.
		var w, h C.int
		C.ghost_main_logical_size(&w, &h)
		return []Display{{Index: 0, Width: int(w), Height: int(h), Primary: true}}, nil
	}
	return out, nil
}

func (macScreen) Capture(display int) (image.Image, error) {
	// Name the permission cause before attempting the capture, so the caller
	// gets an actionable message rather than a raw CoreGraphics rc.
	if err := macScreenPreflight(); err != nil {
		return nil, err
	}
	if display < 0 {
		return nil, fmt.Errorf("ghost: negative display index %d", display)
	}
	var x, y, w, h, isMain C.int
	var id C.uint
	if rc := C.ghost_display_info(C.int(display), &x, &y, &w, &h, &isMain, &id); rc != 0 {
		return nil, fmt.Errorf("ghost: display %d not found (%d active)", display, int(C.ghost_display_count()))
	}
	var buf *C.uchar
	var pw, ph, bpr C.int
	if rc := C.ghost_capture_display(id, &buf, &pw, &ph, &bpr); rc != 0 {
		return nil, fmt.Errorf("ghost: CGDisplayCreateImage(display %d) failed (rc=%d); grant Screen Recording permission to the host process", display, int(rc))
	}
	defer C.free(unsafe.Pointer(buf))

	width, height, stride := int(pw), int(ph), int(bpr)
	src := unsafe.Slice((*byte)(unsafe.Pointer(buf)), stride*height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for yy := 0; yy < height; yy++ {
		for xx := 0; xx < width; xx++ {
			si := yy*stride + xx*4
			di := yy*img.Stride + xx*4
			// CoreGraphics little-endian BGRA.
			img.Pix[di+0] = src[si+2] // R
			img.Pix[di+1] = src[si+1] // G
			img.Pix[di+2] = src[si+0] // B
			img.Pix[di+3] = 255
		}
	}
	// Retina: downscale captured backing pixels to this display's logical
	// points so click coordinates (which CGEvent interprets as points) map 1:1.
	lw, lh := int(w), int(h)
	if lw > 0 && lh > 0 && (lw != width || lh != height) {
		dst := image.NewRGBA(image.Rect(0, 0, lw, lh))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)
		return dst, nil
	}
	return img, nil
}
