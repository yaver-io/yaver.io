// Package ghost is Yaver's cross-OS "UI ghost": a small library that drives a
// desktop GUI the way a human clerk would — capture the screen, locate a target
// (optionally via a vision model), then move/click/type. It is the heavy lifting
// behind the "drive a legacy ERP's own GUI instead of buying its write-API
// license" use case; callers (e.g. Talos over the Yaver mesh) supply the intent
// and the business-side verification, this package supplies the hands and eyes.
//
// Design:
//   - OS-agnostic interfaces (Screen, Input, Tree) with per-OS implementations
//     selected by build tags. Windows ships first; macOS/Linux are stubbed with
//     ErrUnsupported until Phase 2.
//   - No cgo on the Windows path (pure golang.org/x/sys/windows syscalls) so the
//     agent keeps building with CGO_ENABLED=0.
//   - Vision grounding is injected via the Locator interface (see vision.go) so
//     this package carries no LLM/provider dependency — the agent wires it to
//     Yaver's first-class AI runners (Claude Code / Codex / OpenRouter / local).
package ghost

import (
	"errors"
	"image"
)

// ErrUnsupported is returned by platform implementations that are not yet
// available for the host OS (e.g. accessibility trees, or any ghost primitive
// on a not-yet-implemented OS during Phase 1).
var ErrUnsupported = errors.New("ghost: not supported on this platform")

// Button identifies a mouse button.
type Button string

const (
	ButtonLeft   Button = "left"
	ButtonRight  Button = "right"
	ButtonMiddle Button = "middle"
)

// ActionKind enumerates the executable actions the ghost can perform. It is the
// shared vocabulary between vision grounding (what the model decided) and
// actuation (what Input does).
type ActionKind string

const (
	ActionNone        ActionKind = "none" // no-op / "task already satisfied"
	ActionMove        ActionKind = "move"
	ActionClick       ActionKind = "click"
	ActionDoubleClick ActionKind = "double_click"
	ActionDrag        ActionKind = "drag"
	ActionScroll      ActionKind = "scroll"
	ActionType        ActionKind = "type"
	ActionKey         ActionKind = "key"
)

// Action is a single executable step. Only the fields relevant to Kind are set.
// It is JSON-serializable so it can cross the mesh and be logged (metadata only —
// never the screenshot bytes, per the privacy contract).
type Action struct {
	Kind ActionKind `json:"kind"`

	// X,Y target point in screen pixels (click/move/double_click/drag start).
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`

	// ToX,ToY drag end point.
	ToX int `json:"toX,omitempty"`
	ToY int `json:"toY,omitempty"`

	// Button for click/double_click/drag. Defaults to left when empty.
	Button Button `json:"button,omitempty"`

	// Text for ActionType (typed verbatim as Unicode).
	Text string `json:"text,omitempty"`

	// Keys for ActionKey: a chord pressed together then released, e.g.
	// ["ctrl","s"] or ["alt","f4"]. Names are normalized in input.
	Keys []string `json:"keys,omitempty"`

	// DX,DY scroll deltas in wheel notches (positive DY scrolls up).
	DX int `json:"dx,omitempty"`
	DY int `json:"dy,omitempty"`

	// Reason is the model's short rationale (vision grounding). Safe to log.
	Reason string `json:"reason,omitempty"`
}

// Display describes one screen/monitor.
type Display struct {
	Index   int  `json:"index"`
	X       int  `json:"x"`
	Y       int  `json:"y"`
	Width   int  `json:"width"`
	Height  int  `json:"height"`
	Primary bool `json:"primary"`
}

// Node is an accessibility-tree element (Phase 2). Kept here so the Tree
// interface and JSON shape are stable now even though impls land later.
type Node struct {
	Role         string `json:"role,omitempty"`
	Name         string `json:"name,omitempty"`
	Value        string `json:"value,omitempty"`
	AutomationID string `json:"automationId,omitempty"`
	X            int    `json:"x,omitempty"`
	Y            int    `json:"y,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Children     []Node `json:"children,omitempty"`
}

// Screen captures pixels. Implementations must be safe for concurrent Capture
// calls from the engine's single-op serialization (one ghost op at a time).
type Screen interface {
	// Displays enumerates monitors. Index 0 is the primary unless reported
	// otherwise via Display.Primary.
	Displays() ([]Display, error)
	// Capture grabs a full-resolution image of the given display index.
	Capture(display int) (image.Image, error)
}

// Input injects mouse and keyboard events. Coordinates are absolute screen
// pixels in the virtual-desktop space.
type Input interface {
	Move(x, y int) error
	Click(b Button, x, y int) error
	DoubleClick(b Button, x, y int) error
	Drag(b Button, x1, y1, x2, y2 int) error
	Scroll(dx, dy int) error
	TypeText(s string) error
	KeyCombo(keys ...string) error
}

// Tree reads the OS accessibility tree (Phase 2). Phase 1 returns a stub that
// always errors with ErrUnsupported so callers can feature-detect.
type Tree interface {
	Windows() ([]Node, error)
	ElementTree(window string) (Node, error)
	Find(query string) (*Node, error)
}

// Engine bundles the platform primitives plus an optional vision Locator. It is
// the single object the agent's ops verbs hold.
type Engine struct {
	Screen Screen
	Input  Input
	Tree   Tree
}

// New constructs an Engine for the host OS. It fails fast if the platform has no
// screen/input implementation (Phase 1: non-Windows).
func New() (*Engine, error) {
	s, err := newScreen()
	if err != nil {
		return nil, err
	}
	in, err := newInput()
	if err != nil {
		return nil, err
	}
	return &Engine{Screen: s, Input: in, Tree: newTree()}, nil
}

// Supported reports whether this OS has a working ghost (screen+input) build.
// Used by capability advertisement without constructing a full Engine.
func Supported() bool { return platformSupported }
