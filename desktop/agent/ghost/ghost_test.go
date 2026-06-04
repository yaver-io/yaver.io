package ghost

import (
	"context"
	"encoding/json"
	"image"
	"testing"
)

// fakeInput records the calls Execute makes so we can assert the Action->Input
// mapping without a real desktop.
type fakeInput struct {
	calls []string
}

func (f *fakeInput) Move(x, y int) error            { f.log("move", x, y); return nil }
func (f *fakeInput) Click(b Button, x, y int) error { f.log("click:"+string(b), x, y); return nil }
func (f *fakeInput) DoubleClick(b Button, x, y int) error {
	f.log("double:"+string(b), x, y)
	return nil
}
func (f *fakeInput) Drag(b Button, x1, y1, x2, y2 int) error {
	f.calls = append(f.calls, "drag")
	return nil
}
func (f *fakeInput) Scroll(dx, dy int) error       { f.calls = append(f.calls, "scroll"); return nil }
func (f *fakeInput) TypeText(s string) error       { f.calls = append(f.calls, "type:"+s); return nil }
func (f *fakeInput) KeyCombo(keys ...string) error { f.calls = append(f.calls, "key"); return nil }

func (f *fakeInput) log(op string, x, y int) { f.calls = append(f.calls, op) }

type fakeScreen struct{}

func (fakeScreen) Displays() ([]Display, error) {
	return []Display{{Index: 0, Width: 100, Height: 100, Primary: true}}, nil
}
func (fakeScreen) Capture(display int) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
}

func TestExecuteDispatch(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		want   string
	}{
		{"click default button", Action{Kind: ActionClick, X: 5, Y: 6}, "click:left"},
		{"right click", Action{Kind: ActionClick, Button: ButtonRight}, "click:right"},
		{"double", Action{Kind: ActionDoubleClick}, "double:left"},
		{"type", Action{Kind: ActionType, Text: "hi"}, "type:hi"},
		{"key", Action{Kind: ActionKey, Keys: []string{"ctrl", "s"}}, "key"},
		{"scroll", Action{Kind: ActionScroll, DY: 3}, "scroll"},
		{"move", Action{Kind: ActionMove, X: 1, Y: 2}, "move"},
		{"drag", Action{Kind: ActionDrag}, "drag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fi := &fakeInput{}
			e := &Engine{Input: fi}
			if err := e.Execute(tc.action); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(fi.calls) != 1 || fi.calls[0] != tc.want {
				t.Fatalf("calls = %v, want [%s]", fi.calls, tc.want)
			}
		})
	}
}

func TestExecuteNoneIsNoop(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Input: fi}
	if err := e.Execute(Action{Kind: ActionNone}); err != nil {
		t.Fatalf("Execute none: %v", err)
	}
	if len(fi.calls) != 0 {
		t.Fatalf("ActionNone should not actuate, got %v", fi.calls)
	}
}

func TestExecuteUnknownKind(t *testing.T) {
	e := &Engine{Input: &fakeInput{}}
	if err := e.Execute(Action{Kind: "bogus"}); err == nil {
		t.Fatal("expected error for unknown action kind")
	}
}

func TestActRunsLocatorThenExecutes(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi}
	loc := LocatorFunc(func(ctx context.Context, png []byte, instruction string) (Action, error) {
		if len(png) == 0 {
			t.Fatal("locator got empty screenshot")
		}
		if instruction != "click save" {
			t.Fatalf("instruction = %q", instruction)
		}
		return Action{Kind: ActionClick, X: 10, Y: 20, Reason: "found save"}, nil
	})
	act, err := e.Act(context.Background(), loc, "click save", 0)
	if err != nil {
		t.Fatalf("Act: %v", err)
	}
	if act.Reason != "found save" {
		t.Fatalf("reason = %q", act.Reason)
	}
	if len(fi.calls) != 1 || fi.calls[0] != "click:left" {
		t.Fatalf("expected one click, got %v", fi.calls)
	}
}

func TestActRequiresLocator(t *testing.T) {
	e := &Engine{Screen: fakeScreen{}, Input: &fakeInput{}}
	if _, err := e.Act(context.Background(), nil, "x", 0); err == nil {
		t.Fatal("expected error when no locator configured")
	}
}

func TestActionJSONRoundTrip(t *testing.T) {
	in := Action{Kind: ActionType, Text: "ABC", Reason: "field"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Action
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != ActionType || out.Text != "ABC" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestTreeStubUnsupported(t *testing.T) {
	if _, err := newTree().Windows(); err != ErrUnsupported {
		t.Fatalf("stub tree should be unsupported, got %v", err)
	}
}
