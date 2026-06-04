//go:build windows

package ghost

// Windows screen capture via GDI BitBlt + GetDIBits — pure golang.org/x/sys
// syscalls, no cgo. Phase 1 captures the primary display; multi-monitor
// enumeration is Phase 2.
//
// Must be validated on a real Windows session.

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modGDI32 = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC               = modUser32.NewProc("GetDC")
	procReleaseDC           = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics    = modUser32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC  = modGDI32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBmp = modGDI32.NewProc("CreateCompatibleBitmap")
	procSelectObject        = modGDI32.NewProc("SelectObject")
	procBitBlt              = modGDI32.NewProc("BitBlt")
	procGetDIBits           = modGDI32.NewProc("GetDIBits")
	procDeleteObject        = modGDI32.NewProc("DeleteObject")
	procDeleteDC            = modGDI32.NewProc("DeleteDC")
)

const (
	smCXScreen = 0
	smCYScreen = 1

	srcCopy      = 0x00CC0020
	biRGB        = 0
	dibRGBColors = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func getSystemMetric(idx int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(idx))
	return int(int32(r))
}

type winScreen struct{}

func newScreen() (Screen, error) { return winScreen{}, nil }

func (winScreen) Displays() ([]Display, error) {
	w := getSystemMetric(smCXScreen)
	h := getSystemMetric(smCYScreen)
	return []Display{{Index: 0, X: 0, Y: 0, Width: w, Height: h, Primary: true}}, nil
}

func (winScreen) Capture(display int) (image.Image, error) {
	if display != 0 {
		return nil, fmt.Errorf("ghost: only the primary display (0) is captured in phase 1: %w", ErrUnsupported)
	}
	w := getSystemMetric(smCXScreen)
	h := getSystemMetric(smCYScreen)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("ghost: bad screen metrics %dx%d", w, h)
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, fmt.Errorf("ghost: GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("ghost: CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBmp.Call(screenDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("ghost: CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	ret, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), screenDC, 0, 0, srcCopy)
	if ret == 0 {
		return nil, fmt.Errorf("ghost: BitBlt failed")
	}

	// Top-down 32bpp DIB: negative height makes row 0 the top of the image.
	bi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(h),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	buf := make([]byte, w*h*4)
	ret, _, _ = procGetDIBits.Call(
		memDC,
		bmp,
		0,
		uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
	)
	if ret == 0 {
		return nil, fmt.Errorf("ghost: GetDIBits failed")
	}

	// GDI gives BGRA; convert to RGBA.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i+0] = buf[i+2] // R
		img.Pix[i+1] = buf[i+1] // G
		img.Pix[i+2] = buf[i+0] // B
		img.Pix[i+3] = 255      // A (GDI alpha is unreliable)
	}
	return img, nil
}
