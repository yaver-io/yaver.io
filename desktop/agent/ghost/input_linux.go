//go:build linux

package ghost

// Linux input injection via X11 XTEST (pure-Go xgb, no cgo). Mouse uses
// WarpPointer + FakeInput; keyboard uses the xdotool-style "remap a spare
// keycode to the target keysym, press, restore" trick so arbitrary Unicode
// (incl. Turkish ç/ş/ğ/ı/ö/ü) types correctly regardless of the layout.
//
// Drives the X display directly — on the RPi blackbox this is the RustDesk
// client window that mirrors the remote Logo PC. Must be validated on-device.

import (
	"fmt"
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

const (
	evKeyPress      = 2
	evKeyRelease    = 3
	evButtonPress   = 4
	evButtonRelease = 5
)

type x11Input struct {
	conn       *xgb.Conn
	root       xproto.Window
	minKeycode xproto.Keycode
	perKeycode byte
	spare      xproto.Keycode // a keycode with no symbols, used for remap-typing
}

func newInput() (Input, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("ghost: X11 connect failed (is DISPLAY set?): %w", err)
	}
	if err := xtest.Init(c); err != nil {
		return nil, fmt.Errorf("ghost: XTEST extension unavailable: %w", err)
	}
	setup := xproto.Setup(c)
	screen := setup.DefaultScreen(c)
	in := &x11Input{conn: c, root: screen.Root, minKeycode: setup.MinKeycode}
	in.findSpareKeycode(setup)
	return in, nil
}

func (in *x11Input) findSpareKeycode(setup *xproto.SetupInfo) {
	count := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	reply, err := xproto.GetKeyboardMapping(in.conn, setup.MinKeycode, byte(count)).Reply()
	if err != nil || reply == nil {
		return
	}
	in.perKeycode = reply.KeysymsPerKeycode
	per := int(reply.KeysymsPerKeycode)
	for kc := 0; kc < count; kc++ {
		allZero := true
		for j := 0; j < per; j++ {
			if reply.Keysyms[kc*per+j] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			in.spare = xproto.Keycode(int(setup.MinKeycode) + kc)
			return
		}
	}
}

func (in *x11Input) Move(x, y int) error {
	return xproto.WarpPointerChecked(in.conn, 0, in.root, 0, 0, 0, 0, int16(x), int16(y)).Check()
}

func buttonDetail(b Button) byte {
	switch b {
	case ButtonRight:
		return 3
	case ButtonMiddle:
		return 2
	default:
		return 1
	}
}

func (in *x11Input) fakeBtn(ev, detail byte) error {
	return xtest.FakeInputChecked(in.conn, ev, detail, 0, in.root, 0, 0, 0).Check()
}

func (in *x11Input) Click(b Button, x, y int) error {
	if err := in.Move(x, y); err != nil {
		return err
	}
	d := buttonDetail(b)
	if err := in.fakeBtn(evButtonPress, d); err != nil {
		return err
	}
	return in.fakeBtn(evButtonRelease, d)
}

func (in *x11Input) DoubleClick(b Button, x, y int) error {
	if err := in.Click(b, x, y); err != nil {
		return err
	}
	return in.Click(b, x, y)
}

func (in *x11Input) Drag(b Button, x1, y1, x2, y2 int) error {
	if err := in.Move(x1, y1); err != nil {
		return err
	}
	d := buttonDetail(b)
	if err := in.fakeBtn(evButtonPress, d); err != nil {
		return err
	}
	if err := in.Move(x2, y2); err != nil {
		return err
	}
	return in.fakeBtn(evButtonRelease, d)
}

func (in *x11Input) Scroll(dx, dy int) error {
	step := func(detail byte, n int) error {
		for i := 0; i < n; i++ {
			if err := in.fakeBtn(evButtonPress, detail); err != nil {
				return err
			}
			if err := in.fakeBtn(evButtonRelease, detail); err != nil {
				return err
			}
		}
		return nil
	}
	if dy > 0 {
		if err := step(4, dy); err != nil { // wheel up
			return err
		}
	} else if dy < 0 {
		if err := step(5, -dy); err != nil { // wheel down
			return err
		}
	}
	if dx > 0 {
		if err := step(7, dx); err != nil {
			return err
		}
	} else if dx < 0 {
		if err := step(6, -dx); err != nil {
			return err
		}
	}
	return nil
}

func runeToKeysym(r rune) uint32 {
	if (r >= 0x20 && r <= 0x7e) || (r >= 0xa0 && r <= 0xff) {
		return uint32(r) // Latin-1 keysyms == codepoint
	}
	return 0x01000000 + uint32(r) // X Unicode keysym
}

// pressKeysym remaps the spare keycode to keysym, presses+releases it, restores.
func (in *x11Input) pressKeysym(keysym uint32) error {
	if in.spare == 0 || in.perKeycode == 0 {
		return fmt.Errorf("ghost: no spare keycode for typing")
	}
	syms := make([]xproto.Keysym, in.perKeycode)
	for i := range syms {
		syms[i] = xproto.Keysym(keysym)
	}
	if err := xproto.ChangeKeyboardMappingChecked(in.conn, 1, in.spare, in.perKeycode, syms).Check(); err != nil {
		return err
	}
	if err := xtest.FakeInputChecked(in.conn, evKeyPress, byte(in.spare), 0, in.root, 0, 0, 0).Check(); err != nil {
		return err
	}
	if err := xtest.FakeInputChecked(in.conn, evKeyRelease, byte(in.spare), 0, in.root, 0, 0, 0).Check(); err != nil {
		return err
	}
	// Restore the spare to NoSymbol.
	zero := make([]xproto.Keysym, in.perKeycode)
	_ = xproto.ChangeKeyboardMappingChecked(in.conn, 1, in.spare, in.perKeycode, zero).Check()
	return nil
}

func (in *x11Input) TypeText(s string) error {
	for _, r := range s {
		if err := in.pressKeysym(runeToKeysym(r)); err != nil {
			return err
		}
	}
	return nil
}

// x11Keysyms maps friendly key names to X keysyms for KeyCombo.
var x11Keysyms = map[string]uint32{
	"ctrl": 0xffe3, "control": 0xffe3,
	"alt": 0xffe9, "option": 0xffe9,
	"shift": 0xffe1,
	"super": 0xffeb, "win": 0xffeb, "meta": 0xffeb, "cmd": 0xffeb,
	"enter": 0xff0d, "return": 0xff0d,
	"tab": 0xff09, "esc": 0xff1b, "escape": 0xff1b,
	"space": 0x20, "backspace": 0xff08, "back": 0xff08,
	"delete": 0xffff, "del": 0xffff, "insert": 0xff63,
	"home": 0xff50, "end": 0xff57, "pageup": 0xff55, "pagedown": 0xff56,
	"left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54,
	"f1": 0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1, "f5": 0xffc2, "f6": 0xffc3,
	"f7": 0xffc4, "f8": 0xffc5, "f9": 0xffc6, "f10": 0xffc7, "f11": 0xffc8, "f12": 0xffc9,
}

func keyNameToKeysym(name string) (uint32, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) == 1 {
		return runeToKeysym(rune(n[0])), true
	}
	ks, ok := x11Keysyms[n]
	return ks, ok
}

// KeyCombo presses a chord. Each key is remapped onto the spare keycode in turn;
// for true simultaneous modifiers we press them via the spare sequentially,
// which works for the common ctrl/alt/shift + key shortcuts used in ERP UIs.
func (in *x11Input) KeyCombo(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if in.spare == 0 || in.perKeycode == 0 {
		return fmt.Errorf("ghost: no spare keycode for key combo")
	}
	// Resolve all keysyms first.
	var syms []uint32
	for _, k := range keys {
		ks, ok := keyNameToKeysym(k)
		if !ok {
			return fmt.Errorf("ghost: unknown key %q", k)
		}
		syms = append(syms, ks)
	}
	// Remap one spare keycode per chord position would need many spares; the
	// pragmatic path that covers ERP shortcuts: hold each as a fresh spare press
	// in order (down all, up reverse) by remapping the single spare between
	// down events. This is best-effort for multi-modifier chords.
	per := in.perKeycode
	remap := func(ks uint32) error {
		buf := make([]xproto.Keysym, per)
		for i := range buf {
			buf[i] = xproto.Keysym(ks)
		}
		return xproto.ChangeKeyboardMappingChecked(in.conn, 1, in.spare, per, buf).Check()
	}
	// Press in order.
	for _, ks := range syms {
		if err := remap(ks); err != nil {
			return err
		}
		if err := xtest.FakeInputChecked(in.conn, evKeyPress, byte(in.spare), 0, in.root, 0, 0, 0).Check(); err != nil {
			return err
		}
	}
	// Release in reverse (spare currently holds the last symbol; releasing the
	// spare keycode releases the physical key regardless of mapping).
	for range syms {
		if err := xtest.FakeInputChecked(in.conn, evKeyRelease, byte(in.spare), 0, in.root, 0, 0, 0).Check(); err != nil {
			return err
		}
	}
	zero := make([]xproto.Keysym, per)
	_ = xproto.ChangeKeyboardMappingChecked(in.conn, 1, in.spare, per, zero).Check()
	return nil
}
