package main

import "testing"

func TestParseAndroidWMSize_PhysicalOnly(t *testing.T) {
	out := "Physical size: 1080x2400\n"
	got := parseAndroidWMSize(out)
	if got.Width != 1080 || got.Height != 2400 {
		t.Errorf("dims=%+v want 1080x2400", got)
	}
	if got.Rotation != "portrait" {
		t.Errorf("rotation=%q want portrait", got.Rotation)
	}
}

func TestParseAndroidWMSize_OverrideWins(t *testing.T) {
	// Real Android emulator output when the user has run `wm size 720x1440`.
	out := "Physical size: 1080x2400\nOverride size: 720x1440\n"
	got := parseAndroidWMSize(out)
	if got.Width != 720 || got.Height != 1440 {
		t.Errorf("dims=%+v want 720x1440 (override should win)", got)
	}
}

func TestParseAndroidWMSize_LandscapeRotation(t *testing.T) {
	out := "Physical size: 2400x1080\n"
	got := parseAndroidWMSize(out)
	if got.Rotation != "landscape" {
		t.Errorf("rotation=%q want landscape", got.Rotation)
	}
}

func TestParseAndroidWMSize_GarbageReturnsEmpty(t *testing.T) {
	got := parseAndroidWMSize("error: device offline\n")
	if got.Width != 0 || got.Height != 0 {
		t.Errorf("garbage input should produce zero dims, got %+v", got)
	}
}

func TestParsePNGDims_ValidIHDR(t *testing.T) {
	// Build a synthetic 8-byte PNG sig + IHDR length + "IHDR" + width=393 + height=852.
	raw := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length (13)
		'I', 'H', 'D', 'R',
		0x00, 0x00, 0x01, 0x89, // width = 0x189 = 393
		0x00, 0x00, 0x03, 0x54, // height = 0x354 = 852
	}
	got := parsePNGDims(raw)
	if got.Width != 393 || got.Height != 852 {
		t.Errorf("dims=%+v want 393x852", got)
	}
	if got.Rotation != "portrait" {
		t.Errorf("rotation=%q want portrait", got.Rotation)
	}
}

func TestParsePNGDims_TooShort(t *testing.T) {
	got := parsePNGDims([]byte{1, 2, 3})
	if got.Width != 0 || got.Height != 0 {
		t.Errorf("short input should produce zero dims, got %+v", got)
	}
}

func TestParsePNGDims_BadSignature(t *testing.T) {
	raw := make([]byte, 24)
	raw[0] = 0xFF
	got := parsePNGDims(raw)
	if got.Width != 0 || got.Height != 0 {
		t.Errorf("bad signature should produce zero dims, got %+v", got)
	}
}
