package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Recovery centralises the prompts we hand to the wrapped AI agent (Claude
// Code / Codex / Aider / …) when the user hits a friction point on a Yaver
// surface (mobile, web, desktop). Keeping them here — instead of hardcoded on
// every client — means:
//
//   1. Mobile / web / desktop all send the same recovery intent and the agent
//      decides which runner to use and what prompt to craft.
//   2. We can evolve the prompt wording without shipping a new mobile build.
//   3. The prompt always sees the real workdir, framework, and dev-machine OS
//      the agent already knows about, instead of the caller having to guess.
//
// The catch is that Yaver is LLM-agnostic — the prompt has to be runner
// neutral. Keep them declarative ("Fix X. Constraints: ...").

// RecoveryKind is the high-level intent the UI is asking for.
type RecoveryKind string

const (
	RecoveryHermesBuildFailed     RecoveryKind = "hermes-build-failed"
	RecoveryHermesCompatBlocked   RecoveryKind = "hermes-compat-blocked"
	RecoveryMetroNotStarting      RecoveryKind = "metro-not-starting"
	RecoveryFlutterFlushFailed    RecoveryKind = "flutter-flush-failed"
	RecoveryFlutterDeviceMissing  RecoveryKind = "flutter-device-missing"
	RecoverySwiftBuildFailed      RecoveryKind = "swift-build-failed"
	RecoverySwiftInstallFailed    RecoveryKind = "swift-install-failed"
	RecoveryKotlinBuildFailed     RecoveryKind = "kotlin-build-failed"
	RecoveryKotlinInstallFailed   RecoveryKind = "kotlin-install-failed"
	RecoveryApkDownloadFailed     RecoveryKind = "apk-download-failed"
	RecoveryMissingRuntime        RecoveryKind = "missing-runtime"
	RecoveryDepsInstallFailed     RecoveryKind = "deps-install-failed"
	RecoveryDevCompatMissingTools RecoveryKind = "dev-compat-missing-tools"
	RecoveryGeneric               RecoveryKind = "generic"
)

// RecoveryContext is everything the client can tell us about the situation.
// All fields are optional; the agent knows the rest.
type RecoveryContext struct {
	Kind      RecoveryKind `json:"kind"`
	Framework string       `json:"framework,omitempty"`
	WorkDir   string       `json:"workDir,omitempty"`
	Platform  string       `json:"platform,omitempty"` // "ios"/"android" etc, as the user's phone
	Project   string       `json:"project,omitempty"`
	Error     string       `json:"error,omitempty"`    // raw error text shown to the user
	Tool      string       `json:"tool,omitempty"`     // e.g. "node", "flutter", "xcode"
	Hint      string       `json:"hint,omitempty"`     // caller's best guess at what's wrong
	UserGoal  string       `json:"userGoal,omitempty"` // short human sentence
	Surface   string       `json:"surface,omitempty"`  // "mobile"/"web"/"desktop"

	// Compat is the structured Hermes compatibility report, present only for
	// RecoveryHermesCompatBlocked. It carries the exact incompatible modules,
	// version pairs, guest runtime, and family selection so the fix prompt can
	// name them precisely instead of pasting free-text error output.
	Compat *CompatReport `json:"compat,omitempty"`
}

// BuildRecoveryPrompt produces a single, runner-neutral prompt the AI agent
// can execute to resolve the user's problem. It never guesses a runner or
// model — that decision stays with the agent's task manager.
func BuildRecoveryPrompt(ctx RecoveryContext) (title, prompt string) {
	fw := strings.TrimSpace(ctx.Framework)
	wd := strings.TrimSpace(ctx.WorkDir)
	errText := strings.TrimSpace(ctx.Error)
	project := strings.TrimSpace(ctx.Project)
	if project == "" && wd != "" {
		project = basename(wd)
	}
	if project == "" {
		project = "the project"
	}
	hostOS := runtime.GOOS

	var (
		t string // title
		p string // prompt body
	)

	switch ctx.Kind {
	case RecoveryHermesBuildFailed:
		t = fmt.Sprintf("Fix Hermes build for %s", project)
		p = joinLines(
			fmt.Sprintf("The Hermes bundle build failed for %s (framework: %s).", project, fallback(fw, "react-native/expo")),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Figure out the root cause and fix it so `/dev/build-native` succeeds. Typical culprits:",
			"- Metro not starting (node missing, wrong node version, stale `.expo/`).",
			"- Runtime-family mismatch or Hermes bytecode drift between the guest app and the nearest Yaver host family (align the project's Expo / React Native / React versions to a supported host family).",
			"- Missing native dependencies after a prebuild reset.",
			"",
			"Do not run `expo run:ios`, `xcodebuild`, `gradlew`, or `expo run:android` — Yaver loads the app via Hermes push, so Metro + a fresh bundle is what matters.",
		)
	case RecoveryHermesCompatBlocked:
		t = fmt.Sprintf("Make %s Hermes-reloadable in Yaver", project)
		p = buildHermesCompatFixPrompt(project, fw, wd, hostOS, ctx.Compat, errText)
	case RecoveryMetroNotStarting:
		t = fmt.Sprintf("Fix Metro dev server for %s", project)
		p = joinLines(
			fmt.Sprintf("Metro (the JS bundler for %s) won't start on the dev machine.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Diagnose and fix. Check node / npm / expo CLI, and run `npm install` (or pnpm / yarn per lockfile) if dependencies look stale. Start Metro via the Yaver dev-server proxy — do NOT invoke `expo run:ios` or any native build. When Metro is up, the user will retry from the phone.",
		)
	case RecoveryFlutterFlushFailed:
		t = fmt.Sprintf("Fix Flutter flush for %s", project)
		p = joinLines(
			fmt.Sprintf("The Flutter LAN flush for %s failed.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Diagnose the root cause. Common causes on Flutter: `flutter` not installed / wrong channel; no real mobile device visible to `flutter devices`; iOS signing failing; Gradle not syncing.",
			"Fix it so `/dev/start?framework=flutter` produces a reload on the user's real phone. Do not rely on `--platform ios` as a device ID — resolve the actual phone id.",
		)
	case RecoveryFlutterDeviceMissing:
		t = fmt.Sprintf("Hook up Flutter device for %s", project)
		p = joinLines(
			fmt.Sprintf("`flutter devices` on the dev machine does not list the user's phone, so the LAN flush for %s has nothing to push to.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			"",
			"Figure out why: simulator / emulator not running, iOS trust prompt not accepted, USB unplugged, `adb devices` empty, wireless debugging expired. Fix it end-to-end so the phone shows up in `flutter devices --machine` and the Yaver Flutter dev server can target it.",
		)
	case RecoverySwiftBuildFailed:
		t = fmt.Sprintf("Fix Swift / Xcode build for %s", project)
		p = joinLines(
			fmt.Sprintf("The native iOS (Swift) build for %s failed on the dev machine.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s (Xcode builds require darwin)", hostOS),
			errText != "" && true,
			fmt.Sprintf("xcodebuild error reported to the user:\n%s", errText),
			"",
			"Diagnose and fix. Typical causes: Swift / Xcode version mismatch, broken pods, wrong SDK path, signing / provisioning profile missing or not matching the target device, `ENABLE_USER_SCRIPT_SANDBOXING` interactions, or corrupted DerivedData.",
			"Fix it so `yaver build start xcode-device-install` succeeds. If the dev machine is not macOS at all, stop and report that to the user — xcodebuild cannot run.",
		)
	case RecoverySwiftInstallFailed:
		t = fmt.Sprintf("Fix Swift device install for %s", project)
		p = joinLines(
			fmt.Sprintf("The Swift iOS build for %s succeeded, but installing the .ipa / .app on the user's iPhone failed.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			errText != "" && true,
			fmt.Sprintf("Install error reported to the user:\n%s", errText),
			"",
			"Diagnose: provisioning profile probably doesn't include this device UDID, or the device isn't trusted, or `xcrun devicectl` / `ios-deploy` can't see the phone. Fix the signing / provisioning so the install succeeds next run — do not rebuild from scratch if the archive is fine.",
		)
	case RecoveryKotlinBuildFailed:
		t = fmt.Sprintf("Fix Gradle Android build for %s", project)
		p = joinLines(
			fmt.Sprintf("The Kotlin / Android Gradle build for %s failed.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			errText != "" && true,
			fmt.Sprintf("Gradle error reported to the user:\n%s", errText),
			"",
			"Diagnose and fix. Typical causes: Java 17 not on PATH, Android SDK / ndk missing, wrong `compileSdk`, stale `.gradle/`, `keystore.properties` missing or wrong paths, a bad dependency lock.",
			"Fix it so `./gradlew :app:assembleRelease` (or `bundleRelease` for Play Store) produces an artifact. Report back what you changed.",
		)
	case RecoveryKotlinInstallFailed:
		t = fmt.Sprintf("Fix Android APK install for %s", project)
		p = joinLines(
			fmt.Sprintf("The Kotlin / Android APK for %s built, but installing it on the user's phone failed.", project),
			errText != "" && true,
			fmt.Sprintf("Install error reported to the user:\n%s", errText),
			"",
			"Walk the user through enabling \"Install unknown apps\" for Yaver if the OS refused, or fix the signing mismatch if the system is rejecting because a debug-signed APK conflicts with a previously installed release build. Uninstall the conflicting copy if needed.",
		)
	case RecoveryApkDownloadFailed:
		t = fmt.Sprintf("Fix APK download for %s", project)
		p = joinLines(
			fmt.Sprintf("Yaver built an APK for %s but the phone could not download it from the dev machine.", project),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Diagnose: relay down, LAN dropped, phone storage full, artifact path wrong, auth header rejected. Fix so `/builds/<id>/artifact` is reachable.",
		)
	case RecoveryMissingRuntime:
		tool := fallback(ctx.Tool, "the missing runtime")
		t = fmt.Sprintf("Install %s on the dev machine", tool)
		p = joinLines(
			fmt.Sprintf("The user tried to run %s and Yaver reported that `%s` is missing on the dev machine (OS: %s).", fallback(ctx.UserGoal, "a dev action"), tool, hostOS),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			"",
			fmt.Sprintf("Install %s via the user's package manager (brew on darwin, apt on debian/ubuntu, dnf on fedora, winget / scoop on windows) — do not install into `/usr/local` without sudo. Prefer Yaver's per-user runtime path (`~/.yaver/runtimes/%s`) so the user does not need sudo.", tool, tool),
			fmt.Sprintf("When %s is installed and on PATH, re-run the user's original action.", tool),
		)
	case RecoveryDepsInstallFailed:
		t = fmt.Sprintf("Install project deps for %s", project)
		p = joinLines(
			fmt.Sprintf("Dependency install failed for %s.", project),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Detect the package manager from the lockfile and run the right install. If a transitive dep is broken, pin it or bump the project. Don't touch the lockfile unless fixing a real issue.",
		)
	case RecoveryDevCompatMissingTools:
		missing := fallback(ctx.Hint, ctx.Tool)
		if missing == "" {
			missing = "(unspecified)"
		}
		t = fmt.Sprintf("Install missing dev tools: %s", missing)
		p = joinLines(
			fmt.Sprintf("Yaver's dev-compatibility check flagged missing tools on the dev machine: %s.", missing),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			"",
			"Install each missing tool, prefer per-user paths (no sudo if possible), and verify the dev server / compatibility check passes. Do not assume sudo is available.",
		)
	default: // RecoveryGeneric or unknown
		goal := fallback(ctx.UserGoal, "what the user was trying to do")
		t = fmt.Sprintf("Fix: %s", goal)
		p = joinLines(
			fmt.Sprintf("The user was trying to: %s", goal),
			fmt.Sprintf("Framework: %s", fallback(fw, "(unknown)")),
			fmt.Sprintf("Working directory: %s", fallback(wd, "(unknown)")),
			fmt.Sprintf("Dev machine OS: %s", hostOS),
			errText != "" && true,
			fmt.Sprintf("Error reported to the user:\n%s", errText),
			"",
			"Investigate and fix. Stay within this project's workdir. Do not run destructive commands.",
		)
	}

	if s := strings.TrimSpace(ctx.Hint); s != "" && ctx.Kind != RecoveryDevCompatMissingTools {
		p += "\n\nUser-provided hint: " + s
	}
	if s := strings.TrimSpace(ctx.Surface); s != "" {
		p += "\n\n(Triggered from Yaver " + s + ".)"
	}
	return t, p
}

// buildHermesCompatFixPrompt turns a structured CompatReport into a task the
// remote runner can execute to make a guest app Hermes-reloadable. It names the
// exact blocking modules and version pairs so the runner does not have to
// reverse-engineer them from free text, cites the talos Cell3D try/catch pattern
// as the gold standard for guarding an absent module, and forbids the two wrong
// fixes we keep seeing: bumping the shared host, or adding a heavy native dep.
//
// Precedence matches the gate: VERSION / framework / family drift is fatal and
// must be aligned; a MISSING module is warning-only and only needs a guard if
// its require is currently unguarded.
func buildHermesCompatFixPrompt(project, fw, wd, hostOS string, compat *CompatReport, errText string) string {
	lines := []string{
		fmt.Sprintf("The Yaver mobile host compiled a Hermes bundle for %s (%s) at %s, but blocked the on-phone reload because the guest app's native runtime contract does not match the host.",
			project, fallback(fw, "react-native/expo"), fallback(wd, "(unknown)")),
		fmt.Sprintf("Dev machine OS: %s.", hostOS),
		"",
	}

	if compat != nil && compat.RuntimeFamily != nil {
		lines = append(lines,
			fmt.Sprintf("Host runtime family: %s. Host supports: %s.",
				fallback(compat.RuntimeFamily.Selected.Label, "(nearest family)"),
				fallback(compat.RuntimeFamily.SupportedHint, "(see host manifest)")))
	}
	if compat != nil {
		g := compat.GuestRuntime
		if g.ExpoVersion != "" || g.ReactNativeVersion != "" || g.ReactVersion != "" {
			lines = append(lines, fmt.Sprintf("Guest runtime: Expo %s / React Native %s / React %s.",
				fallback(g.ExpoVersion, "?"), fallback(g.ReactNativeVersion, "?"), fallback(g.ReactVersion, "?")))
		}
	}
	lines = append(lines, "")

	// The fatal set — what actually blocked the load.
	fatal := []string{}
	if compat != nil {
		for _, m := range compat.VersionMismatches {
			fatal = append(fatal, fmt.Sprintf("- Native module `%s`: project %s vs host %s — %s",
				m.Name, fallback(m.ProjectVersion, "?"), fallback(m.HostVersion, "?"), fallback(m.Reason, "version boundary")))
		}
		if compat.ReactVersionMismatch != nil {
			fatal = append(fatal, fmt.Sprintf("- React: project %s vs host %s",
				compat.ReactVersionMismatch.ProjectVersion, compat.ReactVersionMismatch.HostVersion))
		}
		if compat.ExpoVersionMismatch != nil {
			fatal = append(fatal, fmt.Sprintf("- Expo: project %s vs host %s",
				compat.ExpoVersionMismatch.ProjectVersion, compat.ExpoVersionMismatch.HostVersion))
		}
		if compat.RNVersionMismatch != nil {
			fatal = append(fatal, fmt.Sprintf("- React Native: project %s vs host %s",
				compat.RNVersionMismatch.ProjectVersion, compat.RNVersionMismatch.HostVersion))
		}
	}
	if len(fatal) > 0 {
		lines = append(lines, "What blocked the load (FATAL — fix these):")
		lines = append(lines, fatal...)
		lines = append(lines, "")
	}

	if compat != nil && len(compat.Incompatible) > 0 {
		lines = append(lines,
			fmt.Sprintf("Warning-only (do NOT block on these, but guard any UNGUARDED usage): the following native modules are declared but not registered in the Yaver host: %s. They throw only if the app calls them at runtime; a require() wrapped in try/catch never reaches the throw.",
				strings.Join(compat.Incompatible, ", ")),
			"")
	}

	if len(fatal) == 0 && (compat == nil || len(compat.Incompatible) == 0) && errText != "" {
		lines = append(lines, "Error reported to the user:", errText, "")
	}

	lines = append(lines,
		fmt.Sprintf("Your job: make this app load into the Yaver Hermes host without changing what it does. Work ONLY inside %s. Choose the smallest change per issue:", fallback(wd, "the project directory")),
		"",
		"1. For each version/family mismatch above — align the GUEST DOWN to the host, never the host up. Pin the project's dependency to the host version shown (a package.json `overrides`/`resolutions` entry, plus Expo/RN/React set to the host family), then run the package manager once (detect it from the lockfile). Do NOT bump the Yaver host, and do NOT add a native module the host lacks — the host binary is fixed and shared across every user.",
		"",
		"2. For each unguarded native require()/import of an unsupported module — wrap it so a missing module renders a fallback instead of crashing. This is the gold-standard pattern already used in talos/mobile's src/screens/more/Cell3D.tsx and SpatialBackdrop.tsx: the expo-gl require is wrapped in try/catch and the component renders a graceful fallback when the module is absent, so the throw lands in the guest's own catch and never reaches the host. Mirror it exactly:",
		"     let NativeMod = null;",
		"     try { NativeMod = require('the-module'); } catch { NativeMod = null; }",
		"     // at the call site: if (!NativeMod) return <Fallback />;",
		"   Grep for every unguarded top-level import of the modules listed above and apply this. A guarded require of an absent module is fully allowed by the host — only an UNGUARDED top-level one is a risk.",
		"",
		"3. If a mismatch cannot be safely aligned (the app genuinely needs a native API the host does not provide and no JS fallback is possible), stop and report which module, why, and what the host would need — do not force it.",
		"",
		"Constraints: NO native builds (`expo run:ios`, `xcodebuild`, `gradlew`, `expo run:android`) — Yaver loads via Hermes push, so a fresh Metro bundle is all that matters. No destructive git. All changes stay inside the project directory; never touch the Yaver host repo.",
		"",
		"When done — re-trigger the reload to verify. Rebuild the Hermes bundle (POST /dev/build-native, or the mobile_project_build MCP verb) and confirm it returns status:ok, not a 409 compat block. If it still 409s, read the new nativeModuleVersionMismatches / runtimeFamilySelection in the response and iterate. Report the exact files changed and the final compat result.",
	)
	return joinLines(toIfaceLines(lines)...)
}

// toIfaceLines adapts a []string to joinLines's []interface{} signature.
func toIfaceLines(lines []string) []interface{} {
	out := make([]interface{}, len(lines))
	for i, l := range lines {
		out[i] = l
	}
	return out
}

func fallback(s, fb string) string {
	if strings.TrimSpace(s) == "" {
		return fb
	}
	return s
}

// joinLines accepts strings and `true` sentinels. A `true` means "include the
// next string"; a `false` means "skip the next string". It keeps the catalog
// readable without an if-ladder per kind.
func joinLines(parts ...interface{}) string {
	var out []string
	skipNext := false
	for _, p := range parts {
		switch v := p.(type) {
		case bool:
			skipNext = !v
		case string:
			if skipNext {
				skipNext = false
				continue
			}
			out = append(out, v)
		}
	}
	return strings.Join(out, "\n")
}

func basename(path string) string {
	path = strings.TrimRight(path, "/\\")
	if idx := strings.LastIndexAny(path, "/\\"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// ─── HTTP ───────────────────────────────────────────────────────────────

// handleRecover exposes POST /recover. Mobile / web / desktop clients send a
// RecoveryContext and get back the resulting task id. The agent picks the
// runner; the caller does not have to know which AI is wrapped.
func (s *HTTPServer) handleRecover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var ctx RecoveryContext
	if err := json.NewDecoder(r.Body).Decode(&ctx); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(string(ctx.Kind)) == "" {
		ctx.Kind = RecoveryGeneric
	}
	title, prompt := BuildRecoveryPrompt(ctx)

	workDir := strings.TrimSpace(ctx.WorkDir)
	if workDir == "" && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	taskOpts := TaskCreateOptions{WorkDir: strings.TrimSpace(ctx.WorkDir)}
	meta := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint:       "build",
		Title:          title,
		Description:    prompt,
		Source:         "recover",
		ProjectName:    ctx.Project,
		WorkDir:        workDir,
		TargetDeviceID: s.deviceID,
	})
	if previewPlacement, perr := s.previewTaskPlacement(r.Context(), meta); perr != nil {
		log.Printf("[placement] recover preview skipped before task create: %v", perr)
	} else if shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		pendingTaskID := newPendingCloudTaskID()
		recordedPlacement := previewPlacement
		if placement, rerr := s.recordTaskPlacement(r.Context(), pendingTaskID, meta); rerr != nil {
			log.Printf("[placement] recover pending record skipped for %s: %v", pendingTaskID, rerr)
		} else if placement != nil {
			recordedPlacement = placement
		}
		var activation map[string]any
		if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
			if result, aerr := s.activateTaskPlacement(r.Context(), recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
				activation = activationMapFromError(aerr)
				log.Printf("[placement] recover activation skipped for %s: %v", pendingTaskID, aerr)
			} else {
				activation = result
			}
		}
		bodyJSON, _ := json.Marshal(map[string]any{
			"title":         title,
			"description":   prompt,
			"source":        "recover",
			"workDir":       taskOpts.WorkDir,
			"projectName":   strings.TrimSpace(ctx.Project),
			"placementKind": meta.Kind,
		})
		cloudErr := &CloudWorkspaceRequiredError{
			PendingTaskID: pendingTaskID,
			Placement:     recordedPlacement,
			Activation:    activation,
			Reason:        "placement selected a Cloud Workspace for this recovery task",
		}
		authHeader := "Bearer " + strings.TrimSpace(s.token)
		if _, remoteTask, herr := createTaskOnCloudWorkspace(r.Context(), cloudErr, authHeader, bodyJSON, 20*time.Second); herr == nil && remoteTask != nil {
			targetDeviceID := ""
			if recordedPlacement != nil {
				targetDeviceID = recordedPlacement.TargetDeviceID
			}
			jsonReply(w, http.StatusAccepted, map[string]interface{}{
				"ok":             true,
				"mode":           "cloud_workspace",
				"taskId":         remoteTask.TaskID,
				"status":         remoteTask.Status,
				"pendingTaskId":  pendingTaskID,
				"targetDeviceId": targetDeviceID,
				"placement":      recordedPlacement,
				"title":          title,
			})
			return
		} else {
			reason := "Cloud Workspace is waking or needs attention before this recovery can run."
			if herr != nil {
				reason = herr.Error()
			}
			jsonReply(w, http.StatusConflict, map[string]interface{}{
				"ok":            false,
				"action":        "cloud_workspace_required",
				"pendingTaskId": pendingTaskID,
				"placement":     recordedPlacement,
				"activation":    activation,
				"reason":        reason,
				"title":         title,
			})
			return
		}
	} else if previewPlacement != nil {
		taskOpts.Placement = previewPlacement
	}

	task, err := s.taskMgr.CreateTaskWithOptions(title, prompt, "", "recover", "", "", nil, taskOpts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"title":  title,
	})
}
