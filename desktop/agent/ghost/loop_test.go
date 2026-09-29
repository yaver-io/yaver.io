package ghost

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"
)

// scriptedLocator returns the queued actions in order, then ActionNone.
type scriptedLocator struct {
	actions []Action
	i       int
	calls   int
}

func (s *scriptedLocator) Locate(_ context.Context, _ []byte, _ string) (Action, error) {
	s.calls++
	if s.i >= len(s.actions) {
		return Action{Kind: ActionNone}, nil
	}
	a := s.actions[s.i]
	s.i++
	return a, nil
}

// errLocator always fails, to prove the loop names a grounding failure instead
// of hanging or silently stopping.
type errLocator struct{}

func (errLocator) Locate(context.Context, []byte, string) (Action, error) {
	return Action{}, errors.New("provider 401")
}

// brokenScreen fails capture, to prove a permission/capture failure stops the
// loop with a named reason.
type brokenScreen struct{}

func (brokenScreen) Displays() ([]Display, error) { return nil, errors.New("no display") }
func (brokenScreen) Capture(int) (image.Image, error) {
	return nil, errors.New("screen recording not granted")
}

func TestRunLoopCompletesOnNone(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi}
	loc := &scriptedLocator{actions: []Action{
		{Kind: ActionClick, X: 1, Y: 1},
		{Kind: ActionType, Text: "hi"},
	}}
	res, err := e.RunLoop(context.Background(), loc, "do the thing", LoopOptions{})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if !res.Completed || res.StoppedReason != "none" {
		t.Fatalf("completed=%v reason=%q", res.Completed, res.StoppedReason)
	}
	// 2 real actions + the terminating "none" iteration.
	if len(res.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(res.Steps))
	}
	if len(fi.calls) != 2 {
		t.Fatalf("input calls = %v, want 2", fi.calls)
	}
	if res.Goal != "do the thing" {
		t.Fatalf("goal = %q", res.Goal)
	}
}

func TestRunLoopStopsOnNoProgress(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi}
	loc := &scriptedLocator{actions: []Action{
		{Kind: ActionClick, X: 5, Y: 5},
		{Kind: ActionClick, X: 5, Y: 5},
		{Kind: ActionClick, X: 5, Y: 5},
		{Kind: ActionClick, X: 5, Y: 5},
	}}
	res, err := e.RunLoop(context.Background(), loc, "stuck", LoopOptions{})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if res.StoppedReason != "no_progress" {
		t.Fatalf("reason = %q, want no_progress", res.StoppedReason)
	}
	// Default NoProgressStopsAfter is 3.
	if len(res.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(res.Steps))
	}
}

func TestRunLoopStopsOnBudget(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi}
	// Distinct coordinates so stall detection does not fire first.
	loc := &scriptedLocator{actions: []Action{
		{Kind: ActionClick, X: 1, Y: 1},
		{Kind: ActionClick, X: 2, Y: 2},
		{Kind: ActionClick, X: 3, Y: 3},
		{Kind: ActionClick, X: 4, Y: 4},
		{Kind: ActionClick, X: 5, Y: 5},
	}}
	res, err := e.RunLoop(context.Background(), loc, "never done", LoopOptions{MaxSteps: 4})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if res.StoppedReason != "budget" {
		t.Fatalf("reason = %q, want budget", res.StoppedReason)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(res.Steps))
	}
}

func TestRunLoopNamesGroundingFailure(t *testing.T) {
	e := &Engine{Screen: fakeScreen{}, Input: &fakeInput{}}
	res, err := e.RunLoop(context.Background(), errLocator{}, "x", LoopOptions{})
	if err == nil {
		t.Fatal("expected error from failing locator")
	}
	if res.StoppedReason != "locate_error" {
		t.Fatalf("reason = %q", res.StoppedReason)
	}
	if len(res.Steps) != 1 || res.Steps[0].Error == "" {
		t.Fatalf("expected one step carrying the error, got %+v", res.Steps)
	}
}

func TestRunLoopNamesCaptureFailure(t *testing.T) {
	e := &Engine{Screen: brokenScreen{}, Input: &fakeInput{}}
	res, err := e.RunLoop(context.Background(), &scriptedLocator{}, "x", LoopOptions{})
	if err == nil {
		t.Fatal("expected error from failing capture")
	}
	if res.StoppedReason != "capture_error" {
		t.Fatalf("reason = %q", res.StoppedReason)
	}
}

func TestRunLoopRequiresLocator(t *testing.T) {
	e := &Engine{Screen: fakeScreen{}, Input: &fakeInput{}}
	if _, err := e.RunLoop(context.Background(), nil, "x", LoopOptions{}); err == nil {
		t.Fatal("expected error when no locator is configured")
	}
}

// TestRunLoopHonorsTotalDeadline proves the loop is wall-clock bounded: with an
// already-cancelled context it must return the deadline reason, not run steps.
func TestRunLoopHonorsTotalDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &Engine{Screen: fakeScreen{}, Input: &fakeInput{}}
	loc := &scriptedLocator{actions: []Action{{Kind: ActionClick, X: 1, Y: 1}}}
	res, err := e.RunLoop(ctx, loc, "x", LoopOptions{TotalTimeout: time.Second})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if res.StoppedReason != "deadline" || len(res.Steps) != 0 {
		t.Fatalf("reason=%q steps=%d", res.StoppedReason, len(res.Steps))
	}
}

// fakeTree is a minimal accessibility tree for TreeText tests.
type fakeTree struct{ root Node }

func (f fakeTree) Windows() ([]Node, error) { return f.root.Children, nil }
func (f fakeTree) ElementTree(string) (Node, error) {
	return f.root, nil
}
func (f fakeTree) Find(q string) (*Node, error) {
	var hit *Node
	var walk func(n Node)
	walk = func(n Node) {
		if hit != nil {
			return
		}
		if n.Name == q {
			c := n
			hit = &c
			return
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(f.root)
	return hit, nil
}

func TestTreeText(t *testing.T) {
	e := &Engine{Tree: fakeTree{root: Node{Role: "AXWindow", Name: "Untitled", Children: []Node{
		{Role: "AXButton", Name: "Save"},
		{Role: "AXTextField", Value: "hello"},
	}}}}
	txt, err := e.TreeText(context.Background())
	if err != nil {
		t.Fatalf("TreeText: %v", err)
	}
	for _, want := range []string{"AXWindow:Untitled", "AXButton:Save", "AXTextField:hello"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("TreeText missing %q in:\n%s", want, txt)
		}
	}
}

func TestTreeTextUnsupported(t *testing.T) {
	if _, err := (&Engine{}).TreeText(context.Background()); err == nil {
		t.Fatal("expected ErrUnsupported with no tree")
	}
}

func TestRunLoopVerifyCompletes(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi, Tree: fakeTree{root: Node{Role: "AXWindow", Name: "Doc"}}}
	loc := &scriptedLocator{actions: []Action{{Kind: ActionClick, X: 1, Y: 1}}}
	verified := 0
	res, err := e.RunLoop(context.Background(), loc, "save the document", LoopOptions{
		Observe: func(context.Context) (string, error) { return "AXWindow:Doc", nil },
		Verify: func(_ context.Context, goal, state string) (bool, string, error) {
			verified++
			return goal == "save the document" && state != "", "document saved", nil
		},
	})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if !res.Completed || !res.Verified || res.StoppedReason != "verified" {
		t.Fatalf("completed=%v verified=%v reason=%q", res.Completed, res.Verified, res.StoppedReason)
	}
	if verified != 1 {
		t.Fatalf("verify called %d times, want 1", verified)
	}
	if res.VerifyDetail != "document saved" {
		t.Fatalf("detail = %q", res.VerifyDetail)
	}
}

func TestRunLoopStopsOnNoChange(t *testing.T) {
	fi := &fakeInput{}
	e := &Engine{Screen: fakeScreen{}, Input: fi}
	// The model issues DIFFERENT clicks but the tree never changes: not progress.
	loc := &scriptedLocator{actions: []Action{
		{Kind: ActionClick, X: 1, Y: 1},
		{Kind: ActionClick, X: 2, Y: 2},
		{Kind: ActionClick, X: 3, Y: 3},
	}}
	res, err := e.RunLoop(context.Background(), loc, "x", LoopOptions{
		Observe: func(context.Context) (string, error) { return "AXWindow:Same", nil },
	})
	if err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if res.StoppedReason != "no_change" {
		t.Fatalf("reason = %q, want no_change", res.StoppedReason)
	}
	// Default NoChangeStopsAfter is 2.
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
}

func TestSameAction(t *testing.T) {
	a := Action{Kind: ActionKey, Keys: []string{"ctrl", "s"}}
	if !sameAction(a, Action{Kind: ActionKey, Keys: []string{"ctrl", "s"}}) {
		t.Fatal("identical key chords should match")
	}
	if sameAction(a, Action{Kind: ActionKey, Keys: []string{"ctrl", "p"}}) {
		t.Fatal("different key chords must not match")
	}
	if sameAction(Action{Kind: ActionClick, X: 1}, Action{Kind: ActionClick, X: 2}) {
		t.Fatal("different click points must not match")
	}
}
