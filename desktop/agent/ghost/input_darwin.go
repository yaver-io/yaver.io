//go:build darwin && cgo

package ghost

// macOS input injection via CoreGraphics CGEvent. Needs Accessibility
// permission granted to the host process. Requires cgo.

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdint.h>

static void ghost_move(double x, double y) {
    CGEventRef e = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft);
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

static void ghost_click(double x, double y, int button, int clicks) {
    CGEventType downT, upT; CGMouseButton b;
    if (button == 1) { downT = kCGEventRightMouseDown; upT = kCGEventRightMouseUp; b = kCGMouseButtonRight; }
    else if (button == 2) { downT = kCGEventOtherMouseDown; upT = kCGEventOtherMouseUp; b = kCGMouseButtonCenter; }
    else { downT = kCGEventLeftMouseDown; upT = kCGEventLeftMouseUp; b = kCGMouseButtonLeft; }
    CGPoint p = CGPointMake(x, y);
    for (int i = 1; i <= clicks; i++) {
        CGEventRef d = CGEventCreateMouseEvent(NULL, downT, p, b);
        CGEventSetIntegerValueField(d, kCGMouseEventClickState, i);
        CGEventPost(kCGHIDEventTap, d); CFRelease(d);
        CGEventRef u = CGEventCreateMouseEvent(NULL, upT, p, b);
        CGEventSetIntegerValueField(u, kCGMouseEventClickState, i);
        CGEventPost(kCGHIDEventTap, u); CFRelease(u);
    }
}

static void ghost_drag(double x1, double y1, double x2, double y2, int button) {
    CGEventType downT, dragT, upT; CGMouseButton b;
    if (button == 1) { downT = kCGEventRightMouseDown; dragT = kCGEventRightMouseDragged; upT = kCGEventRightMouseUp; b = kCGMouseButtonRight; }
    else if (button == 2) { downT = kCGEventOtherMouseDown; dragT = kCGEventOtherMouseDragged; upT = kCGEventOtherMouseUp; b = kCGMouseButtonCenter; }
    else { downT = kCGEventLeftMouseDown; dragT = kCGEventLeftMouseDragged; upT = kCGEventLeftMouseUp; b = kCGMouseButtonLeft; }
    CGEventRef d = CGEventCreateMouseEvent(NULL, downT, CGPointMake(x1, y1), b); CGEventPost(kCGHIDEventTap, d); CFRelease(d);
    CGEventRef m = CGEventCreateMouseEvent(NULL, dragT, CGPointMake(x2, y2), b); CGEventPost(kCGHIDEventTap, m); CFRelease(m);
    CGEventRef u = CGEventCreateMouseEvent(NULL, upT, CGPointMake(x2, y2), b); CGEventPost(kCGHIDEventTap, u); CFRelease(u);
}

static void ghost_scroll(int dx, int dy) {
    CGEventRef e = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, dy, dx);
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

static void ghost_type_unicode(const unsigned short *chars, int n) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventKeyboardSetUnicodeString(down, n, chars);
    CGEventPost(kCGHIDEventTap, down); CFRelease(down);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
    CGEventKeyboardSetUnicodeString(up, n, chars);
    CGEventPost(kCGHIDEventTap, up); CFRelease(up);
}

static void ghost_key(int keycode, uint64_t flags) {
    CGEventRef d = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, true);
    CGEventSetFlags(d, (CGEventFlags)flags);
    CGEventPost(kCGHIDEventTap, d); CFRelease(d);
    CGEventRef u = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, false);
    CGEventSetFlags(u, (CGEventFlags)flags);
    CGEventPost(kCGHIDEventTap, u); CFRelease(u);
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unsafe"
)

const (
	macFlagShift   = 0x00020000
	macFlagControl = 0x00040000
	macFlagOption  = 0x00080000
	macFlagCommand = 0x00100000
)

type macInput struct{}

func newInput() (Input, error) { return macInput{}, nil }

func macButton(b Button) C.int {
	switch b {
	case ButtonRight:
		return 1
	case ButtonMiddle:
		return 2
	default:
		return 0
	}
}

func (macInput) Move(x, y int) error {
	C.ghost_move(C.double(x), C.double(y))
	return nil
}

func (macInput) Click(b Button, x, y int) error {
	C.ghost_click(C.double(x), C.double(y), macButton(b), 1)
	return nil
}

func (macInput) DoubleClick(b Button, x, y int) error {
	C.ghost_click(C.double(x), C.double(y), macButton(b), 2)
	return nil
}

func (macInput) Drag(b Button, x1, y1, x2, y2 int) error {
	C.ghost_drag(C.double(x1), C.double(y1), C.double(x2), C.double(y2), macButton(b))
	return nil
}

func (macInput) Scroll(dx, dy int) error {
	C.ghost_scroll(C.int(dx), C.int(dy))
	return nil
}

func (macInput) TypeText(s string) error {
	for _, r := range s {
		u := utf16.Encode([]rune{r})
		if len(u) == 0 {
			continue
		}
		C.ghost_type_unicode((*C.ushort)(unsafe.Pointer(&u[0])), C.int(len(u)))
	}
	return nil
}

func (macInput) KeyCombo(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	var flags C.uint64_t
	keycode := -1
	for _, k := range keys {
		kn := strings.ToLower(strings.TrimSpace(k))
		switch kn {
		case "ctrl", "control":
			flags |= macFlagControl
		case "alt", "option", "opt":
			flags |= macFlagOption
		case "shift":
			flags |= macFlagShift
		case "cmd", "command", "win", "super", "meta":
			flags |= macFlagCommand
		default:
			kc, ok := macKeycode[kn]
			if !ok {
				return fmt.Errorf("ghost: unknown key %q", k)
			}
			keycode = kc
		}
	}
	if keycode < 0 {
		return fmt.Errorf("ghost: key combo needs a non-modifier key: %v", keys)
	}
	C.ghost_key(C.int(keycode), flags)
	return nil
}

// macKeycode maps friendly names to macOS virtual keycodes (kVK_*).
var macKeycode = map[string]int{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
	"b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
	"o": 31, "u": 32, "i": 34, "p": 35, "l": 37, "j": 38, "k": 40,
	"n": 45, "m": 46,
	"1": 18, "2": 19, "3": 20, "4": 21, "5": 23, "6": 22, "7": 26, "8": 28, "9": 25, "0": 29,
	"return": 36, "enter": 36, "tab": 48, "space": 49,
	"delete": 51, "backspace": 51, "back": 51, "esc": 53, "escape": 53,
	"del": 117, "forwarddelete": 117,
	"left": 123, "right": 124, "down": 125, "up": 126,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
}
