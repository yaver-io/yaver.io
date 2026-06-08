package studio

import (
	"context"
	"fmt"
	"time"
)

// screenshots.go — store-screenshot capture on a CaptureSurface. A screenshot
// run launches the app and walks a list of scenes; each scene optionally runs
// navigation steps (taps/waits) to reach a screen, then grabs a PNG. Reuses the
// same Driver verbs and surfaces (redroid / iOS sim) as the video path, so one
// device session yields the whole set. Framing/localization (the compositor)
// happen downstream on the bytes returned here.

// ScreenshotScene is one screen to capture: optional steps to navigate there,
// then a screenshot tagged with Name.
type ScreenshotScene struct {
	Name  string // file-safe label, e.g. "home", "settings"
	Steps []Step // navigation to reach the screen (empty = capture current screen)
}

// ScreenshotSpec drives a screenshot run.
type ScreenshotSpec struct {
	App          App
	ArtifactPath string            // app artifact to install (built for the surface arch)
	Account      *AccountSpec      // optional sign-in before capturing
	Scenes       []ScreenshotScene // screens to capture (empty → just the launch screen)
	SettleSec    int               // dwell after launch / each nav before the shot (default 4)
}

// ScreenshotResult is one captured screen.
type ScreenshotResult struct {
	Name string `json:"name"`
	PNG  []byte `json:"-"`
}

// CaptureScreenshots provisions the surface, installs the app, signs in if
// asked, then captures each scene. Always tears down (billing stops there).
func CaptureScreenshots(ctx context.Context, surface CaptureSurface, spec ScreenshotSpec) ([]ScreenshotResult, error) {
	if err := surface.Provision(ctx); err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}
	defer surface.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck

	if spec.ArtifactPath != "" {
		if err := surface.Install(ctx, spec.ArtifactPath); err != nil {
			return nil, fmt.Errorf("install: %w", err)
		}
	}
	d := surface.Driver()
	settle := spec.SettleSec
	if settle <= 0 {
		settle = 4
	}

	if err := d.Launch(ctx, spec.App); err != nil {
		return nil, fmt.Errorf("launch: %w", err)
	}
	sleepCtx(ctx, settle)

	if spec.Account != nil {
		for _, st := range AccountSignInSteps(*spec.Account) {
			if st.Run != nil {
				_ = st.Run(ctx, d)
			}
			sleepCtx(ctx, st.HoldSec)
		}
	}

	scenes := spec.Scenes
	if len(scenes) == 0 {
		scenes = []ScreenshotScene{{Name: "launch"}}
	}

	var out []ScreenshotResult
	for i, sc := range scenes {
		for _, st := range sc.Steps {
			if st.Run != nil {
				if err := st.Run(ctx, d); err != nil {
					// best-effort navigation; still try to shoot what's there.
					break
				}
			}
			sleepCtx(ctx, st.HoldSec)
		}
		sleepCtx(ctx, settle)
		png, err := d.Screenshot(ctx)
		if err != nil {
			return out, fmt.Errorf("screenshot %q: %w", sc.Name, err)
		}
		name := sc.Name
		if name == "" {
			name = fmt.Sprintf("screen-%d", i+1)
		}
		out = append(out, ScreenshotResult{Name: name, PNG: png})
	}
	return out, nil
}

func sleepCtx(ctx context.Context, sec int) {
	if sec <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(sec) * time.Second):
	}
}
