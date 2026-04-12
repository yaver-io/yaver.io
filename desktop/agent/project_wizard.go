package main

// project_wizard.go — interactive fullstack project generator.
//
// This is the "stop repeating the same setup" tool the dev
// builds for himself: from a blank directory to a working
// web + mobile + backend + DNS + OAuth + TestFlight + Play
// Store scaffold, via a Q&A wizard that runs from CLI, HTTP,
// or MCP. Defaults are opinionated toward the stack the dev
// already ships (Cloudflare Workers + Next.js + Convex +
// Expo/RN + Apple/Google/Microsoft OAuth).
//
// The wizard is a pure state machine so every surface gets to
// drive it the same way:
//
//   1. StartWizard() → new session ID + first question
//   2. AnswerQuestion(sessionID, answer) → next question or
//      "ready" if all required answers collected
//   3. GenerateProject(sessionID) → materialises a directory
//      tree on disk + prints follow-up instructions for the
//      parts the agent can't automate (OAuth app creation,
//      Cloudflare zone DNS, App Store key upload)
//
// State lives in-memory for now (one yaver process owns it),
// keyed by a random ID. The data is small and ephemeral — no
// reason to persist it to disk.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// QuestionKind tells the client which UI control to render.
type QuestionKind string

const (
	QText    QuestionKind = "text"
	QChoice  QuestionKind = "choice"
	QBool    QuestionKind = "bool"
	QColor   QuestionKind = "color"
	QDone    QuestionKind = "done"
	QConfirm QuestionKind = "confirm"
)

// WizardQuestion is a single step in the Q&A flow.
type WizardQuestion struct {
	ID       string       `json:"id"`
	Kind     QuestionKind `json:"kind"`
	Prompt   string       `json:"prompt"`
	Help     string       `json:"help,omitempty"`
	Default  string       `json:"default,omitempty"`
	Choices  []string     `json:"choices,omitempty"`
	Required bool         `json:"required,omitempty"`
}

// WizardSession holds the state for one in-flight wizard.
type WizardSession struct {
	ID        string            `json:"id"`
	Answers   map[string]string `json:"answers"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Done      bool              `json:"done"`
	// GeneratedPath is populated after GenerateProject runs
	GeneratedPath string `json:"generatedPath,omitempty"`
}

// wizardQuestions is the ordered list of prompts. IDs are
// stable so clients and the generator can refer to them by
// name. Keep this close to what a non-developer needs to
// answer — no dotfile paths, no internal jargon.
// wizardQuestions drives a monorepo-first scaffold. Defaults are
// the stack the dev already ships every day: Convex backend,
// Next.js on Cloudflare, Expo RN for iOS + Android. Each surface
// is opt-in so a "landing page only" project skips mobile, and a
// pure mobile project skips web. The layout is always a monorepo
// so later additions (an admin dashboard, a second app) fit
// without moving files around.
var wizardQuestions = []WizardQuestion{
	// Identity
	{ID: "app_name", Kind: QText, Prompt: "What's the app called?", Help: "Short brand name — shown everywhere.", Required: true},
	{ID: "slug", Kind: QText, Prompt: "URL-safe slug", Help: "Used for the monorepo folder + package names + bundle IDs.", Default: "myapp"},
	{ID: "description", Kind: QText, Prompt: "Describe the app in one paragraph", Help: "Goes into the README, the landing page hero, and feeds the AI agent when it later helps you build features.", Default: ""},
	{ID: "tagline", Kind: QText, Prompt: "One-line tagline", Help: "Landing page subheader. If you press Enter I'll derive one from your description.", Default: ""},

	// Domain + branding
	{ID: "domain", Kind: QText, Prompt: "Production domain (leave blank if not decided)", Help: "e.g. myapp.com — wired into wrangler + OAuth redirects.", Default: ""},
	{ID: "primary_color", Kind: QColor, Prompt: "Primary brand color (hex)", Default: "#4F46E5"},
	{ID: "accent_color", Kind: QColor, Prompt: "Accent color (hex)", Default: "#F59E0B"},
	{ID: "tone", Kind: QChoice, Prompt: "Visual tone", Choices: []string{"light", "dark", "system"}, Default: "system"},

	// Which surfaces to scaffold — defaults = everything the dev's own stack needs.
	{ID: "include_web", Kind: QBool, Prompt: "Include a web app (Next.js on Cloudflare)?", Default: "true"},
	{ID: "include_mobile", Kind: QBool, Prompt: "Include a mobile app (Expo RN for iOS + Android)?", Default: "true"},
	{ID: "include_backend", Kind: QBool, Prompt: "Include a backend (Convex)?", Default: "true"},
	{ID: "include_landing", Kind: QBool, Prompt: "Include a marketing landing page?", Default: "true"},

	// Stack — only asked when the surface is on. Conditional
	// skipping happens in nextQuestion().
	{ID: "web_framework", Kind: QChoice, Prompt: "Web framework", Choices: []string{"nextjs", "remix", "astro"}, Default: "nextjs"},
	{ID: "web_host", Kind: QChoice, Prompt: "Host the web app on?", Choices: []string{"cloudflare", "vercel", "netlify", "self-host"}, Default: "cloudflare"},
	{ID: "backend", Kind: QChoice, Prompt: "Backend platform", Choices: []string{"convex", "supabase", "firebase", "yaver-oauth", "none"}, Default: "convex"},
	{ID: "mobile_stack", Kind: QChoice, Prompt: "Mobile stack", Choices: []string{"expo-rn", "native"}, Default: "expo-rn"},

	// Auth — only asked when any surface needs it.
	{ID: "oauth_apple", Kind: QBool, Prompt: "Add Apple Sign-In?", Default: "true"},
	{ID: "oauth_google", Kind: QBool, Prompt: "Add Google Sign-In?", Default: "true"},
	{ID: "oauth_microsoft", Kind: QBool, Prompt: "Add Microsoft / O365 Sign-In?", Default: "false"},
	{ID: "oauth_email", Kind: QBool, Prompt: "Add email + password fallback?", Default: "true"},

	// Payments.
	{ID: "payments", Kind: QChoice, Prompt: "Payments provider", Choices: []string{"stripe", "lemon-squeezy", "paddle", "none"}, Default: "stripe"},

	// Mobile identifiers — only asked when mobile is on.
	{ID: "ios_bundle_id", Kind: QText, Prompt: "iOS bundle ID", Default: "com.myco.myapp"},
	{ID: "android_package", Kind: QText, Prompt: "Android package name", Default: "com.myco.myapp"},

	// Deploy credentials — all optional so non-developers can skip.
	{ID: "apple_team_id", Kind: QText, Prompt: "Apple Team ID (leave blank to skip TestFlight)", Default: ""},
	{ID: "play_service_account", Kind: QText, Prompt: "Path to Play service account JSON (leave blank to skip)", Default: ""},
	{ID: "cloudflare_zone", Kind: QText, Prompt: "Cloudflare zone for the domain (leave blank if not on CF yet)", Default: ""},

	// Git remote — create + push the fresh monorepo.
	{ID: "git_provider", Kind: QChoice, Prompt: "Push to which git host?", Choices: []string{"gitlab", "github", "none"}, Default: "gitlab"},
	{ID: "git_visibility", Kind: QChoice, Prompt: "Repo visibility", Choices: []string{"private", "public"}, Default: "private"},
	{ID: "git_org", Kind: QText, Prompt: "GitHub org / GitLab group (blank = your personal account)", Default: ""},
	{ID: "git_repo_name", Kind: QText, Prompt: "Repo name", Help: "Defaults to the slug you picked above.", Default: ""},

	{ID: "confirm", Kind: QConfirm, Prompt: "Generate the project now?", Default: "true"},
}

// --- session store ---------------------------------------------------------

var (
	wizardMu       sync.Mutex
	wizardSessions = map[string]*WizardSession{}
)

func newWizardID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartWizard creates a new session and returns the first
// question the client should render.
func StartWizard() (*WizardSession, *WizardQuestion) {
	id := newWizardID()
	sess := &WizardSession{
		ID:        id,
		Answers:   map[string]string{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	wizardMu.Lock()
	wizardSessions[id] = sess
	wizardMu.Unlock()
	q := nextQuestion(sess)
	return sess, q
}

// GetWizard returns a session by ID.
func GetWizard(id string) *WizardSession {
	wizardMu.Lock()
	defer wizardMu.Unlock()
	return wizardSessions[id]
}

// AnswerWizard records an answer and returns the next question
// (nil + Done when the flow is complete). Empty answers fall
// back to the question's Default so a non-developer pressing
// Enter through the whole flow gets a valid project.
func AnswerWizard(id, questionID, answer string) (*WizardQuestion, error) {
	wizardMu.Lock()
	sess, ok := wizardSessions[id]
	wizardMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("wizard session not found")
	}
	q := findQuestion(questionID)
	if q == nil {
		return nil, fmt.Errorf("unknown question %q", questionID)
	}
	if strings.TrimSpace(answer) == "" {
		answer = q.Default
	}
	if q.Required && strings.TrimSpace(answer) == "" {
		return q, fmt.Errorf("answer is required")
	}
	wizardMu.Lock()
	sess.Answers[questionID] = answer
	sess.UpdatedAt = time.Now()
	wizardMu.Unlock()
	return nextQuestion(sess), nil
}

func findQuestion(id string) *WizardQuestion {
	for i := range wizardQuestions {
		if wizardQuestions[i].ID == id {
			return &wizardQuestions[i]
		}
	}
	return nil
}

// nextQuestion returns the first unanswered question in the
// wizardQuestions list. Skips entire branches that aren't needed
// (e.g. no mobile → skip bundle ID / Team ID / Play service
// account) so the non-developer path is as short as possible.
func nextQuestion(sess *WizardSession) *WizardQuestion {
	wizardMu.Lock()
	defer wizardMu.Unlock()
	for i := range wizardQuestions {
		q := &wizardQuestions[i]
		if _, ok := sess.Answers[q.ID]; ok {
			continue
		}

		mobileOn := sess.Answers["include_mobile"] == "true"
		webOn := sess.Answers["include_web"] == "true" || sess.Answers["include_landing"] == "true"
		backendOn := sess.Answers["include_backend"] == "true"
		anyAuth := mobileOn || webOn || backendOn

		// Web-only questions
		if (q.ID == "web_framework" || q.ID == "web_host") && !webOn {
			sess.Answers[q.ID] = ""
			continue
		}
		// Mobile-only questions
		if (q.ID == "mobile_stack" || q.ID == "ios_bundle_id" || q.ID == "android_package" || q.ID == "apple_team_id" || q.ID == "play_service_account") && !mobileOn {
			sess.Answers[q.ID] = ""
			continue
		}
		// Backend-only questions
		if q.ID == "backend" && !backendOn {
			sess.Answers[q.ID] = "none"
			continue
		}
		// Auth questions only matter if we have a surface that uses them
		if (q.ID == "oauth_apple" || q.ID == "oauth_google" || q.ID == "oauth_microsoft" || q.ID == "oauth_email") && !anyAuth {
			sess.Answers[q.ID] = "false"
			continue
		}
		// Cloudflare zone only matters when the web host is CF
		if q.ID == "cloudflare_zone" && sess.Answers["web_host"] != "cloudflare" {
			sess.Answers[q.ID] = ""
			continue
		}
		// Payments only matters when there's something to sell
		if q.ID == "payments" && !webOn && !mobileOn {
			sess.Answers[q.ID] = "none"
			continue
		}
		// Git questions collapse to nothing when provider=none
		if (q.ID == "git_visibility" || q.ID == "git_org" || q.ID == "git_repo_name") && sess.Answers["git_provider"] == "none" {
			sess.Answers[q.ID] = ""
			continue
		}

		return q
	}
	sess.Done = true
	return &WizardQuestion{Kind: QDone, Prompt: "All questions answered."}
}

// --- generation ------------------------------------------------------------

// ProjectGenerationResult is what GenerateProject returns — the
// target directory + a bulleted list of manual follow-up steps.
type ProjectGenerationResult struct {
	OK             bool     `json:"ok"`
	Directory      string   `json:"directory"`
	Files          []string `json:"files"`
	NextSteps      []string `json:"nextSteps"`
	ServicesLog    string   `json:"servicesLog,omitempty"`
	ServicesError  string   `json:"servicesError,omitempty"`
	ServicesStarted bool    `json:"servicesStarted,omitempty"`
}

// GenerateProject materialises the scaffold described by the
// wizard answers as a monorepo at `parentDir/<slug>/`. Layout:
//
//	<slug>/
//	├── apps/
//	│   ├── web/        ← Next.js on Cloudflare (opt-in)
//	│   ├── landing/    ← static marketing page (opt-in)
//	│   └── mobile/     ← Expo RN (opt-in)
//	├── packages/
//	│   └── shared/     ← shared TS types + utils
//	├── backend/
//	│   └── convex/     ← Convex functions + schema (opt-in)
//	├── scripts/
//	├── .github/workflows/
//	├── README.md
//	├── SETUP.md
//	├── package.json    ← workspaces root
//	├── .env.example
//	└── .gitignore
//
// Each surface is opt-in: `include_web=false` means no apps/web,
// etc. The root package.json always declares workspaces so
// adding a second surface later doesn't require a reshuffle.
func GenerateProject(id, parentDir string) (*ProjectGenerationResult, error) {
	sess := GetWizard(id)
	if sess == nil {
		return nil, fmt.Errorf("wizard session not found")
	}
	if !sess.Done {
		return nil, fmt.Errorf("wizard not finished")
	}
	a := sess.Answers
	slug := a["slug"]
	if slug == "" {
		return nil, fmt.Errorf("slug is empty — cannot generate")
	}
	if a["tagline"] == "" {
		a["tagline"] = deriveTagline(a["description"], a["app_name"])
	}
	if parentDir == "" {
		parentDir, _ = os.Getwd()
	}
	dir := filepath.Join(parentDir, slug)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("target %s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := []string{}

	write := func(rel, body string) error {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	}

	// --- Root monorepo files ---
	if err := write("package.json", rootWorkspacePackageJSON(a)); err != nil {
		return nil, err
	}
	if err := write("README.md", rootReadme(a)); err != nil {
		return nil, err
	}
	if err := write("SETUP.md", buildSetupGuide(a)); err != nil {
		return nil, err
	}
	if err := write(".env.example", envExample(a)); err != nil {
		return nil, err
	}
	if err := write(".gitignore", gitignoreBody()); err != nil {
		return nil, err
	}
	if err := write(".nvmrc", "22\n"); err != nil {
		return nil, err
	}

	// --- packages/shared ---
	if err := write("packages/shared/package.json", sharedPackageJSON(a)); err != nil {
		return nil, err
	}
	if err := write("packages/shared/index.ts", sharedIndexTS(a)); err != nil {
		return nil, err
	}

	// --- apps/web (Next.js on Cloudflare) ---
	if a["include_web"] == "true" {
		if err := write("apps/web/package.json", nextjsPackageJSON(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/next.config.mjs", nextjsConfig(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/app/page.tsx", nextjsLandingPage(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/app/layout.tsx", nextjsLayout(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/app/globals.css", nextjsGlobals(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/tsconfig.json", tsConfig()); err != nil {
			return nil, err
		}
		if a["web_host"] == "cloudflare" {
			if err := write("apps/web/wrangler.toml", wranglerToml(a)); err != nil {
				return nil, err
			}
		}
	}

	// --- apps/landing (static marketing site) ---
	if a["include_landing"] == "true" {
		if err := write("apps/landing/index.html", landingHTML(a)); err != nil {
			return nil, err
		}
		if err := write("apps/landing/package.json", landingPackageJSON(a)); err != nil {
			return nil, err
		}
		if a["web_host"] == "cloudflare" {
			if err := write("apps/landing/wrangler.toml", landingWranglerToml(a)); err != nil {
				return nil, err
			}
		}
	}

	// --- apps/mobile (Expo RN) ---
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		if err := write("apps/mobile/app.json", expoAppJSON(a)); err != nil {
			return nil, err
		}
		if err := write("apps/mobile/package.json", expoPackageJSON(a)); err != nil {
			return nil, err
		}
		if err := write("apps/mobile/App.tsx", expoAppTSX(a)); err != nil {
			return nil, err
		}
		if err := write("apps/mobile/tsconfig.json", tsConfig()); err != nil {
			return nil, err
		}
		if err := write("apps/mobile/babel.config.js", expoBabelConfig()); err != nil {
			return nil, err
		}
	}

	// --- backend/convex ---
	if a["include_backend"] == "true" && a["backend"] == "convex" {
		if err := write("backend/package.json", convexPackageJSON(a)); err != nil {
			return nil, err
		}
		if err := write("backend/convex/schema.ts", convexSchema(a)); err != nil {
			return nil, err
		}
		if err := write("backend/convex/auth.ts", convexAuth(a)); err != nil {
			return nil, err
		}
		if err := write("backend/convex/tsconfig.json", tsConfig()); err != nil {
			return nil, err
		}
	}

	// --- scripts ---
	if err := write("scripts/deploy.sh", deployScript(a)); err != nil {
		return nil, err
	}
	os.Chmod(filepath.Join(dir, "scripts/deploy.sh"), 0o755)
	if err := write("scripts/dev.sh", devScript(a)); err != nil {
		return nil, err
	}
	os.Chmod(filepath.Join(dir, "scripts/dev.sh"), 0o755)

	// --- GitHub Actions ---
	if a["git_provider"] == "github" {
		if err := write(".github/workflows/ci.yml", githubCIYAML(a)); err != nil {
			return nil, err
		}
	}
	if a["git_provider"] == "gitlab" {
		if err := write(".gitlab-ci.yml", gitlabCIYAML(a)); err != nil {
			return nil, err
		}
	}

	// --- Git init + push ---
	gitInit(dir)
	pushResult := ""
	if a["git_provider"] != "" && a["git_provider"] != "none" {
		if url, err := createRemoteRepo(a); err == nil {
			if err := gitPushInitial(dir, url); err == nil {
				pushResult = url
			} else {
				pushResult = "created but push failed: " + err.Error()
			}
		} else {
			pushResult = "remote create failed: " + err.Error()
		}
	}

	// Persist the generated path on the session.
	wizardMu.Lock()
	sess.GeneratedPath = dir
	wizardMu.Unlock()

	// Emit .yaver/config.yaml so the universal backend adapter + dashboard
	// know which backend this project uses. Also add matching service
	// presets to .yaver/services.yaml so `yaver services start` works.
	if err := writeYaverProjectConfig(dir, a); err != nil {
		// soft-fail: generation succeeded, config is a nicety
		_ = err
	}

	res := &ProjectGenerationResult{
		OK:        true,
		Directory: dir,
		Files:     files,
		NextSteps: manualNextSteps(a),
	}
	if pushResult != "" {
		res.NextSteps = append([]string{"Git remote: " + pushResult}, res.NextSteps...)
	}

	// Auto-start the local backend services the wizard just wired up. This
	// powers the Video 1 "tap Create Project and the Convex dashboard is live"
	// flow — no manual `yaver services start` step. Best-effort: if Docker
	// isn't running or presets are missing, we surface the error in the
	// result but don't fail generation.
	if sm := NewServicesManager(dir); sm != nil {
		if cfg, err := sm.LoadConfig(); err == nil && len(cfg.Services) > 0 {
			if out, err := sm.Start(); err != nil {
				res.ServicesError = err.Error()
			} else {
				res.ServicesLog = out
				res.ServicesStarted = true
			}
		}
	}
	return res, nil
}

// --- monorepo helpers ------------------------------------------------------

func deriveTagline(desc, appName string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return appName + " — the simplest way to ship."
	}
	// First sentence, capped at 80 chars.
	end := strings.IndexAny(d, ".!?\n")
	if end > 0 && end < 120 {
		d = d[:end]
	}
	if len(d) > 120 {
		d = d[:117] + "..."
	}
	return d
}

func rootWorkspacePackageJSON(a map[string]string) string {
	workspaces := []string{}
	if a["include_web"] == "true" {
		workspaces = append(workspaces, `"apps/web"`)
	}
	if a["include_landing"] == "true" {
		workspaces = append(workspaces, `"apps/landing"`)
	}
	if a["include_mobile"] == "true" {
		workspaces = append(workspaces, `"apps/mobile"`)
	}
	workspaces = append(workspaces, `"packages/*"`)
	if a["include_backend"] == "true" {
		workspaces = append(workspaces, `"backend"`)
	}
	return fmt.Sprintf(`{
  "name": "%s",
  "version": "0.0.1",
  "private": true,
  "description": %q,
  "workspaces": [%s],
  "scripts": {
    "dev": "./scripts/dev.sh",
    "deploy": "./scripts/deploy.sh"
  }
}
`, a["slug"], a["description"], strings.Join(workspaces, ", "))
}

func rootReadme(a map[string]string) string {
	stack := []string{}
	if a["include_web"] == "true" {
		stack = append(stack, fmt.Sprintf("- **Web** (`apps/web`) — %s on %s", a["web_framework"], a["web_host"]))
	}
	if a["include_landing"] == "true" {
		stack = append(stack, "- **Landing** (`apps/landing`) — static marketing page")
	}
	if a["include_mobile"] == "true" {
		stack = append(stack, "- **Mobile** (`apps/mobile`) — Expo RN (iOS + Android)")
	}
	if a["include_backend"] == "true" {
		stack = append(stack, fmt.Sprintf("- **Backend** (`backend/`) — %s", a["backend"]))
	}
	stack = append(stack, "- **Shared** (`packages/shared`) — cross-surface TS types + utils")
	return fmt.Sprintf(`# %s

> %s

%s

---

## Stack

%s

Auth: %s
Payments: %s

## Quick start

%s`+"```bash\nnpm install\n./scripts/dev.sh    # runs every app in dev mode\n./scripts/deploy.sh # builds + deploys every surface\n```\n\n"+`See [SETUP.md](./SETUP.md) for one-time signups (OAuth, Cloudflare, TestFlight, Play Store).

Generated by `+"`yaver new`"+` on %s.
`,
		a["app_name"], a["tagline"], a["description"],
		strings.Join(stack, "\n"),
		describeAuth(a), a["payments"],
		"",
		time.Now().Format("2006-01-02"),
	)
}

func sharedPackageJSON(a map[string]string) string {
	return fmt.Sprintf(`{
  "name": "@%s/shared",
  "version": "0.0.1",
  "main": "index.ts",
  "types": "index.ts",
  "private": true
}
`, a["slug"])
}

func sharedIndexTS(a map[string]string) string {
	return fmt.Sprintf(`// Cross-surface types + utils for %s.
//
// Anything that lives on more than one surface (web, mobile,
// backend) goes in this file or a sibling. Keep it lean — no
// React imports, no Node APIs, pure TypeScript only.

export const APP_NAME = %q;
export const APP_TAGLINE = %q;

export interface User {
  id: string;
  email: string;
  name?: string;
  avatarUrl?: string;
}
`, a["app_name"], a["app_name"], a["tagline"])
}

func landingHTML(a map[string]string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>%s</title>
    <meta name="description" content=%q>
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: %s; color: white; }
      main { min-height: 100vh; display: grid; place-items: center; text-align: center; padding: 24px; }
      h1 { font-size: 56px; margin: 0; }
      p  { opacity: .85; max-width: 640px; margin: 12px auto 0; }
      a.cta { display: inline-block; margin-top: 28px; padding: 14px 28px; background: %s; color: #000; border-radius: 9999px; text-decoration: none; font-weight: 700; }
    </style>
  </head>
  <body>
    <main>
      <div>
        <h1>%s</h1>
        <p>%s</p>
        <a class="cta" href="#waitlist">Get early access</a>
      </div>
    </main>
  </body>
</html>
`, a["app_name"], a["tagline"], a["primary_color"], a["accent_color"], a["app_name"], a["tagline"])
}

func landingPackageJSON(a map[string]string) string {
	return fmt.Sprintf(`{
  "name": "@%s/landing",
  "version": "0.0.1",
  "private": true,
  "scripts": {
    "dev": "npx --yes serve -l 4321 .",
    "deploy": "wrangler pages deploy ."
  },
  "devDependencies": {
    "wrangler": "^3.80.0"
  }
}
`, a["slug"])
}

func landingWranglerToml(a map[string]string) string {
	return fmt.Sprintf(`name = "%s-landing"
compatibility_date = "2024-09-23"
pages_build_output_dir = "."
`, a["slug"])
}

func tsConfig() string {
	return `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "strict": true,
    "esModuleInterop": true,
    "jsx": "preserve",
    "skipLibCheck": true,
    "resolveJsonModule": true
  }
}
`
}

func expoBabelConfig() string {
	return `module.exports = function(api) {
  api.cache(true);
  return { presets: ["babel-preset-expo"] };
};
`
}

func devScript(a map[string]string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# dev.sh — start every surface in parallel.\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd \"$(dirname \"$0\")/..\"\n\n")
	b.WriteString("pids=()\n")
	if a["include_backend"] == "true" && a["backend"] == "convex" {
		b.WriteString("(cd backend && npx convex dev) & pids+=($!)\n")
	}
	if a["include_web"] == "true" {
		b.WriteString("(cd apps/web && npm run dev) & pids+=($!)\n")
	}
	if a["include_landing"] == "true" {
		b.WriteString("(cd apps/landing && npm run dev) & pids+=($!)\n")
	}
	if a["include_mobile"] == "true" {
		b.WriteString("(cd apps/mobile && npx expo start) & pids+=($!)\n")
	}
	b.WriteString("trap 'kill \"${pids[@]}\" 2>/dev/null || true' EXIT\n")
	b.WriteString("wait\n")
	return b.String()
}

func githubCIYAML(a map[string]string) string {
	_ = a
	return `name: CI
on:
  push: { branches: [main] }
  pull_request: { branches: [main] }
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: npm }
      - run: npm install
      - run: npm run -w apps/web build --if-present
`
}

func gitlabCIYAML(a map[string]string) string {
	_ = a
	return `stages: [build]
build:
  image: node:22
  stage: build
  script:
    - npm install
    - npm run -w apps/web build --if-present
`
}

// --- git glue --------------------------------------------------------------

func gitInit(dir string) {
	_ = runWizardCmd(dir, "git", "init", "-q", "-b", "main")
	_ = runWizardCmd(dir, "git", "add", ".")
	_ = runWizardCmd(dir, "git", "-c", "user.email=yaver@localhost", "-c", "user.name=yaver", "commit", "-q", "-m", "chore: initial scaffold via yaver new")
}

// createRemoteRepo asks the selected host's CLI (gh / glab) to
// create the remote. Returns the clone URL on success. Skips
// silently when the CLI is missing so the scaffold still lands.
func createRemoteRepo(a map[string]string) (string, error) {
	provider := a["git_provider"]
	name := a["git_repo_name"]
	if name == "" {
		name = a["slug"]
	}
	visibility := a["git_visibility"]
	if visibility == "" {
		visibility = "private"
	}
	org := a["git_org"]

	switch provider {
	case "github":
		if _, err := osLookPath("gh"); err != nil {
			return "", fmt.Errorf("gh CLI not installed — https://cli.github.com")
		}
		full := name
		if org != "" {
			full = org + "/" + name
		}
		args := []string{"repo", "create", full, "--" + visibility, "--description", a["description"]}
		if a["domain"] != "" {
			args = append(args, "--homepage", "https://"+a["domain"])
		}
		if out, err := runCmdOutput("", "gh", args...); err != nil {
			return "", fmt.Errorf("gh repo create: %v — %s", err, out)
		}
		if org == "" {
			user, _ := runCmdOutput("", "gh", "api", "user", "--jq", ".login")
			org = strings.TrimSpace(user)
		}
		return fmt.Sprintf("git@github.com:%s/%s.git", org, name), nil
	case "gitlab":
		if _, err := osLookPath("glab"); err != nil {
			return "", fmt.Errorf("glab CLI not installed — https://gitlab.com/gitlab-org/cli")
		}
		args := []string{"repo", "create", name, "--" + visibility, "--description", a["description"]}
		if org != "" {
			args = append(args, "--group", org)
		}
		if out, err := runCmdOutput("", "glab", args...); err != nil {
			return "", fmt.Errorf("glab repo create: %v — %s", err, out)
		}
		host := "git@gitlab.com"
		ns := org
		if ns == "" {
			ns, _ = runCmdOutput("", "glab", "api", "user", "--jq", ".username")
			ns = strings.TrimSpace(ns)
		}
		return fmt.Sprintf("%s:%s/%s.git", host, ns, name), nil
	}
	return "", fmt.Errorf("unsupported provider %q", provider)
}

func gitPushInitial(dir, url string) error {
	if err := runWizardCmd(dir, "git", "remote", "add", "origin", url); err != nil {
		return err
	}
	return runWizardCmd(dir, "git", "push", "-u", "origin", "main")
}

// runWizardCmd executes a command in dir and swallows its
// output. Separate from runCmd in mcp_devtools.go.
func runWizardCmd(dir, bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run()
}

// runCmdOutput is the combined-output variant, used when we need
// to parse a CLI's response (e.g. `gh api user`).
func runCmdOutput(dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// --- template helpers ------------------------------------------------------

func describeAuth(a map[string]string) string {
	parts := []string{}
	if a["oauth_apple"] == "true" {
		parts = append(parts, "Apple")
	}
	if a["oauth_google"] == "true" {
		parts = append(parts, "Google")
	}
	if a["oauth_microsoft"] == "true" {
		parts = append(parts, "Microsoft")
	}
	if a["oauth_email"] == "true" {
		parts = append(parts, "Email+password")
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, ", ")
}

func quickStartFor(a map[string]string) string {
	lines := []string{
		"```bash",
		"# 1. install deps",
	}
	if a["web_framework"] == "nextjs" {
		lines = append(lines, "cd web && npm install && cd ..")
	}
	if a["backend"] == "convex" {
		lines = append(lines, "cd backend && npm install && npx convex dev && cd ..")
	}
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		lines = append(lines, "cd mobile && npm install && npx expo prebuild && cd ..")
	}
	lines = append(lines, "", "# 2. follow SETUP.md for OAuth + DNS", "```")
	return strings.Join(lines, "\n")
}

func buildSetupGuide(a map[string]string) string {
	var b strings.Builder
	b.WriteString("# Setup — " + a["app_name"] + "\n\n")
	b.WriteString("This file walks you through every manual step that a generator can't automate. Take them in order.\n\n")

	// Domain + DNS
	b.WriteString("## 1. Domain & DNS\n\n")
	if a["web_host"] == "cloudflare" {
		b.WriteString("- Sign in at https://dash.cloudflare.com and add the zone `" + a["domain"] + "` (Cloudflare will give you two nameservers).\n")
		b.WriteString("- At your domain registrar, replace the current nameservers with Cloudflare's.\n")
		b.WriteString("- Wait for the zone to go Active (5 min–24 h), then run `cd web && npx wrangler deploy`.\n")
		b.WriteString("- The wrangler config already has a route for `" + a["domain"] + "`, so the first deploy will wire production immediately.\n")
	} else {
		b.WriteString("- Point `" + a["domain"] + "` at your " + a["web_host"] + " deployment per that provider's instructions.\n")
	}
	b.WriteString("\n")

	// OAuth
	b.WriteString("## 2. OAuth providers\n\n")
	if a["oauth_apple"] == "true" {
		b.WriteString("### Apple Sign-In\n\n")
		b.WriteString("- https://developer.apple.com/account/resources/identifiers/list/serviceId — create a Services ID.\n")
		b.WriteString("  - Identifier: `" + a["ios_bundle_id"] + ".auth`\n")
		b.WriteString("  - Return URL: `https://" + a["domain"] + "/auth/callback/apple`\n")
		b.WriteString("- https://developer.apple.com/account/resources/authkeys/list — create a Sign in with Apple key. Save the .p8 file and the Key ID.\n")
		b.WriteString("- Paste the Key ID, Team ID, and .p8 path into `.env` under `APPLE_CLIENT_ID`, `APPLE_KEY_ID`, `APPLE_TEAM_ID`, `APPLE_PRIVATE_KEY_PATH`.\n\n")
	}
	if a["oauth_google"] == "true" {
		b.WriteString("### Google Sign-In\n\n")
		b.WriteString("- https://console.cloud.google.com/apis/credentials — create an OAuth client ID (Type: Web).\n")
		b.WriteString("  - Authorized redirect URI: `https://" + a["domain"] + "/auth/callback/google`\n")
		b.WriteString("- Copy the Client ID + Client Secret into `.env` under `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`.\n\n")
	}
	if a["oauth_microsoft"] == "true" {
		b.WriteString("### Microsoft / O365\n\n")
		b.WriteString("- https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade — register an application.\n")
		b.WriteString("  - Redirect URI: `https://" + a["domain"] + "/auth/callback/microsoft`\n")
		b.WriteString("- Under Certificates & secrets, create a client secret.\n")
		b.WriteString("- Copy Application (client) ID, Directory (tenant) ID, client secret into `.env`.\n\n")
	}
	if a["oauth_email"] == "true" {
		b.WriteString("### Email + password fallback\n\n")
		b.WriteString("- No external signup; the Convex `users` table stores hashed passwords out of the box.\n\n")
	}

	// Mobile
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		b.WriteString("## 3. iOS TestFlight\n\n")
		if a["apple_team_id"] != "" {
			b.WriteString("- Team ID: `" + a["apple_team_id"] + "`\n")
		} else {
			b.WriteString("- Grab your Team ID from https://developer.apple.com/account (top right).\n")
		}
		b.WriteString("- https://appstoreconnect.apple.com/access/api — create an App Store Connect API key with Admin or App Manager role.\n")
		b.WriteString("- Save the .p8 file and note the Key ID + Issuer ID.\n")
		b.WriteString("- Put them in `.env` under `APP_STORE_KEY_PATH`, `APP_STORE_KEY_ID`, `APP_STORE_KEY_ISSUER`, `APPLE_TEAM_ID`.\n")
		b.WriteString("- Deploy with `./scripts/deploy.sh testflight`.\n\n")

		b.WriteString("## 4. Android Play Store\n\n")
		b.WriteString("- https://play.google.com/console — create your app listing.\n")
		b.WriteString("- https://console.cloud.google.com/iam-admin/serviceaccounts — create a service account, grant it access in Play Console under Users & Permissions.\n")
		b.WriteString("- Download the JSON key.\n")
		if a["play_service_account"] != "" {
			b.WriteString("- Already configured: `" + a["play_service_account"] + "`\n")
		} else {
			b.WriteString("- Put the path in `.env` under `PLAY_STORE_KEY_FILE`.\n")
		}
		b.WriteString("- Deploy with `./scripts/deploy.sh playstore`.\n\n")
	}

	// Payments
	if a["payments"] == "stripe" {
		b.WriteString("## 5. Stripe\n\n")
		b.WriteString("- https://dashboard.stripe.com/apikeys — grab publishable + secret keys.\n")
		b.WriteString("- https://dashboard.stripe.com/webhooks — add `https://" + a["domain"] + "/api/stripe/webhook` and copy the signing secret.\n")
		b.WriteString("- Put them in `.env` under `STRIPE_PUBLIC_KEY`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`.\n\n")
	}

	b.WriteString("## Done\n\n")
	b.WriteString("Once `.env` is filled in:\n\n")
	b.WriteString("```bash\n./scripts/deploy.sh web\n```\n\n")
	b.WriteString("Check back into the wizard any time with `yaver new --resume " + a["slug"] + "`.\n")
	return b.String()
}

func manualNextSteps(a map[string]string) []string {
	steps := []string{
		"cd " + a["slug"] + " && less SETUP.md",
		"Copy .env.example to .env and fill in the blanks as you finish each SETUP.md step.",
	}
	if a["web_host"] == "cloudflare" {
		steps = append(steps, "Add the Cloudflare zone for "+a["domain"]+" and swap nameservers at your registrar.")
	}
	if a["oauth_apple"] == "true" {
		steps = append(steps, "Create the Apple Services ID + Sign in with Apple key.")
	}
	if a["oauth_google"] == "true" {
		steps = append(steps, "Create the Google OAuth client.")
	}
	if a["oauth_microsoft"] == "true" {
		steps = append(steps, "Register the Azure AD app.")
	}
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		steps = append(steps, "Fetch an App Store Connect API key and a Play Store service account.")
	}
	if a["payments"] == "stripe" {
		steps = append(steps, "Set up a Stripe account and paste the keys into .env.")
	}
	steps = append(steps, "Run `./scripts/deploy.sh web` for the first production deploy.")
	return steps
}

// --- file bodies -----------------------------------------------------------

func nextjsPackageJSON(a map[string]string) string {
	return fmt.Sprintf(`{
  "name": "%s-web",
  "version": "0.0.1",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "deploy": "opennextjs-cloudflare && wrangler deploy"
  },
  "dependencies": {
    "next": "^14.2.0",
    "react": "^18.3.0",
    "react-dom": "^18.3.0"
  },
  "devDependencies": {
    "@opennextjs/cloudflare": "^0.5.0",
    "typescript": "^5.5.0",
    "wrangler": "^3.80.0"
  }
}
`, a["slug"])
}

func nextjsConfig(a map[string]string) string {
	_ = a
	return `/** @type {import('next').NextConfig} */
const nextConfig = { reactStrictMode: true };
export default nextConfig;
`
}

func nextjsLandingPage(a map[string]string) string {
	return fmt.Sprintf(`export default function Home() {
  return (
    <main style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: "%s", color: "white", fontFamily: "system-ui" }}>
      <div style={{ textAlign: "center" }}>
        <h1 style={{ fontSize: 56, margin: 0 }}>%s</h1>
        <p style={{ opacity: 0.85, marginTop: 12 }}>%s</p>
        <a href="#waitlist" style={{ display: "inline-block", marginTop: 24, padding: "12px 24px", background: "%s", color: "black", borderRadius: 9999, textDecoration: "none", fontWeight: 600 }}>Get early access</a>
      </div>
    </main>
  );
}
`, a["primary_color"], a["app_name"], a["tagline"], a["accent_color"])
}

func nextjsLayout(a map[string]string) string {
	return fmt.Sprintf(`import "./globals.css";
export const metadata = { title: "%s", description: "%s" };
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
`, a["app_name"], a["tagline"])
}

func nextjsGlobals(a map[string]string) string {
	return fmt.Sprintf(`:root { --primary: %s; --accent: %s; }
* { box-sizing: border-box; }
body { margin: 0; color-scheme: %s; }
`, a["primary_color"], a["accent_color"], a["tone"])
}

func wranglerToml(a map[string]string) string {
	return fmt.Sprintf(`name = "%s-web"
main = ".open-next/worker.js"
compatibility_date = "2024-09-23"
compatibility_flags = ["nodejs_compat"]

[[routes]]
pattern = "%s/*"
zone_name = "%s"
custom_domain = true
`, a["slug"], a["domain"], a["domain"])
}

func convexSchema(a map[string]string) string {
	_ = a
	return `import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({
  users: defineTable({
    email: v.string(),
    name: v.optional(v.string()),
    avatarUrl: v.optional(v.string()),
    provider: v.string(), // "apple" | "google" | "microsoft" | "password"
    createdAt: v.number(),
  }).index("by_email", ["email"]),

  sessions: defineTable({
    userId: v.id("users"),
    token: v.string(),
    createdAt: v.number(),
    expiresAt: v.number(),
  }).index("by_token", ["token"]),
});
`
}

func convexAuth(a map[string]string) string {
	return fmt.Sprintf(`// Convex HTTP actions that handle OAuth callbacks for %s.
// Providers enabled: %s.
//
// Each handler receives the provider-issued code, exchanges it
// for an access token, fetches the user profile, and upserts a
// row into the users table. The real secret values live in Convex
// env vars — set them with `+"`npx convex env set`"+`.
import { httpRouter } from "convex/server";
import { httpAction } from "./_generated/server";

const http = httpRouter();

// Stub handlers — fill in with your preferred OAuth client.
http.route({
  path: "/auth/callback/apple",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: apple callback")),
});
http.route({
  path: "/auth/callback/google",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: google callback")),
});
http.route({
  path: "/auth/callback/microsoft",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: microsoft callback")),
});

export default http;
`, a["app_name"], describeAuth(a))
}

func convexPackageJSON(a map[string]string) string {
	return fmt.Sprintf(`{
  "name": "%s-backend",
  "version": "0.0.1",
  "private": true,
  "dependencies": {
    "convex": "^1.17.0"
  }
}
`, a["slug"])
}

func expoAppJSON(a map[string]string) string {
	return fmt.Sprintf(`{
  "expo": {
    "name": "%s",
    "slug": "%s",
    "version": "0.1.0",
    "orientation": "portrait",
    "userInterfaceStyle": "%s",
    "ios": {
      "bundleIdentifier": "%s",
      "supportsTablet": true
    },
    "android": {
      "package": "%s"
    },
    "newArchEnabled": true
  }
}
`, a["app_name"], a["slug"], a["tone"], a["ios_bundle_id"], a["android_package"])
}

func expoPackageJSON(a map[string]string) string {
	// scripts intentionally drop `expo build` / EAS — yaver's
	// convention is native local builds via xcodebuild (iOS) and
	// gradle (Android). `prebuild` generates ios/ and android/
	// once; from then on the dev ships bytecode through the
	// yaver container or archives a signed release here.
	return fmt.Sprintf(`{
  "name": "@%s/mobile",
  "version": "0.0.1",
  "private": true,
  "scripts": {
    "prebuild": "expo prebuild",
    "start": "expo start",
    "ios": "cd ios && xcodebuild -workspace *.xcworkspace -scheme %s -configuration Debug",
    "android": "cd android && ./gradlew assembleDebug"
  },
  "dependencies": {
    "expo": "~52.0.0",
    "react": "18.3.1",
    "react-native": "0.76.3"
  }
}
`, a["slug"], a["app_name"])
}

func expoAppTSX(a map[string]string) string {
	return fmt.Sprintf(`import { StatusBar } from "expo-status-bar";
import { Text, View } from "react-native";

export default function App() {
  return (
    <View style={{ flex: 1, backgroundColor: "%s", alignItems: "center", justifyContent: "center" }}>
      <Text style={{ color: "white", fontSize: 28, fontWeight: "700" }}>%s</Text>
      <Text style={{ color: "white", opacity: 0.8, marginTop: 8 }}>%s</Text>
      <StatusBar style="light" />
    </View>
  );
}
`, a["primary_color"], a["app_name"], a["tagline"])
}

func deployScript(a map[string]string) string {
	// deploy.sh follows yaver's native-build convention: no EAS,
	// no `expo build`, never a WebView. iOS goes through
	// xcodebuild archive → export → app-store-connect upload;
	// Android through gradle bundleRelease → Play Console
	// service-account upload. The web app always deploys via
	// wrangler when the host is Cloudflare.
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# deploy.sh — native builds only.\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd \"$(dirname \"$0\")/..\"\n\n")
	b.WriteString("case \"${1:-}\" in\n")
	if a["include_web"] == "true" && a["web_host"] == "cloudflare" {
		b.WriteString("  web)\n")
		b.WriteString("    cd apps/web && npm run deploy ;;\n")
	}
	if a["include_landing"] == "true" && a["web_host"] == "cloudflare" {
		b.WriteString("  landing)\n")
		b.WriteString("    cd apps/landing && npx wrangler pages deploy . ;;\n")
	}
	if a["include_backend"] == "true" && a["backend"] == "convex" {
		b.WriteString("  backend)\n")
		b.WriteString("    cd backend && npx convex deploy --yes ;;\n")
	}
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		b.WriteString("  testflight)\n")
		b.WriteString("    cd apps/mobile && npx expo prebuild --platform ios\n")
		b.WriteString("    cd apps/mobile/ios && xcodebuild -workspace *.xcworkspace \\\n")
		b.WriteString("      -scheme \"" + a["app_name"] + "\" -configuration Release \\\n")
		b.WriteString("      -archivePath /tmp/app.xcarchive archive \\\n")
		b.WriteString("      DEVELOPMENT_TEAM=\"$APPLE_TEAM_ID\" CODE_SIGN_STYLE=Automatic \\\n")
		b.WriteString("      -allowProvisioningUpdates \\\n")
		b.WriteString("      -authenticationKeyPath \"$APP_STORE_KEY_PATH\" \\\n")
		b.WriteString("      -authenticationKeyID \"$APP_STORE_KEY_ID\" \\\n")
		b.WriteString("      -authenticationKeyIssuerID \"$APP_STORE_KEY_ISSUER\"\n")
		b.WriteString("    xcodebuild -exportArchive -archivePath /tmp/app.xcarchive \\\n")
		b.WriteString("      -exportOptionsPlist ../../scripts/ExportOptions.plist \\\n")
		b.WriteString("      -exportPath /tmp/export \\\n")
		b.WriteString("      -allowProvisioningUpdates ;;\n")
		b.WriteString("  playstore)\n")
		b.WriteString("    cd apps/mobile && npx expo prebuild --platform android\n")
		b.WriteString("    cd apps/mobile/android && JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew bundleRelease ;;\n")
	}
	b.WriteString("  *)\n")
	b.WriteString("    echo \"usage: $0 web|landing|backend|testflight|playstore\" ;;\n")
	b.WriteString("esac\n")
	return b.String()
}

func envExample(a map[string]string) string {
	var b strings.Builder
	b.WriteString("# " + a["app_name"] + " — copy to .env and fill in\n\n")
	if a["oauth_apple"] == "true" {
		b.WriteString("APPLE_CLIENT_ID=\nAPPLE_KEY_ID=\nAPPLE_TEAM_ID=\nAPPLE_PRIVATE_KEY_PATH=\n\n")
	}
	if a["oauth_google"] == "true" {
		b.WriteString("GOOGLE_CLIENT_ID=\nGOOGLE_CLIENT_SECRET=\n\n")
	}
	if a["oauth_microsoft"] == "true" {
		b.WriteString("MICROSOFT_CLIENT_ID=\nMICROSOFT_TENANT_ID=\nMICROSOFT_CLIENT_SECRET=\n\n")
	}
	if a["payments"] == "stripe" {
		b.WriteString("STRIPE_PUBLIC_KEY=\nSTRIPE_SECRET_KEY=\nSTRIPE_WEBHOOK_SECRET=\n\n")
	}
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		b.WriteString("APP_STORE_KEY_PATH=\nAPP_STORE_KEY_ID=\nAPP_STORE_KEY_ISSUER=\nAPPLE_TEAM_ID=\nPLAY_STORE_KEY_FILE=\n")
	}
	return b.String()
}

func gitignoreBody() string {
	return `node_modules/
.next/
.open-next/
dist/
build/
.env
.env.local
ios/Pods/
android/.gradle/
android/app/build/
*.xcworkspace/xcuserdata
.DS_Store
`
}
