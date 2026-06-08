package studio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// compositor.go — the ffmpeg-only compositor (no GUI deps). Turns a raw capture
// + the timed caption Cues that RunFlowRecording emits into a reviewer-ready
// captioned MP4. Recipes are data; this is the `permission-proof` recipe: a
// translucent bottom banner with the step caption timed to each Cue.
//
// Requires ffmpeg built with drawtext (--enable-libfreetype). On a Yaver farm
// box ffmpeg is provisioned with it; minimal local builds may lack it, in which
// case CaptionMP4 returns ErrNoDrawtext and the caller ships the raw clip + the
// caption list for manual compositing.

// ErrNoDrawtext means the available ffmpeg has no drawtext filter.
var ErrNoDrawtext = fmt.Errorf("ffmpeg has no drawtext filter (needs --enable-libfreetype)")

var commonFontPaths = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
	"/usr/share/fonts/TTF/DejaVuSans.ttf",
	"/System/Library/Fonts/Supplemental/Arial.ttf",
	"/Library/Fonts/Arial.ttf",
}

// findFont returns the first existing font from a candidate then the common set.
func findFont(candidate string) string {
	if candidate != "" {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	for _, p := range commonFontPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// drawtextEscape sanitizes caption text for an ffmpeg drawtext value: the chars
// that break the filtergraph (':' separates options, ',' separates filters, "'"
// quotes) are replaced with safe equivalents. Reviewer captions don't need them.
func drawtextEscape(s string) string {
	r := strings.NewReplacer(":", " -", ",", " ", "'", "", "\\", "", "%", " pct ", "\n", " ")
	return r.Replace(s)
}

// buildCaptionFilter builds the -vf filtergraph: a bottom banner plus one
// drawtext per cue, each enabled only during its [StartSec,EndSec] window.
// Exported-for-test via the lowercase helper; deterministic + ffmpeg-free.
func buildCaptionFilter(fontPath string, cues []Cue) string {
	parts := []string{"drawbox=y=ih-170:color=black@0.6:width=iw:height=170:t=fill"}
	for _, c := range cues {
		// Internal step-error cues are kept in the job log, never burned into the
		// reviewer video.
		if strings.HasPrefix(strings.TrimSpace(c.Text), "[step error") {
			continue
		}
		txt := drawtextEscape(c.Text)
		if strings.TrimSpace(txt) == "" {
			continue
		}
		fs := 40
		if len(txt) > 38 {
			fs = 33
		}
		parts = append(parts, fmt.Sprintf(
			"drawtext=fontfile=%s:text='%s':fontcolor=white:fontsize=%d:x=(w-tw)/2:y=h-115:enable='between(t,%.2f,%.2f)'",
			fontPath, txt, fs, c.StartSec, c.EndSec))
	}
	return strings.Join(parts, ",")
}

// ffmpegHasDrawtext reports whether the ffmpeg at bin supports drawtext.
func ffmpegHasDrawtext(ctx context.Context, bin string) bool {
	out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte("drawtext"))
}

// CaptionMP4 captions raw MP4 bytes with the cues and returns the captioned MP4
// bytes. ffmpegBin defaults to "ffmpeg" on PATH; fontPath falls back to a common
// system font. Returns ErrNoDrawtext if the ffmpeg can't draw text.
func CaptionMP4(ctx context.Context, raw []byte, cues []Cue, ffmpegBin, fontPath string) ([]byte, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if !ffmpegHasDrawtext(ctx, ffmpegBin) {
		return nil, ErrNoDrawtext
	}
	font := findFont(fontPath)
	if font == "" {
		return nil, fmt.Errorf("no usable .ttf font found (pass fontPath)")
	}

	in, err := os.CreateTemp("", "studio-in-*.mp4")
	if err != nil {
		return nil, err
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(raw); err != nil {
		return nil, err
	}
	in.Close()

	outPath := in.Name() + ".cap.mp4"
	defer os.Remove(outPath)

	args := []string{
		"-y", "-i", in.Name(),
		"-vf", buildCaptionFilter(font, cues),
		"-an", "-movflags", "+faststart", "-pix_fmt", "yuv420p",
		outPath,
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, tail(stderr.String(), 400))
	}
	return os.ReadFile(outPath)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
