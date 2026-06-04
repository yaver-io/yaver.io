//go:build windows

package ghost

// Windows input injection via the Win32 SendInput API — pure golang.org/x/sys
// syscalls, no cgo (keeps the agent's CGO_ENABLED=0 build). Coordinates are
// absolute virtual-desktop pixels.
//
// NOTE: this path must be validated on a real Windows session (UIA/SendInput
// need an unlocked interactive desktop). It cross-compiles on the dev mac; it
// is exercised on the Windows "Slave" box.

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32 = windows.NewLazySystemDLL("user32.dll")

	procSendInput          = modUser32.NewProc("SendInput")
	procSetCursorPos       = modUser32.NewProc("SetCursorPos")
	procSetProcessDPIAware = modUser32.NewProc("SetProcessDPIAware")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfHWheel     = 0x1000

	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	wheelDelta = 120
)

const platformSupported = true

// winInput mirrors the Win32 INPUT struct. On 64-bit Windows sizeof(INPUT)==40:
// DWORD type (4) + 4 bytes alignment padding + union (32, the size of
// MOUSEINPUT). The union is overlaid via unsafe so we keep a single fixed
// layout for both mouse and keyboard events.
type winInput struct {
	inputType uint32
	_         uint32
	union     [32]byte
}

type winMouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
}

type winKeybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
}

func mkMouse(flags, data uint32) winInput {
	in := winInput{inputType: inputMouse}
	mi := (*winMouseInput)(unsafe.Pointer(&in.union[0]))
	mi.dwFlags = flags
	mi.mouseData = data
	return in
}

func mkKey(vk, scan uint16, flags uint32) winInput {
	in := winInput{inputType: inputKeyboard}
	ki := (*winKeybdInput)(unsafe.Pointer(&in.union[0]))
	ki.wVk = vk
	ki.wScan = scan
	ki.dwFlags = flags
	return in
}

func sendInputs(inputs ...winInput) error {
	if len(inputs) == 0 {
		return nil
	}
	ret, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if int(ret) != len(inputs) {
		return fmt.Errorf("ghost: SendInput injected %d/%d events: %v", int(ret), len(inputs), err)
	}
	return nil
}

type winInputDev struct{}

func newInput() (Input, error) {
	// Become DPI-aware so SetCursorPos and GDI capture share one physical-pixel
	// coordinate space (vision coords from the screenshot then map 1:1).
	procSetProcessDPIAware.Call()
	return winInputDev{}, nil
}

func (winInputDev) Move(x, y int) error {
	ret, _, err := procSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y)))
	if ret == 0 {
		return fmt.Errorf("ghost: SetCursorPos(%d,%d): %v", x, y, err)
	}
	return nil
}

func mouseFlags(b Button) (down, up uint32) {
	switch b {
	case ButtonRight:
		return mouseeventfRightDown, mouseeventfRightUp
	case ButtonMiddle:
		return mouseeventfMiddleDown, mouseeventfMiddleUp
	default:
		return mouseeventfLeftDown, mouseeventfLeftUp
	}
}

func (d winInputDev) Click(b Button, x, y int) error {
	if err := d.Move(x, y); err != nil {
		return err
	}
	down, up := mouseFlags(b)
	return sendInputs(mkMouse(down, 0), mkMouse(up, 0))
}

func (d winInputDev) DoubleClick(b Button, x, y int) error {
	if err := d.Click(b, x, y); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	down, up := mouseFlags(b)
	return sendInputs(mkMouse(down, 0), mkMouse(up, 0))
}

func (d winInputDev) Drag(b Button, x1, y1, x2, y2 int) error {
	if err := d.Move(x1, y1); err != nil {
		return err
	}
	down, up := mouseFlags(b)
	if err := sendInputs(mkMouse(down, 0)); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	if err := d.Move(x2, y2); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	return sendInputs(mkMouse(up, 0))
}

func (winInputDev) Scroll(dx, dy int) error {
	var inputs []winInput
	if dy != 0 {
		inputs = append(inputs, mkMouse(mouseeventfWheel, uint32(int32(dy*wheelDelta))))
	}
	if dx != 0 {
		inputs = append(inputs, mkMouse(mouseeventfHWheel, uint32(int32(dx*wheelDelta))))
	}
	return sendInputs(inputs...)
}

func (winInputDev) TypeText(s string) error {
	var inputs []winInput
	for _, r := range s {
		inputs = append(inputs, runeToInputs(r)...)
	}
	// Chunk to stay well under SendInput's practical batch limits.
	const chunk = 64
	for i := 0; i < len(inputs); i += chunk {
		end := i + chunk
		if end > len(inputs) {
			end = len(inputs)
		}
		if err := sendInputs(inputs[i:end]...); err != nil {
			return err
		}
	}
	return nil
}

// runeToInputs encodes a rune as KEYEVENTF_UNICODE down/up events (UTF-16,
// surrogate pairs for astral code points), which is keyboard-layout independent.
func runeToInputs(r rune) []winInput {
	if r <= 0xFFFF {
		return []winInput{
			mkKey(0, uint16(r), keyeventfUnicode),
			mkKey(0, uint16(r), keyeventfUnicode|keyeventfKeyUp),
		}
	}
	r -= 0x10000
	hi := uint16(0xD800 + (r >> 10))
	lo := uint16(0xDC00 + (r & 0x3FF))
	return []winInput{
		mkKey(0, hi, keyeventfUnicode),
		mkKey(0, hi, keyeventfUnicode|keyeventfKeyUp),
		mkKey(0, lo, keyeventfUnicode),
		mkKey(0, lo, keyeventfUnicode|keyeventfKeyUp),
	}
}

func (winInputDev) KeyCombo(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	vks := make([]uint16, 0, len(keys))
	for _, k := range keys {
		vk, ok := vkForName(k)
		if !ok {
			return fmt.Errorf("ghost: unknown key %q", k)
		}
		vks = append(vks, vk)
	}
	var inputs []winInput
	for _, vk := range vks { // press in order
		inputs = append(inputs, mkKey(vk, 0, 0))
	}
	for i := len(vks) - 1; i >= 0; i-- { // release in reverse
		inputs = append(inputs, mkKey(vks[i], 0, keyeventfKeyUp))
	}
	return sendInputs(inputs...)
}

// vkForName maps a friendly key name to a Win32 virtual-key code. Letters and
// digits map directly; modifiers and common named keys are in the table.
func vkForName(name string) (uint16, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) == 1 {
		c := n[0]
		if c >= 'a' && c <= 'z' {
			return uint16('A' + (c - 'a')), true
		}
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	vk, ok := namedVK[n]
	return vk, ok
}

var namedVK = map[string]uint16{
	"ctrl": 0x11, "control": 0x11,
	"alt": 0x12, "menu": 0x12,
	"shift": 0x10,
	"win":   0x5B, "super": 0x5B, "meta": 0x5B, "cmd": 0x5B,
	"enter": 0x0D, "return": 0x0D,
	"tab": 0x09, "esc": 0x1B, "escape": 0x1B,
	"space": 0x20, "backspace": 0x08, "back": 0x08,
	"delete": 0x2E, "del": 0x2E, "insert": 0x2D, "ins": 0x2D,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75,
	"f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
}
