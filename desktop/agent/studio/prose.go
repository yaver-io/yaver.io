package studio

import (
	"fmt"
	"strings"
)

// Justification is the reviewer-facing output for a permission: the two Play
// Console declaration fields plus a shot-list the video generator (later phase)
// turns into captioned scenes. For iOS permissions the same struct carries the
// App Review notes prose.
type Justification struct {
	// TaskOther is the one-liner for the Play "Other" task selection.
	TaskOther string
	// Description answers "Describe your app's use of this permission, including
	// why the task must start immediately and cannot be paused or restarted."
	Description string
	// ShotList is the ordered scene plan for the demo video (each line becomes
	// an on-screen caption in the permission-proof composite recipe).
	ShotList []string
	// Warnings surfaces manifest problems a reviewer would also catch
	// (declared-but-no-service, missing subtype, etc.).
	Warnings []string
}

// humanFGSType renders a foregroundServiceType token as a short noun phrase.
var humanFGSType = map[string]string{
	"specialUse":      "a special-use foreground task not covered by any standard type",
	"dataSync":        "data synchronization",
	"location":        "continuous location access",
	"camera":          "camera access while backgrounded",
	"microphone":      "microphone access while backgrounded",
	"mediaPlayback":   "background media playback",
	"connectedDevice": "communication with a connected device",
	"health":          "health/fitness tracking",
	"remoteMessaging": "remote messaging transport",
	"phoneCall":       "an ongoing phone/VoIP call",
	"mediaProjection": "screen capture / projection",
	"systemExempted":  "a system-exempted task",
	"fileManagement":  "long-running file management",
}

// GenerateJustification builds reviewer prose + a video shot-list from the
// analyzed facts. It is deterministic and offline; the description is a
// production-grade template the developer can edit. `whatRuns` is an optional
// one-clause description of the work the service performs (e.g. "an on-device
// coding agent and a local Linux environment"); when empty a neutral phrasing
// derived from the service class is used.
func GenerateJustification(facts *PermissionFacts, appName, whatRuns string) Justification {
	j := Justification{}
	if facts == nil {
		return j
	}
	appName = strings.TrimSpace(appName)
	if appName == "" {
		appName = "The app"
	}
	whatRuns = strings.TrimSpace(whatRuns)

	// Warnings the reviewer would also hit.
	if !facts.Declared {
		j.Warnings = append(j.Warnings, fmt.Sprintf("permission %s is not declared in <uses-permission>", facts.Permission))
	}
	if facts.FGSType != "" && facts.Service == nil {
		j.Warnings = append(j.Warnings, fmt.Sprintf("no <service> declares foregroundServiceType=%q for %s — Play will reject this; add or fix the service first", facts.FGSType, facts.Permission))
	}
	if facts.FGSType == "specialUse" && facts.Service != nil && facts.SpecialUseSubtype == "" {
		j.Warnings = append(j.Warnings, "the special-use service has no PROPERTY_SPECIAL_USE_FGS_SUBTYPE — Android 14+ wants a subtype declared")
	}

	serviceName := ""
	if facts.Service != nil {
		serviceName = facts.Service.Name
	}
	work := whatRuns
	if work == "" {
		if facts.SpecialUseSubtype != "" {
			work = "the task identified by the subtype \"" + facts.SpecialUseSubtype + "\""
		} else if serviceName != "" {
			work = "the long-running task implemented by " + simpleClass(serviceName)
		} else {
			work = "a long-running, user-started task"
		}
	}

	typePhrase := humanFGSType[facts.FGSType]
	if typePhrase == "" {
		typePhrase = "a foreground task"
	}

	switch facts.FGSType {
	case "specialUse":
		j.TaskOther = fmt.Sprintf("On-device tool: running %s, started and stopped by the user, with an ongoing notification.", work)
		j.Description = specialUseDescription(appName, work, serviceName, facts.SpecialUseSubtype)
		j.ShotList = []string{
			"1. User opens the feature and taps Start",
			"2. Foreground notification appears (the task is now running)",
			"3. User backgrounds the app — the task keeps running",
			"4. User returns and taps Stop — the task and its notification end",
		}
	default:
		j.TaskOther = fmt.Sprintf("%s for %s, started by the user with an ongoing notification.", strings.Title(typePhrase), appName)
		j.Description = genericFGSDescription(appName, typePhrase, work, serviceName, facts.FGSType)
		j.ShotList = []string{
			"1. User starts the feature that needs " + typePhrase,
			"2. Foreground notification appears",
			"3. App backgrounded — task continues uninterrupted",
			"4. User stops the feature; notification clears",
		}
	}
	return j
}

// GenerateUseCaseJustification builds prose + a shot-list for the NARRATIVE
// permission video (UseCaseProofSteps). Unlike GenerateJustification it names the
// real task, the proof-of-work, and the completion payoff — matching what the
// reviewer actually sees on screen, and arguing the necessity (Android would kill
// the process mid-task without the foreground service) rather than just toggling
// a notification.
func GenerateUseCaseJustification(facts *PermissionFacts, appName string, cfg UseCaseConfig) Justification {
	// Start from the deterministic base (warnings, type handling) so callers get
	// the same manifest checks, then override prose/shot-list with the narrative.
	j := GenerateJustification(facts, appName, cfg.WhatRuns)
	if facts == nil {
		return j
	}
	appName = strings.TrimSpace(appName)
	if appName == "" {
		appName = "The app"
	}
	work := strings.TrimSpace(cfg.WhatRuns)
	if work == "" {
		work = "a long-running, user-started task"
	}
	serviceName := ""
	if facts.Service != nil {
		serviceName = facts.Service.Name
	}

	j.TaskOther = fmt.Sprintf("On-device tool: the user starts %s; it runs to completion with an ongoing notification and a completion notification, and the user can stop it at any time.", work)
	j.Description = useCaseDescription(appName, work, serviceName, facts)

	progress := "the task shows live progress"
	if strings.TrimSpace(cfg.ProgressText) != "" {
		progress = fmt.Sprintf("the task shows live progress (\"%s\")", strings.TrimSpace(cfg.ProgressText))
	}
	done := "the user gets a “task finished” notification"
	if strings.TrimSpace(cfg.CompletionText) != "" {
		done = fmt.Sprintf("the user gets a “%s” notification", strings.TrimSpace(cfg.CompletionText))
	}
	j.ShotList = []string{
		fmt.Sprintf("1. User opens %s and starts %s", appName, work),
		fmt.Sprintf("2. The task begins real work — %s", progress),
		"3. The ongoing foreground notification shows the process is being kept alive",
		"4. User leaves the app — without the foreground service Android would kill the process and lose the in-flight work",
		"5. The task keeps running in the background and completes",
		fmt.Sprintf("6. %s while the app is backgrounded", done),
		"7. User can stop the task anytime — the service and notification end",
	}
	return j
}

func useCaseDescription(appName, work, serviceName string, facts *PermissionFacts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is an on-device tool. When the user explicitly starts a task, %s runs %s. ", appName, appName, work)
	b.WriteString("The work is stateful and can take minutes: it streams progress to the user through an ongoing notification, and posts a completion notification when it finishes.\n\n")

	b.WriteString("The task must run in a foreground service and cannot be paused, deferred, or restarted: it is a single, user-initiated session that holds in-flight state and live connections. ")
	b.WriteString("If the OS froze or killed the process when the user switched away — which Android does to ordinary background work within seconds — the running task would be lost and the user's work discarded. ")
	if serviceName != "" {
		fmt.Fprintf(&b, "The service is %s; ", simpleClass(serviceName))
	}
	b.WriteString("the foreground state and wake lock exist specifically so the user-started session survives while the app is backgrounded, and the user remains in control via the persistent notification and an in-app Stop control.\n\n")

	if facts.FGSType == "specialUse" {
		b.WriteString("This use case is not covered by any standard foreground service type (it is not media playback, location, data sync, camera, microphone, phone call, connected device, health, or remote messaging). ")
		if facts.SpecialUseSubtype != "" {
			fmt.Fprintf(&b, "It is declared as specialUse with the subtype \"%s\".", facts.SpecialUseSubtype)
		} else {
			b.WriteString("It is declared as specialUse.")
		}
	} else {
		fmt.Fprintf(&b, "It is declared with foregroundServiceType=%q.", facts.FGSType)
	}
	return b.String()
}

func specialUseDescription(appName, work, serviceName, subtype string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s starts a foreground service when the user explicitly enables the feature. ", appName)
	fmt.Fprintf(&b, "While the service runs, it performs %s; ", work)
	b.WriteString("the work is noticeable to the user via an ongoing notification, and the user starts and stops it manually.\n\n")

	b.WriteString("The task must run from the moment the user starts it and cannot be paused or restarted, because each session is a stateful, long-running operation: interrupting it freezes or kills the in-flight work and drops any live connections the app holds to it. ")
	if serviceName != "" {
		fmt.Fprintf(&b, "The service is %s; ", simpleClass(serviceName))
	}
	b.WriteString("the foreground state and wake lock exist specifically so the user-initiated session survives while the app is backgrounded.\n\n")

	b.WriteString("This use case is not covered by any of the standard foreground service types (it is not media playback, location, data sync, camera, microphone, phone call, connected device, health, or remote messaging). ")
	if subtype != "" {
		fmt.Fprintf(&b, "It is declared as specialUse with the subtype \"%s\".", subtype)
	} else {
		b.WriteString("It is declared as specialUse.")
	}
	return b.String()
}

func genericFGSDescription(appName, typePhrase, work, serviceName, fgsType string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s uses a foreground service of type %q for %s. ", appName, fgsType, typePhrase)
	fmt.Fprintf(&b, "The service runs %s and shows an ongoing notification while active; ", work)
	b.WriteString("it is started in response to a user action and stops when the user ends the task or the work completes.\n\n")
	b.WriteString("It runs in the foreground (rather than a background job) because the task must continue without interruption while the app is not in the foreground; pausing or deferring it would break the user-visible operation in progress.")
	if serviceName != "" {
		fmt.Fprintf(&b, " The service is %s.", simpleClass(serviceName))
	}
	return b.String()
}

func simpleClass(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// Markdown renders the justification as a paste-ready markdown block for the
// CLI/MCP output and the .justification.md artifact.
func (j Justification) Markdown(permission string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Permission justification — %s\n\n", permission)
	if len(j.Warnings) > 0 {
		b.WriteString("> **Fix these first — a reviewer will catch them too:**\n")
		for _, w := range j.Warnings {
			fmt.Fprintf(&b, "> - %s\n", w)
		}
		b.WriteString("\n")
	}
	b.WriteString("## \"What tasks require this permission?\" → Other\n\n")
	fmt.Fprintf(&b, "%s\n\n", j.TaskOther)
	b.WriteString("## \"Describe your app's use of this permission…\"\n\n")
	fmt.Fprintf(&b, "%s\n\n", j.Description)
	b.WriteString("## Demo video shot-list\n\n")
	for _, s := range j.ShotList {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return b.String()
}
