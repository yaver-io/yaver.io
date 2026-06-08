package studio

import (
	"strings"
	"testing"
)

func TestDrawtextEscape(t *testing.T) {
	// chars that break the filtergraph must be gone.
	got := drawtextEscape("3. Foreground: 'running', now")
	for _, bad := range []string{":", "'", ","} {
		if strings.Contains(got, bad) {
			t.Errorf("escaped text still contains %q: %q", bad, got)
		}
	}
}

func TestBuildCaptionFilter(t *testing.T) {
	cues := []Cue{
		{Text: "1. Open the app", StartSec: 0, EndSec: 6},
		{Text: "", StartSec: 6, EndSec: 7}, // empty → skipped
		{Text: "3. Notification appears", StartSec: 7, EndSec: 12},
	}
	f := buildCaptionFilter("/font.ttf", cues)
	if !strings.HasPrefix(f, "drawbox=") {
		t.Errorf("filter must start with the banner: %s", f)
	}
	if strings.Count(f, "drawtext=") != 2 { // empty cue skipped
		t.Errorf("expected 2 drawtext, got: %s", f)
	}
	if !strings.Contains(f, "enable='between(t,0.00,6.00)'") {
		t.Errorf("missing timed enable window: %s", f)
	}
	if !strings.Contains(f, "1. Open the app") {
		t.Errorf("caption text missing: %s", f)
	}
}
