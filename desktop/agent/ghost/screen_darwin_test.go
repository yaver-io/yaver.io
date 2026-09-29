//go:build darwin && cgo

package ghost

import "testing"

// TestDisplaysEnumeratesMac proves the macOS ghost reports every active display
// with sane dimensions and exactly one primary — the enumeration multi-monitor
// click mapping depends on. Display enumeration needs no TCC permission, so it
// runs in CI/the test process.
func TestDisplaysEnumeratesMac(t *testing.T) {
	disps, err := macScreen{}.Displays()
	if err != nil {
		t.Fatalf("Displays: %v", err)
	}
	if len(disps) == 0 {
		t.Fatal("expected at least one display")
	}
	primaries := 0
	for _, d := range disps {
		if d.Width <= 0 || d.Height <= 0 {
			t.Fatalf("display %d has bad dims: %+v", d.Index, d)
		}
		if d.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("expected exactly one primary display, got %d", primaries)
	}
	if disps[0].Index != 0 {
		t.Fatalf("first display index = %d, want 0", disps[0].Index)
	}
}
