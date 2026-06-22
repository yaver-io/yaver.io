package studio

import (
	"context"
	"fmt"
	"strings"
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

	// UseCase, when non-nil, switches the capture from the mechanical proof
	// (start→notify→home→stop) to a narrative that demonstrates WHY the
	// foreground service is required: it runs a real, long-running user task,
	// backgrounds the app while the task is still working, and shows the
	// "task finished" notification as the payoff. This is what a Play reviewer
	// needs to see for FOREGROUND_SERVICE_SPECIAL_USE — a mechanical clip that
	// just toggles a notification does not justify the permission.
	UseCase *UseCaseConfig
}

// UseCaseConfig parameterizes the narrative permission video. It is data-driven
// so the same engine works for Yaver's own on-device coding agent and for any
// third-party app: the caller supplies the human description of the work, the
// in-app affordances, and the on-screen/notification strings that prove the
// task is genuinely running and then finishing.
type UseCaseConfig struct {
	// WhatRuns is the human description of the long-running work, woven into
	// captions and prose, e.g. "an on-device coding agent running a real task".
	WhatRuns string
	// StartButtonText, when set, is tapped in the app UI to start the feature
	// (TapText). When empty the service is started directly via
	// StartForegroundService(component, StartAction) — use this when the feature
	// is awkward to reach by UI on the capture surface.
	StartButtonText string
	// StopButtonText, when set, is tapped to stop; otherwise StopService is used.
	StopButtonText string
	// TaskSteps are caller-injected steps that give the app a REAL task after the
	// service is up (e.g. navigate to the Tasks tab, type a prompt, submit, or —
	// more robustly on a flaky emulator — POST the task to the on-device agent).
	// Each step carries its own caption.
	TaskSteps []Step
	// ProgressText is a string that appears on screen or in the notification
	// while the task is working; the flow WaitTexts for it as proof of real work.
	ProgressText string
	// CompletionText is the "task finished" string (typically the completion
	// notification text); the flow WaitTexts for it as the payoff.
	CompletionText string
	// WaitProgressSec / WaitDoneSec bound the two waits (defaults 30 / 150).
	WaitProgressSec int
	WaitDoneSec     int
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

// UseCaseProofSteps builds the narrative scene that actually justifies the
// permission: open → (sign in) → (navigate) → start the feature → give it a
// REAL task → show the task working → expand the foreground notification (the
// process is alive) → background the app while the task is still running (this
// is the crux: without the foreground service Android would kill the process and
// lose the in-flight work) → wait for the task to finish in the background →
// show the "task finished" notification → stop. Captions name the actual work so
// a reviewer understands the use case, not just the mechanics.
func UseCaseProofSteps(spec PermissionVideoSpec, cfg UseCaseConfig) []Step {
	work := strings.TrimSpace(cfg.WhatRuns)
	if work == "" {
		work = "a long-running, user-started task"
	}
	progressWait := cfg.WaitProgressSec
	if progressWait <= 0 {
		progressWait = 30
	}
	doneWait := cfg.WaitDoneSec
	if doneWait <= 0 {
		doneWait = 150
	}

	steps := []Step{{
		Caption: "1. Open " + appLabel(spec) + " — an on-device tool",
		Run:     func(ctx context.Context, d Driver) error { return d.Launch(ctx, spec.App) },
		HoldSec: 5,
	}}
	if spec.Account != nil {
		steps = append(steps, AccountSignInSteps(*spec.Account)...)
	}
	steps = append(steps, spec.NavSteps...)

	component := ""
	if spec.Facts != nil && spec.Facts.Service != nil {
		component = spec.App.Package + "/" + spec.Facts.Service.Name
	}

	// Start the feature (the foreground service).
	if cfg.StartButtonText != "" {
		btn := cfg.StartButtonText
		steps = append(steps, Step{
			Caption: "2. The user starts " + work,
			Run:     func(ctx context.Context, d Driver) error { return d.TapText(ctx, btn) },
			HoldSec: 4,
		})
	} else if component != "" {
		steps = append(steps, Step{
			Caption: "2. The user starts " + work,
			Run: func(ctx context.Context, d Driver) error {
				return d.StartForegroundService(ctx, component, spec.StartAction)
			},
			HoldSec: 4,
		})
	}

	// Give it a real task (caller-injected; carries its own captions).
	steps = append(steps, cfg.TaskSteps...)

	// Prove the task is genuinely working.
	if cfg.ProgressText != "" {
		pt := cfg.ProgressText
		steps = append(steps, Step{
			Caption: "3. The task is doing real work — this can take minutes",
			Run:     func(ctx context.Context, d Driver) error { return d.WaitText(ctx, pt, progressWait) },
			HoldSec: 4,
		})
	} else {
		steps = append(steps, Step{Caption: "3. The task is doing real work — this can take minutes", HoldSec: 5})
	}

	// The foreground notification proves the process is kept alive.
	steps = append(steps, Step{
		Caption: "4. A foreground notification shows it running — Android keeps the process alive",
		Run:     func(ctx context.Context, d Driver) error { return d.ExpandNotifications(ctx) },
		HoldSec: 5,
	})

	// Background the app — the WHY.
	steps = append(steps, Step{
		Caption: "5. We leave the app. Without a foreground service Android would kill this mid-task and lose the work",
		Run: func(ctx context.Context, d Driver) error {
			_ = d.CollapseNotifications(ctx)
			return d.Home(ctx)
		},
		HoldSec: 5,
	})

	// Wait for completion in the background, then reveal the finished notification.
	if cfg.CompletionText != "" {
		ct := cfg.CompletionText
		steps = append(steps,
			Step{
				Caption: "6. The task keeps running in the background and finishes",
				Run:     func(ctx context.Context, d Driver) error { return d.WaitText(ctx, ct, doneWait) },
				HoldSec: 2,
			},
			Step{
				Caption: "7. A “task finished” notification confirms the work completed while backgrounded",
				Run:     func(ctx context.Context, d Driver) error { return d.ExpandNotifications(ctx) },
				HoldSec: 5,
			},
		)
	} else {
		steps = append(steps, Step{
			Caption: "6. The task keeps running while backgrounded and finishes",
			Run:     func(ctx context.Context, d Driver) error { return d.ExpandNotifications(ctx) },
			HoldSec: 5,
		})
	}

	// Stop — user is always in control.
	if cfg.StopButtonText != "" {
		btn := cfg.StopButtonText
		steps = append(steps, Step{
			Caption: "8. The user can stop the agent anytime — the service and notification end",
			Run: func(ctx context.Context, d Driver) error {
				_ = d.CollapseNotifications(ctx)
				return d.TapText(ctx, btn)
			},
			HoldSec: 4,
		})
	} else if component != "" {
		steps = append(steps, Step{
			Caption: "8. The user can stop the agent anytime — the service and notification end",
			Run: func(ctx context.Context, d Driver) error {
				_ = d.CollapseNotifications(ctx)
				return d.StopService(ctx, component)
			},
			HoldSec: 4,
		})
	}
	return steps
}

func appLabel(spec PermissionVideoSpec) string {
	if spec.App.Package != "" {
		return simpleClass(spec.App.Package)
	}
	return "the app"
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
		if spec.UseCase != nil {
			j = GenerateUseCaseJustification(spec.Facts, appName, *spec.UseCase)
		} else {
			j = GenerateJustification(spec.Facts, appName, whatRuns)
		}
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
	steps := PermissionProofSteps(spec)
	if spec.UseCase != nil {
		steps = UseCaseProofSteps(spec, *spec.UseCase)
	}
	mp4, cues, err := RunFlowRecording(ctx, surface, steps, spec.MaxSec)
	return mp4, cues, j, err
}
