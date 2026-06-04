//go:build linux

package ghost

// Linux screen capture via X11 (pure-Go xgb, no cgo). Primary use case: an
// on-prem RPi "blackbox" that drives the customer's PC through a RustDesk client
// window — the ghost captures the X display (the RustDesk window mirrors the
// remote Logo PC) and injects input into it. Needs a running X session
// ($DISPLAY); on a headless box newScreen fails and the ghost reports
// unavailable.

import (
	"fmt"
	"image"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

const platformSupported = true

type x11Screen struct {
	conn *xgb.Conn
	root xproto.Window
	w, h int
}

func newScreen() (Screen, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("ghost: X11 connect failed (is DISPLAY set?): %w", err)
	}
	setup := xproto.Setup(c)
	screen := setup.DefaultScreen(c)
	return &x11Screen{
		conn: c,
		root: screen.Root,
		w:    int(screen.WidthInPixels),
		h:    int(screen.HeightInPixels),
	}, nil
}

func (s *x11Screen) Displays() ([]Display, error) {
	return []Display{{Index: 0, X: 0, Y: 0, Width: s.w, Height: s.h, Primary: true}}, nil
}

func (s *x11Screen) Capture(display int) (image.Image, error) {
	if display != 0 {
		return nil, fmt.Errorf("ghost: only the root display (0) is captured on X11: %w", ErrUnsupported)
	}
	reply, err := xproto.GetImage(
		s.conn,
		xproto.ImageFormatZPixmap,
		xproto.Drawable(s.root),
		0, 0, uint16(s.w), uint16(s.h),
		0xffffffff,
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("ghost: X11 GetImage failed: %w", err)
	}
	data := reply.Data // ZPixmap, typically 4 bytes/pixel BGRX for depth 24/32
	img := image.NewRGBA(image.Rect(0, 0, s.w, s.h))
	n := s.w * s.h
	for px := 0; px < n; px++ {
		si := px * 4
		if si+3 >= len(data) {
			break
		}
		di := px * 4
		img.Pix[di+0] = data[si+2] // R
		img.Pix[di+1] = data[si+1] // G
		img.Pix[di+2] = data[si+0] // B
		img.Pix[di+3] = 255
	}
	return img, nil
}
