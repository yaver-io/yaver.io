package main

// CLI: `yaver monorepo start` — interactive wizard that creates a
// new Yaver monorepo project. Mirrors the in-app sandbox flow at
// mobile/app/phone-projects.tsx so a project created here lands in
// the same place (~/.yaver/phone-projects/<slug>/) with the same
// on-disk shape, ready to vibe with claude / codex / opencode and
// importable from the mobile app's Phone Backend list.
//
// The five wizard steps mirror the mobile sandbox 1:1:
//   1. Name
//   2. Git (skip / configure)
//   3. Runner — claude / codex / opencode (mobile calls this "Who codes?")
//   4. Quick survey (6 questions, all skippable: platform, audience,
//      auth, persistence, theme, palette)
//   5. Description + logo URL + primary color hex (optional)
//
// All answers are concatenated into a single `Prompt` field on
// PhoneCreateSpec — schema/auth/seed are then drafted by
// generatePhoneProjectFromPrompt the same way the mobile flow does.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runMonorepoStart(args []string) {
	_ = args
	r := bufio.NewReader(os.Stdin)

	fmt.Println("Yaver Monorepo Start")
	fmt.Println("--------------------")
	fmt.Println("Same flow as the mobile sandbox. Press Enter to take the default for")
	fmt.Println("any question. Type 'skip' on the first survey question to skip them all.")
	fmt.Println()

	// Step 1 — Name
	fmt.Println("1. Name your app")
	fmt.Println("   You can rename it later.")
	name := promptRequired(r, "App name", "")
	slug := Slugify(name)
	if slug == "" {
		fmt.Fprintln(os.Stderr, "name must contain at least one alphanumeric char")
		os.Exit(1)
	}
	fmt.Println()

	// Step 2 — Git (optional)
	fmt.Println("2. Git (optional)")
	fmt.Println("   You can skip — Yaver works without git.")
	gitMode := promptChoice(r, "Git", []string{"skip", "configure"}, "skip")
	var gitProvider, repoVisibility, repoName string
	if gitMode == "configure" {
		gitProvider = promptChoice(r, "  Provider", []string{"github", "gitlab"}, "github")
		repoVisibility = promptChoice(r, "  Visibility", []string{"private", "public"}, "private")
		repoName = promptLine(r, "  Repo name", slug)
	}
	fmt.Println()

	// Step 3 — Coding location + runner. Yaver wraps Claude Code,
	// Codex, and OpenCode; the wizard probes each runner's install +
	// auth state on every machine connected to the user's account so
	// the user can pick the right wrapper on the right host. The
	// matrix mirrors what the mobile sandbox shows under
	// `activeRunnerDevice?.runners` — same data, different surface.
	fmt.Println("3. Who will code?")
	fmt.Println("   Yaver supports Claude Code, Codex, and OpenCode. Probing your machines…")
	locations := probeRunnerLocations()
	printRunnerLocationMatrix(locations)
	locID := promptChoice(r, "Where", runnerLocationIDs(locations), "this")
	chosenLoc := runnerLocationByID(locations, locID)
	runnerDflt := pickDefaultRunnerID(chosenLoc)
	runner := promptChoice(r, "Runner", []string{"claude", "codex", "opencode"}, runnerDflt)
	if !runMonorepoAuthInteractive(r, chosenLoc, runner) {
		fmt.Println("  Aborted — re-run `yaver monorepo start` after you've authenticated.")
		os.Exit(0)
	}
	fmt.Println()

	// Step 4 — Quick survey (skippable). Order + keys MUST match
	// SURVEY_QUESTIONS in mobile/app/phone-projects.tsx.
	fmt.Println("4. Quick survey (optional)")
	fmt.Println("   Six quick multiple-choice questions. Type 'skip' on the first to skip them all.")
	surveyQs := []surveyQuestion{
		{key: "platform", title: "Where will it run?", choices: []string{"web", "mobile", "both"}, dflt: "both"},
		{key: "audience", title: "Who's the user?", choices: []string{"myself", "friends", "customers", "public"}, dflt: "myself"},
		{key: "auth", title: "How do users sign in?", choices: []string{"none", "apple", "google", "email"}, dflt: "none"},
		{key: "persistence", title: "Save data between sessions?", choices: []string{"persist", "ephemeral"}, dflt: "persist"},
		{key: "theme", title: "Visual style?", choices: []string{"minimal", "playful", "professional"}, dflt: "minimal"},
		{key: "palette", title: "Color palette?", choices: []string{"slate", "zinc", "blue", "emerald", "rose", "amber", "violet", "neutral"}, dflt: "blue"},
	}
	answers := map[string]string{}
	skipped := false
	for i, q := range surveyQs {
		ans := promptLine(r, fmt.Sprintf("Q%d/%d %s [%s]", i+1, len(surveyQs), q.title, strings.Join(q.choices, "/")), q.dflt)
		if strings.EqualFold(ans, "skip") {
			skipped = true
			break
		}
		v := strings.ToLower(strings.TrimSpace(ans))
		if !sliceContains(q.choices, v) {
			fmt.Printf("  ! %q not in %v — using default %q\n", v, q.choices, q.dflt)
			v = q.dflt
		}
		answers[q.key] = v
	}
	fmt.Println()

	// Step 5 — Description + brand
	fmt.Println("5. Describe the app")
	fmt.Println("   What are you building? One paragraph is enough.")
	description := promptRequired(r, "Description", "")
	logoURL := promptLine(r, "Logo URL (optional, e.g. https://…/logo.png)", "")
	primaryHex := promptLine(r, "Primary color hex (optional, e.g. #0066ff)", "")
	fmt.Println()

	effectivePrompt := buildMonorepoEffectivePrompt(answers, skipped, logoURL, primaryHex, description, chosenLoc, runner)

	fmt.Println("Creating project on disk…")
	spec := PhoneCreateSpec{
		Name:   name,
		Slug:   slug,
		Prompt: effectivePrompt,
		Runner: runner,
	}
	proj, err := CreatePhoneProject(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Created %s\n", proj.Dir)

	// Symlink ~/Workspace/<slug> → proj.Dir so the user can `cd` into
	// it from their Workspace dir without leaving the sandbox layout
	// the mobile app already discovers. Best-effort — never fatal.
	if linkPath, ok := workspaceSymlinkFor(proj); ok {
		fmt.Printf("✓ Symlinked %s → %s\n", linkPath, proj.Dir)
	}

	if gitMode == "configure" {
		fmt.Println()
		fmt.Printf("(Note: git provisioning isn't wired into `yaver monorepo start` yet — you asked for")
		fmt.Printf(" a %s %s repo on %s. Run `git init && git remote add origin …` manually for now.)\n",
			repoVisibility, repoName, gitProvider)
	}

	fmt.Println()
	fmt.Println("Next:")
	fmt.Printf("  cd %s\n", proj.Dir)
	if chosenLoc != nil && chosenLoc.ID != "this" {
		fmt.Printf("  yaver code --runner %s --attach %s\n", runner, chosenLoc.ID)
	} else {
		fmt.Printf("  yaver code --runner %s\n", runner)
	}
	fmt.Println()
	fmt.Println("It's also already in your mobile sandbox — open the Yaver app → Phone Backend.")
}

// surveyQuestion mirrors one entry of SURVEY_QUESTIONS in the mobile
// wizard. The order of survey questions MUST stay in lockstep with
// mobile so a project created from either surface produces the same
// [Survey] block.
type surveyQuestion struct {
	key     string
	title   string
	choices []string
	dflt    string
}

// buildMonorepoEffectivePrompt mirrors the mobile sandbox's
// effectivePrompt build (mobile/app/phone-projects.tsx::create()).
// Survey + brand blocks get prepended to the user's free-text
// description so the LLM-driven generator sees the same structured
// blob whether the user came in via CLI or the in-app wizard.
//
// CLI-only addition: if the user picked a coding location + runner
// in step 3, that gets prepended as a [Coding] block so the LLM
// knows which wrapper will execute its plan and (if remote) which
// machine the project is going to be vibed on.
func buildMonorepoEffectivePrompt(answers map[string]string, skipped bool, logoURL, primaryHex, description string, loc *runnerLocation, runner string) string {
	parts := []string{}

	if !skipped && len(answers) > 0 {
		// Stable key order matches buildSurveyParagraph in mobile.
		ordered := []string{"platform", "audience", "auth", "persistence", "theme", "palette"}
		labels := map[string]string{
			"platform":    "Target",
			"audience":    "Users",
			"auth":        "Auth",
			"persistence": "Data",
			"theme":       "Style",
			"palette":     "Palette",
		}
		lines := []string{"[Survey]"}
		for _, k := range ordered {
			v, ok := answers[k]
			if !ok {
				continue
			}
			val := v
			switch {
			case k == "platform" && v == "both":
				val = "web + mobile"
			case k == "auth" && v == "none":
				val = "none / anonymous"
			case k == "persistence" && v == "persist":
				val = "persist between sessions"
			case k == "persistence" && v == "ephemeral":
				val = "ephemeral, no DB"
			}
			lines = append(lines, fmt.Sprintf("%s: %s", labels[k], val))
		}
		if len(lines) > 1 {
			parts = append(parts, strings.Join(lines, "\n")+"\n")
		}
	}

	brandLines := []string{}
	if strings.TrimSpace(logoURL) != "" {
		brandLines = append(brandLines, "Logo URL: "+strings.TrimSpace(logoURL))
	}
	if strings.TrimSpace(primaryHex) != "" {
		brandLines = append(brandLines, "Primary color: "+strings.TrimSpace(primaryHex))
	}
	if len(brandLines) > 0 {
		parts = append(parts, "[Brand]\n"+strings.Join(brandLines, "\n")+"\n")
	}

	if strings.TrimSpace(runner) != "" || loc != nil {
		codingLines := []string{"[Coding]"}
		if strings.TrimSpace(runner) != "" {
			codingLines = append(codingLines, "Runner: "+strings.TrimSpace(runner))
		}
		if loc != nil {
			where := loc.Label
			if loc.ID != "this" {
				where += " (remote: " + loc.ID + ")"
			}
			codingLines = append(codingLines, "Host: "+where)
		}
		if len(codingLines) > 1 {
			parts = append(parts, strings.Join(codingLines, "\n")+"\n")
		}
	}

	parts = append(parts, strings.TrimSpace(description))
	return strings.Join(parts, "\n")
}

// workspaceSymlinkFor creates ~/Workspace/<slug> → proj.Dir if
// ~/Workspace exists and the link path is free. Best-effort: returns
// (path, true) only when the symlink was actually created in this
// run. Existing path or any error → (path, false), no message printed
// here (caller decides whether to surface).
func workspaceSymlinkFor(proj *PhoneProject) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	wsRoot := filepath.Join(home, "Workspace")
	info, statErr := os.Stat(wsRoot)
	if statErr != nil || !info.IsDir() {
		return "", false
	}
	link := filepath.Join(wsRoot, proj.Slug)
	if _, lstatErr := os.Lstat(link); lstatErr == nil {
		// Already exists — leave alone.
		return link, false
	}
	if symErr := os.Symlink(proj.Dir, link); symErr != nil {
		return link, false
	}
	return link, true
}

func promptLine(r *bufio.Reader, label, dflt string) string {
	if dflt != "" {
		fmt.Printf("> %s [%s]: ", label, dflt)
	} else {
		fmt.Printf("> %s: ", label)
	}
	line, err := r.ReadString('\n')
	if err == io.EOF && line == "" {
		// stdin closed (Ctrl+D, redirected file ran out, etc.) — abort
		// rather than spin forever in a "Required" loop.
		fmt.Println()
		fmt.Fprintln(os.Stderr, "stdin closed — aborting")
		os.Exit(1)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return dflt
	}
	return line
}

func promptRequired(r *bufio.Reader, label, dflt string) string {
	for i := 0; i < 5; i++ {
		v := promptLine(r, label, dflt)
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		fmt.Println("  ! Required.")
	}
	fmt.Fprintln(os.Stderr, "too many empty answers — aborting")
	os.Exit(1)
	return ""
}

func promptChoice(r *bufio.Reader, label string, choices []string, dflt string) string {
	if !sliceContains(choices, dflt) && len(choices) > 0 {
		dflt = choices[0]
	}
	for {
		fmt.Printf("> %s [%s] (default: %s): ", label, strings.Join(choices, "/"), dflt)
		line, _ := r.ReadString('\n')
		v := strings.ToLower(strings.TrimSpace(line))
		if v == "" {
			return dflt
		}
		if sliceContains(choices, v) {
			return v
		}
		fmt.Printf("  ! pick one of: %s\n", strings.Join(choices, ", "))
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, x := range haystack {
		if x == needle {
			return true
		}
	}
	return false
}

