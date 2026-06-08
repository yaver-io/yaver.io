package studio

import (
	"context"
	"fmt"
	"time"
)

// Step is one action in a capture flow, with an optional on-screen caption and a
// dwell so the result is visible in the recording.
type Step struct {
	Caption string
	Run     func(ctx context.Context, d Driver) error
	HoldSec int
}

// Cue is a timed caption span derived from running a flow while recording, fed
// to the compositor (ffmpeg drawtext between StartSec..EndSec).
type Cue struct {
	Text     string  `json:"text"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}

// RunFlowRecording records the surface while executing steps, returning the MP4
// bytes and the caption cues aligned to the recording timeline. The surface must
// already be provisioned + the app installed.
func RunFlowRecording(ctx context.Context, surface CaptureSurface, steps []Step, maxSec int) ([]byte, []Cue, error) {
	d := surface.Driver()
	if maxSec <= 0 {
		maxSec = sumHold(steps) + 8
	}
	if err := d.RecordStart(ctx, maxSec); err != nil {
		return nil, nil, fmt.Errorf("record start: %w", err)
	}
	start := time.Now()
	var cues []Cue
	for _, st := range steps {
		cueStart := time.Since(start).Seconds()
		var runErr error
		if st.Run != nil {
			runErr = st.Run(ctx, d)
		}
		if st.HoldSec > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(st.HoldSec) * time.Second):
			}
		}
		if st.Caption != "" {
			cues = append(cues, Cue{Text: st.Caption, StartSec: cueStart, EndSec: time.Since(start).Seconds()})
		}
		if runErr != nil {
			// best-effort: keep recording the partial flow, but surface the error
			cues = append(cues, Cue{Text: "[step error: " + runErr.Error() + "]", StartSec: cueStart, EndSec: time.Since(start).Seconds()})
		}
	}
	mp4, err := d.RecordStop(ctx)
	if err != nil {
		return nil, cues, fmt.Errorf("record stop: %w", err)
	}
	return mp4, cues, nil
}

func sumHold(steps []Step) int {
	t := 0
	for _, s := range steps {
		t += s.HoldSec
	}
	return t
}

// PermissionVideoSpec drives an end-to-end permission-justification capture.
type PermissionVideoSpec struct {
	App          App
	ArtifactPath string // APK built for the SURFACE's arch (amd64 for x86 redroid)
	Facts        *PermissionFacts
	StartAction  string       // FGS start intent action, e.g. io.yaver.mobile.sandbox.START
	Account      *AccountSpec // optional: sign in before the demo
	NavSteps     []Step       // optional: navigate to the in-app trigger; if empty,
	// the service is started directly (am start-foreground-service) — still valid
	// permission-use evidence, just without the in-app tap.
	MaxSec int
}

// PermissionProofSteps builds the reviewer scene: open → (sign in) → (navigate) →
// start service → notification → background → still running → stop. Captions are
// the same shot-list the prose generator emits, so video and prose agree.
func PermissionProofSteps(spec PermissionVideoSpec) []Step {
	steps := []Step{{
		Caption: "1. Open the app",
		Run:     func(ctx context.Context, d Driver) error { return d.Launch(ctx, spec.App) },
		HoldSec: 6,
	}}

	if spec.Account != nil {
		steps = append(steps, AccountSignInSteps(*spec.Account)...)
	}
	steps = append(steps, spec.NavSteps...)

	component := ""
	if spec.Facts != nil && spec.Facts.Service != nil {
		component = spec.App.Package + "/" + spec.Facts.Service.Name
	}

	if component != "" {
		steps = append(steps,
			Step{
				Caption: "2. User starts the feature",
				Run: func(ctx context.Context, d Driver) error {
					return d.StartForegroundService(ctx, component, spec.StartAction)
				},
				HoldSec: 4,
			},
			Step{
				Caption: "3. Foreground notification appears",
				Run:     func(ctx context.Context, d Driver) error { return d.ExpandNotifications(ctx) },
				HoldSec: 5,
			},
			Step{
				Caption: "4. Still running while backgrounded",
				Run: func(ctx context.Context, d Driver) error {
					_ = d.CollapseNotifications(ctx)
					return d.Home(ctx)
				},
				HoldSec: 4,
			},
			Step{
				Caption: "5. Still running",
				Run:     func(ctx context.Context, d Driver) error { return d.ExpandNotifications(ctx) },
				HoldSec: 4,
			},
			Step{
				Caption: "6. User stops it — notification clears",
				Run: func(ctx context.Context, d Driver) error {
					_ = d.CollapseNotifications(ctx)
					return d.StopService(ctx, component)
				},
				HoldSec: 3,
			},
		)
	}
	return steps
}

// AccountSignInSteps turns an AccountSpec into best-effort UI steps. The default
// here matches Yaver's auth screen (provider buttons + "Continue with Email").
// For other apps the caller overrides via NavSteps. When the provider needs a
// verification code, AccountSpec.CodeSource is invoked; if it is nil the flow
// stops at the code prompt and the caller must supply one (we never guess).
func AccountSignInSteps(a AccountSpec) []Step {
	switch a.Provider {
	case "email":
		steps := []Step{
			{Caption: "Sign in", Run: func(ctx context.Context, d Driver) error { return d.TapText(ctx, "Continue with Email") }, HoldSec: 2},
			{Run: func(ctx context.Context, d Driver) error { return d.Type(ctx, a.Email) }, HoldSec: 1},
			{Run: func(ctx context.Context, d Driver) error { return d.Key(ctx, "ENTER") }, HoldSec: 3},
		}
		if a.CodeSource != nil {
			steps = append(steps, Step{
				Caption: "Enter verification code",
				Run: func(ctx context.Context, d Driver) error {
					code, err := a.CodeSource(ctx)
					if err != nil {
						return fmt.Errorf("code source: %w", err)
					}
					if err := d.Type(ctx, code); err != nil {
						return err
					}
					return d.Key(ctx, "ENTER")
				},
				HoldSec: 4,
			})
		}
		return steps
	default:
		// OAuth providers open a browser/webview — app + provider specific.
		btn := map[string]string{
			"apple": "Continue with Apple", "google": "Continue with Google",
			"github": "Continue with GitHub", "gitlab": "Continue with GitLab",
			"microsoft": "Continue with Microsoft", "passkey": "Sign in with passkey",
		}[a.Provider]
		if btn == "" {
			return nil
		}
		return []Step{{Caption: "Sign in", Run: func(ctx context.Context, d Driver) error { return d.TapText(ctx, btn) }, HoldSec: 4}}
	}
}

// CapturePermissionVideo is the top-level orchestrator: provision the surface,
// install the app, optionally sign in, record the proof flow, tear down. Returns
// the MP4 bytes, the caption cues, and the reviewer prose — everything the
// surface produces, ready for the compositor + the Play form.
func CapturePermissionVideo(ctx context.Context, surface CaptureSurface, spec PermissionVideoSpec, appName, whatRuns string) ([]byte, []Cue, Justification, error) {
	var j Justification
	if spec.Facts != nil {
		j = GenerateJustification(spec.Facts, appName, whatRuns)
	}
	if err := surface.Provision(ctx); err != nil {
		return nil, nil, j, fmt.Errorf("provision: %w", err)
	}
	defer surface.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck — teardown is best-effort, must always run

	if spec.ArtifactPath != "" {
		if err := surface.Install(ctx, spec.ArtifactPath); err != nil {
			return nil, nil, j, fmt.Errorf("install: %w", err)
		}
	}
	mp4, cues, err := RunFlowRecording(ctx, surface, PermissionProofSteps(spec), spec.MaxSec)
	return mp4, cues, j, err
}
