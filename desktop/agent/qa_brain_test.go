package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/yaver-io/agent/studio"
)

// fakeQAModel returns canned replies so the brain logic is tested with no network.
type fakeQAModel struct {
	decideReply  string
	judgeVerdict string
	judgeReason  string
	lastUser     string
	lastPNG      []byte
}

func (f *fakeQAModel) Decide(ctx context.Context, system, user string, png []byte) (string, error) {
	f.lastUser = user
	f.lastPNG = png
	return f.decideReply, nil
}

func (f *fakeQAModel) Judge(ctx context.Context, expectation string, png []byte) (string, string, error) {
	return f.judgeVerdict, f.judgeReason, nil
}

func TestBrainNextActionParsesJSON(t *testing.T) {
	f := &fakeQAModel{decideReply: "Sure!\n```json\n{\"verb\":\"TapText\",\"args\":{\"text\":\"Continue with Email\"},\"done\":false,\"why\":\"start signup\"}\n```"}
	b := newLLMBrain(f, "create an account")
	act, err := b.NextAction(context.Background(), studio.Observation{ViewTree: "<hierarchy/>", Goal: "create an account"})
	if err != nil {
		t.Fatalf("next action: %v", err)
	}
	if act.Step.Verb != "taptext" || act.Step.Args["text"] != "Continue with Email" {
		t.Errorf("parsed step wrong: %+v", act.Step)
	}
	if act.Done {
		t.Error("should not be done")
	}
	// The goal + view tree must reach the model.
	if !strings.Contains(f.lastUser, "create an account") {
		t.Errorf("goal not in prompt: %q", f.lastUser)
	}
}

func TestBrainVisionFallbackWhenTreeEmpty(t *testing.T) {
	f := &fakeQAModel{decideReply: `{"verb":"tap","args":{"x":0.5,"y":0.25},"done":false,"why":"tap by image"}`}
	b := newLLMBrain(f, "open settings")
	// empty view tree (redroid uiautomator dead) but a screenshot present
	act, err := b.NextAction(context.Background(), studio.Observation{ViewTree: "", Screenshot: testPNG(t, 1000, 2000), Goal: "open settings"})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if act.Step.Args["x"] != "500" || act.Step.Args["y"] != "500" {
		t.Fatalf("fractional coordinates not normalized: %+v", act.Step.Args)
	}
	if len(f.lastPNG) == 0 {
		t.Error("vision fallback should send the screenshot to the model")
	}
	if !strings.Contains(f.lastUser, "screenshot") {
		t.Errorf("vision prompt should mention the screenshot: %q", f.lastUser)
	}
}

func TestBrainTextPathWhenTreePresent(t *testing.T) {
	f := &fakeQAModel{decideReply: `{"verb":"taptext","args":{"text":"Wi-Fi"},"done":false}`}
	b := newLLMBrain(f, "open wifi")
	_, _ = b.NextAction(context.Background(), studio.Observation{
		ViewTree: `<hierarchy><node text="Wi-Fi" bounds="[0,0][100,100]"/></hierarchy>`, Screenshot: []byte("png"), Goal: "open wifi",
	})
	if len(f.lastPNG) != 0 {
		t.Error("text path should NOT send a screenshot when the tree is present")
	}
}

func TestBrainUnparseableReplyReturnsError(t *testing.T) {
	f := &fakeQAModel{decideReply: "I cannot help with that."}
	b := newLLMBrain(f, "x")
	_, err := b.NextAction(context.Background(), studio.Observation{})
	if err == nil {
		t.Fatal("unparseable reply should fail the harness step")
	}
}

func TestParseBrainActionAcceptsNumericArgs(t *testing.T) {
	act, err := parseBrainAction(`{"verb":"tap","args":{"x":371,"y":620},"done":false}`)
	if err != nil {
		t.Fatalf("parse numeric args: %v", err)
	}
	if act.Step.Args["x"] != "371" || act.Step.Args["y"] != "620" {
		t.Fatalf("numeric args not stringified: %+v", act.Step.Args)
	}
}

func TestBrainAssertVerdicts(t *testing.T) {
	cases := []struct {
		verdict  string
		wantPass bool
		wantSev  string
	}{
		{"pass", true, "info"},
		{"fail", false, "fail"},
		{"warn", false, "warn"},
	}
	for _, c := range cases {
		f := &fakeQAModel{judgeVerdict: c.verdict, judgeReason: "because"}
		b := newLLMBrain(f, "g")
		v, _ := b.Assert(context.Background(), "home is visible", []byte("png"))
		if v.Pass != c.wantPass || v.Severity != c.wantSev {
			t.Errorf("verdict %q → pass=%v sev=%q, want pass=%v sev=%q", c.verdict, v.Pass, v.Severity, c.wantPass, c.wantSev)
		}
	}
}

func TestExtractJSONObject(t *testing.T) {
	in := `prose {"a":1,"b":{"c":"}"}} trailing`
	got := qaExtractJSONObject(in)
	if got != `{"a":1,"b":{"c":"}"}}` {
		t.Errorf("balanced extract failed: %q", got)
	}
	if qaExtractJSONObject("no json here") != "" {
		t.Error("should return empty when no object")
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
