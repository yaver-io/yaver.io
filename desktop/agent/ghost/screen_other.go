//go:build !windows && !linux && (!darwin || !cgo)

package ghost

import "image"

// Fallback stub for platforms without a native ghost implementation: macOS built
// without cgo, and any OS other than Windows/Linux. Windows and Linux always
// have native impls; macOS has one when CGO is enabled. The package still builds
// (CGO_ENABLED=0) everywhere so the agent compiles host-side; ghost ops simply
// report unavailable here.
const platformSupported = false

type unsupportedScreen struct{}

func newScreen() (Screen, error) { return unsupportedScreen{}, nil }

func (unsupportedScreen) Displays() ([]Display, error)             { return nil, ErrUnsupported }
func (unsupportedScreen) Capture(display int) (image.Image, error) { return nil, ErrUnsupported }
