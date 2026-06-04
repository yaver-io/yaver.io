package ghost

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
)

// Locator turns a screenshot plus a natural-language instruction into a single
// Action. The implementation is injected by the agent and wired to Yaver's
// first-class AI runners (Claude Code / Codex / OpenRouter / local Ollama/vLLM),
// so this package stays free of any LLM/provider dependency and a customer can
// run the grounding model entirely on-prem.
type Locator interface {
	// Locate is given a PNG-encoded screenshot and an instruction such as
	// "click the Save button" and returns the Action to perform. Returning an
	// Action with Kind == ActionNone means "nothing to do".
	Locate(ctx context.Context, screenshotPNG []byte, instruction string) (Action, error)
}

// LocatorFunc adapts a plain function to the Locator interface (handy for tests
// and for the agent's thin wrapper around its model call).
type LocatorFunc func(ctx context.Context, screenshotPNG []byte, instruction string) (Action, error)

// Locate implements Locator.
func (f LocatorFunc) Locate(ctx context.Context, screenshotPNG []byte, instruction string) (Action, error) {
	return f(ctx, screenshotPNG, instruction)
}

// Execute performs a single Action against the engine's Input. It is the one
// place that maps the Action vocabulary onto Input calls, so both the vision
// path (Act) and direct callers (the ops verbs) go through identical actuation.
func (e *Engine) Execute(a Action) error {
	if e == nil || e.Input == nil {
		return ErrUnsupported
	}
	btn := a.Button
	if btn == "" {
		btn = ButtonLeft
	}
	switch a.Kind {
	case ActionNone:
		return nil
	case ActionMove:
		return e.Input.Move(a.X, a.Y)
	case ActionClick:
		return e.Input.Click(btn, a.X, a.Y)
	case ActionDoubleClick:
		return e.Input.DoubleClick(btn, a.X, a.Y)
	case ActionDrag:
		return e.Input.Drag(btn, a.X, a.Y, a.ToX, a.ToY)
	case ActionScroll:
		return e.Input.Scroll(a.DX, a.DY)
	case ActionType:
		return e.Input.TypeText(a.Text)
	case ActionKey:
		return e.Input.KeyCombo(a.Keys...)
	default:
		return fmt.Errorf("ghost: unknown action kind %q", a.Kind)
	}
}

// CapturePNG grabs the given display and PNG-encodes it. Shared by Act and the
// ghost_screenshot verb.
func (e *Engine) CapturePNG(display int) ([]byte, image.Image, error) {
	if e == nil || e.Screen == nil {
		return nil, nil, ErrUnsupported
	}
	img, err := e.Screen.Capture(display)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil, fmt.Errorf("ghost: encode screenshot: %w", err)
	}
	return buf.Bytes(), img, nil
}

// Act is the single-step vision loop: capture the display, ask the Locator what
// to do, then execute it. The multi-step plan→verify→retry loop is owned by the
// caller (Talos), which can read the ERP's SQL back to confirm each write. This
// keeps the heavy perception+actuation in Yaver and the business logic in Talos.
func (e *Engine) Act(ctx context.Context, loc Locator, instruction string, display int) (Action, error) {
	if loc == nil {
		return Action{}, fmt.Errorf("ghost: no vision locator configured")
	}
	pngBytes, _, err := e.CapturePNG(display)
	if err != nil {
		return Action{}, err
	}
	act, err := loc.Locate(ctx, pngBytes, instruction)
	if err != nil {
		return Action{}, err
	}
	if err := e.Execute(act); err != nil {
		return act, err
	}
	return act, nil
}
