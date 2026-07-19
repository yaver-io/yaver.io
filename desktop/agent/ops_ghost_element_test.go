package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yaver-io/agent/ghost"
)

// sampleTree mimics a small app: a window with a toolbar holding two buttons
// whose names both contain "save", plus a text field and a zero-area node.
func sampleTree() ghost.Node {
	return ghost.Node{
		Role: "AXWindow", Name: "Drawing1.dwg", X: 0, Y: 0, Width: 1440, Height: 900,
		Children: []ghost.Node{
			{
				Role: "AXToolbar", Name: "Standard", X: 0, Y: 0, Width: 1440, Height: 40,
				Children: []ghost.Node{
					{Role: "AXButton", Name: "Save", X: 100, Y: 10, Width: 20, Height: 20},
					{Role: "AXButton", Name: "Save As…", X: 140, Y: 10, Width: 20, Height: 20},
					// Zero area: present in the tree but has no clickable point.
					{Role: "AXButton", Name: "Save Hidden", X: 0, Y: 0, Width: 0, Height: 0},
				},
			},
			{Role: "AXTextField", Name: "Command", Value: "LINE", X: 0, Y: 860, Width: 1440, Height: 40},
			{Role: "AXButton", Name: "Cancel", AutomationID: "btnCancel", X: 1300, Y: 10, Width: 60, Height: 24},
		},
	}
}

type stubTree struct {
	root ghost.Node
	err  error
}

func (s stubTree) Windows() ([]ghost.Node, error) { return s.root.Children, s.err }
func (s stubTree) ElementTree(string) (ghost.Node, error) {
	return s.root, s.err
}
func (s stubTree) Find(string) (*ghost.Node, error) { return nil, nil }

func TestCollectGhostElementsSubstring(t *testing.T) {
	got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "save"})
	// "Save" and "Save As…" match; "Save Hidden" is zero-area and must be dropped.
	if len(got) != 2 {
		t.Fatalf("want 2 matches (zero-area dropped), got %d: %+v", len(got), got)
	}
	if got[0].Name != "Save" || got[1].Name != "Save As…" {
		t.Errorf("unexpected matches: %q, %q", got[0].Name, got[1].Name)
	}
}

// The zero-area guard matters: its center would be (0,0), which on macOS is the
// Apple menu. Silently clicking there is far worse than reporting no match.
func TestCollectGhostElementsSkipsZeroArea(t *testing.T) {
	for _, m := range collectGhostElements(sampleTree(), ghostElementQuery{Query: "save"}) {
		if m.Name == "Save Hidden" {
			t.Fatal("zero-area element must not be offered as a click target")
		}
	}
}

func TestCollectGhostElementsCenter(t *testing.T) {
	got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "Cancel"})
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d", len(got))
	}
	if got[0].CenterX != 1330 || got[0].CenterY != 22 {
		t.Errorf("center = (%d,%d), want (1330,22)", got[0].CenterX, got[0].CenterY)
	}
}

// Each OS's own Find checks a different field set. The unified matcher must hit
// all three: Name (darwin/linux/windows), Value (darwin), AutomationID (windows).
func TestCollectGhostElementsMatchesValueAndAutomationID(t *testing.T) {
	if got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "LINE"}); len(got) != 1 {
		t.Errorf("Value match failed: got %d", len(got))
	}
	if got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "btnCancel"}); len(got) != 1 {
		t.Errorf("AutomationID match failed: got %d", len(got))
	}
}

func TestCollectGhostElementsExact(t *testing.T) {
	got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "Save", Exact: true})
	if len(got) != 1 || got[0].Name != "Save" {
		t.Fatalf("exact match should return only \"Save\", got %+v", got)
	}
}

func TestCollectGhostElementsRoleFilter(t *testing.T) {
	got := collectGhostElements(sampleTree(), ghostElementQuery{Role: "AXButton"})
	// Save, Save As…, Cancel — "Save Hidden" dropped for zero area.
	if len(got) != 3 {
		t.Fatalf("want 3 buttons, got %d: %+v", len(got), got)
	}
	got = collectGhostElements(sampleTree(), ghostElementQuery{Query: "save", Role: "AXTextField"})
	if len(got) != 0 {
		t.Errorf("role filter should exclude buttons, got %+v", got)
	}
}

func TestCollectGhostElementsPath(t *testing.T) {
	got := collectGhostElements(sampleTree(), ghostElementQuery{Query: "Save As"})
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if want := "Drawing1.dwg > Standard > Save As…"; got[0].Path != want {
		t.Errorf("Path = %q, want %q", got[0].Path, want)
	}
}

// THE CONTRACT: ambiguity must refuse, not guess. A wrong click in a CAD app or
// an ERP is destructive; picking matches[0] silently is the failure mode this
// whole file exists to remove.
func TestResolveGhostElementRefusesAmbiguity(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	m, deny := resolveGhostElement(eng, ghostElementQuery{Query: "save"})
	if m != nil {
		t.Fatalf("expected refusal, got a match: %+v", m)
	}
	if deny == nil || deny.Code != "ambiguous" {
		t.Fatalf("expected code=ambiguous, got %+v", deny)
	}
	if !strings.Contains(deny.Error, "index") {
		t.Errorf("refusal should tell the caller how to disambiguate: %q", deny.Error)
	}
	init, _ := deny.Initial.(map[string]interface{})
	if init["count"] != 2 {
		t.Errorf("refusal must carry the candidate count, got %v", init["count"])
	}
	if _, ok := init["matches"]; !ok {
		t.Error("refusal must carry the candidate list")
	}
}

// Regression guard for the *int design: index 0 must be selectable in an
// ambiguous set. With a plain int this was indistinguishable from "unset" and
// the first candidate could never be chosen.
func TestResolveGhostElementIndexZeroSelectable(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	zero := 0
	m, deny := resolveGhostElement(eng, ghostElementQuery{Query: "save", Index: &zero})
	if deny != nil {
		t.Fatalf("index 0 should select the first match, got refusal: %+v", deny)
	}
	if m.Name != "Save" {
		t.Errorf("selected %q, want \"Save\"", m.Name)
	}
}

func TestResolveGhostElementIndexSelects(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	one := 1
	m, deny := resolveGhostElement(eng, ghostElementQuery{Query: "save", Index: &one})
	if deny != nil {
		t.Fatalf("unexpected refusal: %+v", deny)
	}
	if m.Name != "Save As…" {
		t.Errorf("selected %q, want \"Save As…\"", m.Name)
	}
}

func TestResolveGhostElementIndexOutOfRange(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	big := 99
	if _, deny := resolveGhostElement(eng, ghostElementQuery{Query: "save", Index: &big}); deny == nil {
		t.Fatal("expected out-of-range refusal")
	} else if deny.Code != "bad_payload" {
		t.Errorf("code = %q, want bad_payload", deny.Code)
	}
}

func TestResolveGhostElementUniqueSucceeds(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	m, deny := resolveGhostElement(eng, ghostElementQuery{Query: "Cancel"})
	if deny != nil {
		t.Fatalf("unique match should resolve, got %+v", deny)
	}
	if m.CenterX != 1330 {
		t.Errorf("CenterX = %d, want 1330", m.CenterX)
	}
}

func TestResolveGhostElementNotFound(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{root: sampleTree()}}
	_, deny := resolveGhostElement(eng, ghostElementQuery{Query: "Nonexistent"})
	if deny == nil || deny.Code != "not_found" {
		t.Fatalf("expected not_found, got %+v", deny)
	}
}

func TestFocusDesktopAppRejectsInjection(t *testing.T) {
	for _, bad := range []string{"Safari; rm -rf /", "Safari && id", "Safari`id`", "Safari\nrm"} {
		if err := focusDesktopApp(context.Background(), bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		} else if !strings.Contains(err.Error(), "illegal characters") {
			t.Errorf("expected illegal-characters error for %q, got %v", bad, err)
		}
	}
	if err := focusDesktopApp(context.Background(), "  "); err == nil {
		t.Error("expected empty name to be rejected")
	}
}

// The element verbs must be registered, or they are unreachable regardless of
// how well the resolver works.
func TestGhostElementVerbsRegistered(t *testing.T) {
	for _, verb := range []string{"ghost_elements", "ghost_click_element", "ghost_type_into_element", "ghost_focus_app"} {
		if _, ok := opsRegistry[verb]; !ok {
			t.Errorf("verb %q is not registered", verb)
		}
	}
}

// ---- closed loop ---------------------------------------------------------

// mutableTree lets a test change what the "screen" shows between the action and
// the verification read — which is the whole point of a closed loop.
type mutableTree struct{ root *ghost.Node }

func (m mutableTree) Windows() ([]ghost.Node, error)         { return m.root.Children, nil }
func (m mutableTree) ElementTree(string) (ghost.Node, error) { return *m.root, nil }
func (m mutableTree) Find(string) (*ghost.Node, error)       { return nil, nil }

func treeWithFieldValue(val string) ghost.Node {
	return ghost.Node{
		Role: "AXWindow", Name: "Form", X: 0, Y: 0, Width: 800, Height: 600,
		Children: []ghost.Node{
			{Role: "AXTextField", Name: "Quantity", Value: val, X: 10, Y: 10, Width: 200, Height: 30},
		},
	}
}

// The value landed → verified.
func TestVerifyGhostElementConfirmsValue(t *testing.T) {
	root := treeWithFieldValue("42")
	eng := &ghost.Engine{Tree: mutableTree{root: &root}}
	ok, why := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, "42")
	if !ok {
		t.Fatalf("expected verified, got: %s", why)
	}
}

// THE CORE GUARANTEE: keystrokes were emitted but the field is empty. Reporting
// success here is exactly what makes a GUI agent untrustworthy.
func TestVerifyGhostElementDetectsValueDidNotLand(t *testing.T) {
	root := treeWithFieldValue("")
	eng := &ghost.Engine{Tree: mutableTree{root: &root}}
	ok, why := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, "42")
	if ok {
		t.Fatal("empty field must NOT verify as success")
	}
	if !strings.Contains(why, "empty") {
		t.Errorf("reason should say what the field actually reads, got %q", why)
	}
}

// A field that reformats what it was given (trimming, currency, autocomplete)
// must still verify — demanding equality would fail correct actions.
func TestVerifyGhostElementAllowsReformatting(t *testing.T) {
	root := treeWithFieldValue("$42.00")
	eng := &ghost.Engine{Tree: mutableTree{root: &root}}
	if ok, why := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, "42"); !ok {
		t.Fatalf("substring match should tolerate reformatting, got: %s", why)
	}
}

// The element vanishing after the action is a failure, not a success.
func TestVerifyGhostElementDetectsDisappearance(t *testing.T) {
	root := ghost.Node{Role: "AXWindow", Name: "Empty", Width: 800, Height: 600}
	eng := &ghost.Engine{Tree: mutableTree{root: &root}}
	ok, why := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, "42")
	if ok {
		t.Fatal("a missing element must not verify")
	}
	if !strings.Contains(why, "no longer on screen") {
		t.Errorf("unexpected reason: %q", why)
	}
}

// Cannot see ⇒ cannot confirm. An unverifiable action is not a successful one.
func TestVerifyGhostElementUnreadableTreeIsNotSuccess(t *testing.T) {
	eng := &ghost.Engine{Tree: stubTree{err: errors.New("accessibility denied")}}
	if ok, _ := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, "42"); ok {
		t.Fatal("an unreadable tree must never report verified")
	}
}

// With no wanted value, presence alone is the post-condition (the `expect` form).
func TestVerifyGhostElementPresenceOnly(t *testing.T) {
	root := treeWithFieldValue("anything")
	eng := &ghost.Engine{Tree: mutableTree{root: &root}}
	if ok, why := verifyGhostElement(eng, ghostElementQuery{Query: "Quantity"}, ""); !ok {
		t.Fatalf("presence-only check failed: %s", why)
	}
}
