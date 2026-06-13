package circuit

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// PlotPNG renders a SimResult to a PNG waveform chart, dependency-free (std
// image/png only). Column 0 is the X axis (time/freq/sweep); every other column
// is a trace. selected filters traces by signal name (empty = all). For AC the
// X axis is drawn logarithmically. This image powers the circuit_plot
// first-class MCP tool so the host model can SEE the waveform.
func PlotPNG(res SimResult, selected []string) ([]byte, error) {
	if len(res.Samples) == 0 || len(res.Signals) < 2 {
		return nil, fmt.Errorf("no waveform to plot (run a tran/ac/dc analysis first)")
	}
	logX := res.Analysis == "ac"

	// pick trace columns
	want := map[string]bool{}
	for _, s := range selected {
		want[strings.TrimSpace(s)] = true
	}
	var cols []int
	for i := 1; i < len(res.Signals); i++ {
		name := res.Signals[i]
		if res.Analysis == "ac" && strings.HasSuffix(name, "deg") {
			continue // default Bode = magnitude only
		}
		if len(want) == 0 || want[name] {
			cols = append(cols, i)
		}
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("no matching signals to plot")
	}

	const W, H = 760, 420
	const padL, padR, padT, padB = 56, 16, 18, 30
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	bg := color.RGBA{18, 20, 26, 255}
	grid := color.RGBA{44, 48, 58, 255}
	axis := color.RGBA{120, 128, 140, 255}
	fill(img, bg)

	plotX0, plotY0 := padL, padT
	plotW, plotH := W-padL-padR, H-padT-padB

	// x range
	xmin, xmax := math.Inf(1), math.Inf(-1)
	ymin, ymax := math.Inf(1), math.Inf(-1)
	for _, row := range res.Samples {
		x := row[0]
		if logX {
			if x <= 0 {
				continue
			}
			x = math.Log10(x)
		}
		xmin, xmax = math.Min(xmin, x), math.Max(xmax, x)
		for _, c := range cols {
			if c < len(row) {
				v := row[c]
				if math.IsInf(v, 0) || math.IsNaN(v) {
					continue
				}
				ymin, ymax = math.Min(ymin, v), math.Max(ymax, v)
			}
		}
	}
	if xmax <= xmin {
		xmax = xmin + 1
	}
	if ymax <= ymin {
		ymax, ymin = ymin+1, ymin-1
	}
	// pad y
	yr := ymax - ymin
	ymin -= yr * 0.08
	ymax += yr * 0.08

	sx := func(x float64) int {
		if logX && x > 0 {
			x = math.Log10(x)
		}
		return plotX0 + int(float64(plotW)*(x-xmin)/(xmax-xmin))
	}
	sy := func(y float64) int {
		return plotY0 + plotH - int(float64(plotH)*(y-ymin)/(ymax-ymin))
	}

	// grid (5x4)
	for i := 0; i <= 5; i++ {
		x := plotX0 + plotW*i/5
		vline(img, x, plotY0, plotY0+plotH, grid)
	}
	for i := 0; i <= 4; i++ {
		y := plotY0 + plotH*i/4
		hline(img, plotX0, plotX0+plotW, y, grid)
	}
	// zero line
	if ymin < 0 && ymax > 0 {
		hline(img, plotX0, plotX0+plotW, sy(0), axis)
	}
	// axes
	vline(img, plotX0, plotY0, plotY0+plotH, axis)
	hline(img, plotX0, plotX0+plotW, plotY0+plotH, axis)

	palette := []color.RGBA{
		{86, 180, 255, 255}, {120, 220, 130, 255}, {255, 170, 70, 255},
		{235, 110, 150, 255}, {190, 140, 255, 255}, {110, 220, 220, 255},
	}
	for ci, c := range cols {
		col := palette[ci%len(palette)]
		var px, py int
		first := true
		for _, row := range res.Samples {
			if c >= len(row) {
				continue
			}
			v := row[c]
			if math.IsInf(v, 0) || math.IsNaN(v) {
				continue
			}
			x := sx(row[0])
			y := sy(v)
			if !first {
				drawLine(img, px, py, x, y, col)
			}
			px, py, first = x, y, false
		}
		// legend swatch
		ly := plotY0 + 6 + ci*12
		for dx := 0; dx < 16; dx++ {
			for dy := 0; dy < 6; dy++ {
				img.SetRGBA(plotX0+plotW-150+dx, ly+dy, col)
			}
		}
		drawText(img, plotX0+plotW-130, ly, res.Signals[c], col)
	}

	// axis captions
	drawText(img, plotX0, plotY0+plotH+14, xAxisLabel(res.Analysis, logX), axis)
	drawText(img, 4, plotY0+8, yAxisLabel(res.Analysis), axis)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xAxisLabel(analysis string, logX bool) string {
	switch analysis {
	case "tran":
		return "time (s)"
	case "ac":
		return "freq (Hz, log)"
	case "dc":
		return "sweep"
	}
	return "x"
}

func yAxisLabel(analysis string) string {
	if analysis == "ac" {
		return "dB"
	}
	return "V"
}

// ---- tiny raster primitives (no external deps) ----

func fill(img *image.RGBA, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func hline(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		img.SetRGBA(x, y, c)
	}
}

func vline(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		img.SetRGBA(x, y, c)
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.SetRGBA(x0, y0, c)
		img.SetRGBA(x0, y0+1, c) // 2px thickness for visibility
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// drawText renders a label with the built-in 7x13 bitmap face (x/image).
func drawText(img *image.RGBA, x, y int, s string, c color.RGBA) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y+9),
	}
	d.DrawString(s)
}
