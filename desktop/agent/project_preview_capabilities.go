package main

// project_preview_capabilities.go — ONE place that answers "what can I actually
// do with this project?", derived from detection rather than hardcoded per
// surface.
//
// Why this exists:
//
// The list of preview actions used to be assembled inside the mobile Projects
// screen, in TypeScript, with the rules inline — `isHermesMobileFramework(fw)`
// gates the Hermes buttons, a literal `fw === "swift" || fw === "kotlin"` gates
// the remote-runtime button, and so on. Three problems followed from that, and
// all three are the same problem:
//
//  1. EVERY OTHER SURFACE HAD TO REIMPLEMENT IT. Web, tvOS, glass/AR-VR and any
//     future surface each needed their own copy of the same conditionals, and
//     nothing kept the copies honest. Cross-surface parity is a house rule here
//     precisely because copies drift.
//
//  2. THE RULES WERE HARDCODED PER FRAMEWORK NAME. Adding a stack meant editing
//     a switch in each surface. Detection already knows what the project is;
//     the option list should FOLLOW from detection, not be maintained beside
//     it.
//
//  3. A UI-ONLY RULE IS NOT A RULE. The mobile screen could hide the Hermes
//     button while the endpoint happily served the same build to a caller that
//     didn't hide it — see the recursion guard in devserver_http.go.
//
// So: the agent detects, decides, and returns a list of options with support
// flags and reasons. Surfaces render what they are given. A surface may drop an
// option it cannot present, but it must never invent one.
//
// THE HARD RULE ENCODED HERE: Hermes is React Native / Expo ONLY. A Hermes
// bundle is JavaScript bytecode loaded into a React Native container — there is
// nothing for it to load in a Flutter, Kotlin, Swift or plain-web project. It
// must not merely be greyed out for those stacks; it must not appear.

import (
	"path/filepath"
	"strings"
)

// Preview option identifiers. These are contract with every surface — the
// mobile action sheet, the web dashboard, tvOS. Do not rename casually.
const (
	PreviewOptionHermes        = "compile-hermes"
	PreviewOptionOpenNative    = "open-native"
	PreviewOptionRemoteRuntime = "remote-runtime"
	PreviewOptionDevServer     = "dev-server"
	PreviewOptionWirePush      = "wire-push"
)

// ProjectPreviewOption is one thing the user can do with this project.
//
// Unsupported options are RETURNED, not omitted — when an option is one the
// user could reasonably expect, saying why it is unavailable beats it silently
// not existing. Options that make no sense for the stack at all (Hermes on
// Flutter) are omitted entirely; there is no useful "why" for something that
// was never applicable.
type ProjectPreviewOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Supported bool   `json:"supported"`
	Primary   bool   `json:"primary,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Framework string `json:"framework,omitempty"`
}

// ProjectPreviewCapabilities is the whole answer for one project.
type ProjectPreviewCapabilities struct {
	WorkDir string `json:"workDir,omitempty"`
	// Framework as DETECTED on disk — never taken from the caller. A surface
	// that guesses wrong would render options the project cannot support.
	Framework string `json:"framework"`
	// SelfDevelopment marks Yaver developing Yaver, which removes Hermes.
	SelfDevelopment bool                   `json:"selfDevelopment"`
	HasPairedDevice bool                   `json:"hasPairedDevice"`
	Options         []ProjectPreviewOption `json:"options"`
	Reason          string                 `json:"reason,omitempty"`
}

// hermesCapableFramework is the single source of truth for "can this stack load
// a Hermes bundle at all". Hermes bytecode is executed by a React Native
// runtime; nothing else can host it.
func hermesCapableFramework(framework string) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo", "react-native":
		return true
	default:
		return false
	}
}

// nativeMobileFramework is a stack that needs a real device/emulator runtime
// rather than a browser.
func nativeMobileFramework(framework string) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "swift", "kotlin":
		return true
	default:
		return false
	}
}

// browserRenderableFramework is a stack whose dev output a browser can render,
// so remote-runtime streaming / direct URL apply.
func browserRenderableFramework(framework string) bool {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "flutter", "nextjs", "vite", "react", "web", "astro", "remix":
		return true
	default:
		return false
	}
}

// DetectProjectPreviewCapabilities is the entry point every surface should use.
//
// framework is detected from disk when workDir is readable; the caller's hint
// is only a fallback for the case where the agent cannot see the project (a
// remote/unscanned path).
func DetectProjectPreviewCapabilities(workDir, frameworkHint string, hasPairedDevice bool) ProjectPreviewCapabilities {
	framework := ""
	if strings.TrimSpace(workDir) != "" {
		framework = detectFramework(workDir)
	}
	if framework == "" {
		framework = strings.ToLower(strings.TrimSpace(frameworkHint))
	}

	caps := ProjectPreviewCapabilities{
		WorkDir:         workDir,
		Framework:       framework,
		HasPairedDevice: hasPairedDevice,
		SelfDevelopment: IsYaverSelfDevelopmentDir(workDir) ||
			IsYaverSelfDevelopment(filepath.Base(strings.TrimSuffix(workDir, "/")), ""),
	}

	switch {
	// ── React Native / Expo — the only Hermes-capable stacks ─────────────
	case hermesCapableFramework(framework):
		if caps.SelfDevelopment {
			// Not "greyed out": Yaver-into-Yaver is a refusal, and the option
			// is replaced by the route that works.
			caps.Options = append(caps.Options, ProjectPreviewOption{
				ID: PreviewOptionRemoteRuntime, Label: "Stream over WebRTC",
				Supported: true, Primary: true, Framework: framework,
				Reason: "Yaver developing Yaver — the preview streams pixels so the escape stays in the phone's native chrome",
			})
			caps.Reason = "Yaver self-development: Hermes is withheld because loading Yaver into Yaver " +
				"puts two shake/exit owners in one React Native process and the preview could not be exited."
		} else {
			caps.Options = append(caps.Options,
				ProjectPreviewOption{
					ID: PreviewOptionOpenNative, Label: "Open in Yaver",
					Supported: hasPairedDevice, Primary: hasPairedDevice, Framework: framework,
					Reason: pairedDeviceReason(hasPairedDevice),
				},
				ProjectPreviewOption{
					ID: PreviewOptionHermes, Label: "Compile Hermes bundle",
					Supported: true, Framework: framework,
				},
				ProjectPreviewOption{
					ID: PreviewOptionRemoteRuntime, Label: "Stream over WebRTC",
					Supported: true, Primary: !hasPairedDevice, Framework: framework,
					Reason: "runs the RN web target on the box",
				},
			)
			caps.Reason = "React Native / Expo: a real device via Hermes is the most honest test; " +
				"WebRTC covers the web target when no device is paired."
		}
		caps.Options = append(caps.Options, ProjectPreviewOption{
			ID: PreviewOptionDevServer, Label: "Dev server", Supported: true, Framework: framework,
		})

	// ── Native mobile: Swift / Kotlin ────────────────────────────────────
	case nativeMobileFramework(framework):
		// NO Hermes entry at all — there is no React Native runtime here to
		// load bytecode into, so offering it (even disabled) is noise.
		caps.Options = append(caps.Options, ProjectPreviewOption{
			ID: PreviewOptionRemoteRuntime, Label: "Remote Runtime",
			Supported: true, Primary: true, Framework: framework,
			Reason: nativeRuntimeReason(framework),
		})
		caps.Options = append(caps.Options, ProjectPreviewOption{
			ID: PreviewOptionWirePush, Label: "Install on connected device",
			Supported: hasPairedDevice, Framework: framework,
			Reason: pairedDeviceReason(hasPairedDevice),
		})
		caps.Reason = "native " + framework + ": needs a real device or an emulator/simulator — " +
			"Hermes does not apply, there is no React Native runtime to load a bundle into."

	// ── Flutter and web stacks ───────────────────────────────────────────
	case browserRenderableFramework(framework):
		caps.Options = append(caps.Options,
			ProjectPreviewOption{
				ID: PreviewOptionDevServer, Label: "Dev server",
				Supported: true, Primary: true, Framework: framework,
			},
			ProjectPreviewOption{
				ID: PreviewOptionRemoteRuntime, Label: "Stream over WebRTC",
				Supported: true, Framework: framework,
				Reason: "for when the viewer cannot reach the dev server directly",
			},
		)
		caps.Reason = framework + " renders in a browser — the dev server is the lightest path; " +
			"Hermes does not apply."

	default:
		caps.Options = append(caps.Options, ProjectPreviewOption{
			ID: PreviewOptionDevServer, Label: "Dev server",
			Supported: true, Primary: true, Framework: framework,
		})
		caps.Reason = "unrecognised stack — offering the dev server only, rather than guessing at a runtime."
	}

	return caps
}

func pairedDeviceReason(paired bool) string {
	if paired {
		return ""
	}
	return "no paired device — connect one to use this"
}

func nativeRuntimeReason(framework string) string {
	switch strings.ToLower(framework) {
	case "swift":
		return "Apple UI needs an iOS simulator (macOS host) or a paired iPhone"
	case "kotlin":
		return "native Android needs an emulator/Redroid on the box, streamed over WebRTC"
	default:
		return "native app — needs a device or emulator runtime"
	}
}

// HermesOfferedFor reports whether any Hermes option would be shown. Surfaces
// and tests use this rather than re-deriving the rule.
func HermesOfferedFor(caps ProjectPreviewCapabilities) bool {
	for _, o := range caps.Options {
		if o.ID == PreviewOptionHermes || o.ID == PreviewOptionOpenNative {
			return true
		}
	}
	return false
}
