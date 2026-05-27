package main

// voice_launch.go — "launch <slug>" intent for hands-free agent loop.
//
// When the user says "launch sfmg" / "open carrotbet" / "start talos"
// over /voice/stream, we DON'T create a Claude Code task. Instead:
//
//   1. Parse the transcript for a launch verb + project slug
//   2. Resolve the slug against Config.Voice.LaunchProjects
//   3. HermesSmokeTest the project's bundle to catch crashes early
//   4. Trigger the existing Hermes-push to paired phones (best-effort)
//   5. Speak back a one-line confirmation: "launched sfmg" or the
//      smoke-test hint when the bundle is broken
//
// Falls through to the normal Claude-task path when:
//   - no launch verb matched
//   - the slug isn't configured in LaunchProjects
//
// Why a special path: shipping a build via voice is a discoverable
// shortcut. Routing through Claude every time means a 30s+ roundtrip
// just to invoke a deterministic action. This layer turns those into
// a 2-4s loop (smoke-test + fan-out).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// launchVerbPattern matches:
//   "launch sfmg" / "open sfmg" / "start sfmg" / "fire up sfmg" / "run sfmg"
//   optionally with leading filler ("ok", "hey yaver", "please")
//   optionally with trailing filler ("please", "on my phone")
var launchVerbPattern = regexp.MustCompile(`(?i)\b(launch|open|start|fire\s+up|run)\s+([a-z][a-z0-9_\-\.]{0,32})\b`)

// VoiceLaunchIntent describes a parsed launch instruction.
type VoiceLaunchIntent struct {
	Verb string // "launch" | "open" | "start" | "fire up" | "run"
	Slug string // normalized lowercase project slug
}

// LaunchIntentMatch returns the FIRST launch intent in the transcript,
// or nil if none. Trims fillers + normalizes case.
func LaunchIntentMatch(transcript string) *VoiceLaunchIntent {
	t := strings.TrimSpace(transcript)
	if t == "" {
		return nil
	}
	m := launchVerbPattern.FindStringSubmatch(t)
	if m == nil {
		return nil
	}
	return &VoiceLaunchIntent{
		Verb: strings.ToLower(strings.Join(strings.Fields(m[1]), " ")),
		Slug: strings.ToLower(m[2]),
	}
}

// VoiceLaunchResult is what we return for TTS readback.
type VoiceLaunchResult struct {
	OK             bool   `json:"ok"`
	Slug           string `json:"slug"`
	WorkDir        string `json:"workDir,omitempty"`
	SpokenResponse string `json:"spokenResponse"`
	Hint           string `json:"hint,omitempty"`
}

// VoiceLauncher is the callback that performs the actual launch action
// after the intent is validated + the bundle smoke-tested. The HTTP
// layer (voice_http.go) provides one that broadcasts the existing
// "open_app" command over the blackbox bus; tests inject a stub.
type VoiceLauncher func(workDir, slug string) error

// HandleVoiceLaunch executes the launch flow. Returns a result that the
// /voice/stream WS handler renders as TTS + display.
//
// The launcher callback is fired ASYNCHRONOUSLY after smoke-test
// passes — voice readback should not wait for the device install to
// complete (Hermes-push takes seconds; we want the user to hear
// "launching sfmg now" immediately).
func HandleVoiceLaunch(ctx context.Context, intent *VoiceLaunchIntent, cfg *Config, launcher VoiceLauncher) VoiceLaunchResult {
	if intent == nil {
		return VoiceLaunchResult{OK: false, SpokenResponse: "no launch intent"}
	}
	v := voiceCfgOrNil(cfg)
	if v == nil || len(v.LaunchProjects) == 0 {
		return VoiceLaunchResult{
			OK:             false,
			Slug:           intent.Slug,
			SpokenResponse: fmt.Sprintf("no launch projects configured. Add %q to voice.launch_projects.", intent.Slug),
			Hint:           "set voice.launch_projects in ~/.yaver/config.json",
		}
	}
	workDir, ok := v.LaunchProjects[intent.Slug]
	if !ok {
		// Try a fuzzy match — "sfmg" should match "sfmg-mobile"
		workDir = fuzzyMatchLaunchProject(intent.Slug, v.LaunchProjects)
	}
	if workDir == "" {
		known := make([]string, 0, len(v.LaunchProjects))
		for k := range v.LaunchProjects {
			known = append(known, k)
		}
		return VoiceLaunchResult{
			OK:             false,
			Slug:           intent.Slug,
			SpokenResponse: fmt.Sprintf("don't know %q. Known: %s", intent.Slug, strings.Join(known, ", ")),
			Hint:           "add the slug to voice.launch_projects",
		}
	}

	smoke := HermesSmokeTest(ctx, workDir)
	if !smoke.OK {
		return VoiceLaunchResult{
			OK:             false,
			Slug:           intent.Slug,
			WorkDir:        workDir,
			SpokenResponse: fmt.Sprintf("can't launch %s — %s", intent.Slug, smoke.Hint),
			Hint:           smoke.Hint,
		}
	}

	// Smoke passed → fire the launcher async so TTS readback isn't
	// blocked on the actual device install. The launcher does the
	// platform-specific work (Hermes-push, open_app broadcast).
	if launcher != nil {
		go func() {
			_ = launcher(workDir, intent.Slug)
		}()
	}

	return VoiceLaunchResult{
		OK:             true,
		Slug:           intent.Slug,
		WorkDir:        workDir,
		SpokenResponse: fmt.Sprintf("launching %s now.", intent.Slug),
	}
}

// fuzzyMatchLaunchProject finds the best slug-vs-key match. We accept:
//   - exact match
//   - key STARTS WITH the slug
//   - slug STARTS WITH the key
// Picks the shortest matching key for stability.
func fuzzyMatchLaunchProject(slug string, projects map[string]string) string {
	slug = strings.ToLower(slug)
	var best string
	for k, wd := range projects {
		lk := strings.ToLower(k)
		if lk == slug {
			return wd
		}
		if strings.HasPrefix(lk, slug) || strings.HasPrefix(slug, lk) {
			if best == "" || len(k) < len(best) {
				best = k
			}
		}
	}
	if best == "" {
		return ""
	}
	return projects[best]
}

// (no helper here — the HTTP layer provides the launcher via callback)
