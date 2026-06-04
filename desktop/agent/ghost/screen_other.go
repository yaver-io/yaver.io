//go:build !windows

package ghost

import "image"

// platformSupported is false until the macOS/Linux screen+input implementations
// land in Phase 2. The package still builds (CGO_ENABLED=0) on every OS so the
// agent compiles host-side; ghost ops simply report unavailable off-Windows.
const platformSupported = false

type unsupportedScreen struct{}

func newScreen() (Screen, error) { return unsupportedScreen{}, nil }

func (unsupportedScreen) Displays() ([]Display, error)             { return nil, ErrUnsupported }
func (unsupportedScreen) Capture(display int) (image.Image, error) { return nil, ErrUnsupported }
