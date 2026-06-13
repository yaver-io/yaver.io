package circuit

import (
	"bytes"
	"context"
	"image/png"
	"testing"
)

func TestPlotPNGValid(t *testing.T) {
	nl, _ := ParseSPICE(`* rc
V1 1 0 PULSE(0 5 0 1e-9 0 1 2)
R1 1 2 1k
C1 2 0 1u
.end`)
	res, err := NewBuiltinBackend().Simulate(context.Background(), nl, Analysis{Type: "tran", TStop: 5e-3, TStep: 1e-5})
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	data, err := PlotPNG(res, nil)
	if err != nil {
		t.Fatalf("plot: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() < 100 || img.Bounds().Dy() < 100 {
		t.Fatalf("png too small: %v", img.Bounds())
	}
}
