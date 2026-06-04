//go:build !windows && !linux && (!darwin || !cgo)

package ghost

type unsupportedInput struct{}

func newInput() (Input, error) { return unsupportedInput{}, nil }

func (unsupportedInput) Move(x, y int) error                     { return ErrUnsupported }
func (unsupportedInput) Click(b Button, x, y int) error          { return ErrUnsupported }
func (unsupportedInput) DoubleClick(b Button, x, y int) error    { return ErrUnsupported }
func (unsupportedInput) Drag(b Button, x1, y1, x2, y2 int) error { return ErrUnsupported }
func (unsupportedInput) Scroll(dx, dy int) error                 { return ErrUnsupported }
func (unsupportedInput) TypeText(s string) error                 { return ErrUnsupported }
func (unsupportedInput) KeyCombo(keys ...string) error           { return ErrUnsupported }
