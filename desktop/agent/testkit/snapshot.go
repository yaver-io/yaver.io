package testkit

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/chromedp/chromedp"
)

// SnapshotMode controls how snapshot steps behave at runtime.
type SnapshotMode string

const (
	// SnapshotModeCheck (default) compares the current page screenshot
	// against the on-disk baseline and fails if they differ beyond the
	// threshold.
	SnapshotModeCheck SnapshotMode = "check"
	// SnapshotModeUpdate writes the current screenshot as the new
	// baseline (no comparison). Equivalent to Playwright's
	// --update-snapshots / Jest's --updateSnapshot.
	SnapshotModeUpdate SnapshotMode = "update"
)

// SnapshotConfig is the global config the runner is started with.
// Each spec inherits this; per-step overrides happen via the YAML.
type SnapshotConfig struct {
	Mode SnapshotMode
	// MaxDiffPixelRatio is the fraction of pixels that may differ before
	// a snapshot is considered a failure (0 = exact match, 1 = always
	// pass). Default 0.001 (0.1%).
	MaxDiffPixelRatio float64
	// PerChannelTolerance is the per-channel delta below which two
	// pixels are considered identical. Useful for handling JPEG /
	// anti-aliasing noise. Default 4.
	PerChannelTolerance int
}

// DefaultSnapshotConfig returns conservative defaults for solo dev use.
func DefaultSnapshotConfig() SnapshotConfig {
	return SnapshotConfig{
		Mode:                SnapshotModeCheck,
		MaxDiffPixelRatio:   0.001,
		PerChannelTolerance: 4,
	}
}

// snapshotBaselineDir returns the directory where baseline PNGs live for
// a given spec. Baselines are committed to git, so they live next to the
// spec, not in the .yaver-test-results artifact dir.
func snapshotBaselineDir(spec *Spec) string {
	return filepath.Join(filepath.Dir(spec.Path), "snapshots")
}

// runSnapshotStep handles a `snapshot:` step. The runner calls this
// after the step's chromedp action; this function captures the page,
// compares to a baseline (or writes a new one in update mode), and
// returns an error if the diff is over the threshold.
func runSnapshotStep(ctx context.Context, spec *Spec, name string, opts RunOptions) error {
	if name == "" {
		return fmt.Errorf("snapshot step requires a name")
	}
	cfg := opts.Snapshot
	if cfg.Mode == "" {
		cfg = DefaultSnapshotConfig()
		cfg.Mode = opts.Snapshot.Mode // honor caller override even if zero-valued
		if cfg.Mode == "" {
			cfg.Mode = SnapshotModeCheck
		}
	}
	if cfg.MaxDiffPixelRatio == 0 {
		cfg.MaxDiffPixelRatio = 0.001
	}
	if cfg.PerChannelTolerance == 0 {
		cfg.PerChannelTolerance = 4
	}

	var buf []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return fmt.Errorf("snapshot %q: capture: %w", name, err)
	}

	baselineDir := snapshotBaselineDir(spec)
	if err := os.MkdirAll(baselineDir, 0o755); err != nil {
		return fmt.Errorf("snapshot %q: mkdir baseline: %w", name, err)
	}
	baselinePath := filepath.Join(baselineDir, sanitizeName(name)+".png")

	if cfg.Mode == SnapshotModeUpdate {
		if err := os.WriteFile(baselinePath, buf, 0o644); err != nil {
			return fmt.Errorf("snapshot %q: write baseline: %w", name, err)
		}
		return nil
	}

	// Check mode: load baseline if present.
	baseFile, err := os.Open(baselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			// No baseline yet — write the current screenshot and pass.
			// First-run is always green; the dev commits the baseline.
			if err := os.WriteFile(baselinePath, buf, 0o644); err != nil {
				return fmt.Errorf("snapshot %q: write initial baseline: %w", name, err)
			}
			return nil
		}
		return fmt.Errorf("snapshot %q: open baseline: %w", name, err)
	}
	defer baseFile.Close()

	baseImg, _, err := image.Decode(baseFile)
	if err != nil {
		return fmt.Errorf("snapshot %q: decode baseline: %w", name, err)
	}

	gotImg, err := png.Decode(byteReader(buf))
	if err != nil {
		return fmt.Errorf("snapshot %q: decode current: %w", name, err)
	}

	diff, ratio := compareImages(baseImg, gotImg, cfg.PerChannelTolerance)
	if ratio <= cfg.MaxDiffPixelRatio {
		return nil
	}

	// Write the diff and the current image next to the baseline so the
	// dev can inspect what changed without re-running.
	currentPath := filepath.Join(baselineDir, sanitizeName(name)+".current.png")
	diffPath := filepath.Join(baselineDir, sanitizeName(name)+".diff.png")
	_ = os.WriteFile(currentPath, buf, 0o644)
	if diff != nil {
		f, ferr := os.Create(diffPath)
		if ferr == nil {
			_ = png.Encode(f, diff)
			_ = f.Close()
		}
	}
	return fmt.Errorf("snapshot %q: %.4f%% pixels differ (max %.4f%%) — diff at %s",
		name, ratio*100, cfg.MaxDiffPixelRatio*100, diffPath)
}

// compareImages diffs two images pixel-by-pixel with a per-channel
// tolerance. Returns a red-tinted diff image and the ratio of differing
// pixels to total pixels. If the dimensions don't match, ratio = 1.0.
func compareImages(a, b image.Image, tolerance int) (image.Image, float64) {
	bA := a.Bounds()
	bB := b.Bounds()
	if bA.Dx() != bB.Dx() || bA.Dy() != bB.Dy() {
		// Render the smaller of the two with a uniform red tint so the
		// dev sees "this baseline doesn't even match in size."
		out := image.NewNRGBA(bA)
		for y := bA.Min.Y; y < bA.Max.Y; y++ {
			for x := bA.Min.X; x < bA.Max.X; x++ {
				out.Set(x, y, color.NRGBA{255, 0, 0, 255})
			}
		}
		return out, 1.0
	}
	out := image.NewNRGBA(bA)
	w := bA.Dx()
	h := bA.Dy()
	total := w * h
	differing := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ar, ag, ab, _ := a.At(x+bA.Min.X, y+bA.Min.Y).RGBA()
			br, bg, bb, _ := b.At(x+bB.Min.X, y+bB.Min.Y).RGBA()
			da := absInt(int(ar>>8) - int(br>>8))
			db := absInt(int(ag>>8) - int(bg>>8))
			dc := absInt(int(ab>>8) - int(bb>>8))
			if da > tolerance || db > tolerance || dc > tolerance {
				differing++
				out.Set(x+bA.Min.X, y+bA.Min.Y, color.NRGBA{255, 0, 0, 255})
			} else {
				// fade the matching area so the diff is easy to read
				out.Set(x+bA.Min.X, y+bA.Min.Y, color.NRGBA{200, 200, 200, 80})
			}
		}
	}
	return out, float64(differing) / float64(total)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// byteReader is a tiny adapter so image.Decode can read from a []byte
// without us pulling in bytes.NewReader at every call site.
type byteReaderT struct {
	b []byte
	i int
}

func byteReader(b []byte) *byteReaderT { return &byteReaderT{b: b} }
func (r *byteReaderT) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, errEOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

var errEOF = fmtErrEOF{}

type fmtErrEOF struct{}

func (fmtErrEOF) Error() string { return "EOF" }

// snapshotName extracts the snapshot label from a step. Used by the
// runner; lives here to keep snapshot logic in one file.
func snapshotName(step Step) string {
	if step.Snapshot == "" {
		return ""
	}
	return strings.TrimSpace(step.Snapshot)
}
