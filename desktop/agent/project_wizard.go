package main

// project_wizard.go — interactive fullstack project generator.
//
// This is the "stop repeating the same setup" tool the dev
// builds for himself: from a blank directory to a working
// web + mobile + backend + DNS + OAuth + TestFlight + Play
// Store scaffold, via a Q&A wizard that runs from CLI, HTTP,
// or MCP. Defaults are opinionated toward the stack the dev
// already ships (Cloudflare Workers + Next.js + Yaver backend +
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
// the stack the dev already ships every day: Yaver backend,
// Next.js on Cloudflare, Expo RN for iOS + Android. Each surface
// is opt-in so a "landing page only" project skips mobile, and a
// pure mobile project skips web. The layout is always a monorepo
// so later additions (an admin dashboard, a second app) fit
// without moving files around.
var wizardQuestions = []WizardQuestion{
	// Discovery — product strategy. Asked first so the AI agent,
	// the README, and the mobile starter all know *why* this app
	// exists before a single screen is scaffolded. These answers
	// also feed init.md so every subsequent autodev kick reads
	// the product intent instead of inferring it from code.
	{ID: "app_template", Kind: QChoice, Prompt: "What kind of app is this?", Help: "Shapes the mobile information architecture, starter screens, and default nav labels.", Choices: []string{
		"saas-dashboard",
		"creator-marketplace",
		"internal-tool",
		"consumer-social",
		"commerce",
		"booking",
		"ai-companion",
		"habit-wellness",
		"journal-notes",
		"fitness-tracker",
		"meal-recipe",
		"education-course",
		"finance-budget",
		"dating-social",
		"local-discovery",
		"creator-tool",
		"newsletter-blog",
		"dev-tool",
		"analytics-dashboard",
		"photo-video",
	}, Default: "saas-dashboard"},
	{ID: "audience_type", Kind: QChoice, Prompt: "Who will use this every day?", Help: "Frames the UX — a consumer indie app and an internal B2B tool are written differently.", Choices: []string{"consumers", "small-businesses", "enterprise-teams", "agency-clients", "family-and-friends", "internal-team", "developers", "creators"}, Default: "consumers"},
	{ID: "problem_statement", Kind: QText, Prompt: "What problem does it solve in one sentence?", Help: "Example: Busy parents can't plan weeknight dinners without 30 minutes of scrolling.", Default: ""},
	{ID: "unique_angle", Kind: QText, Prompt: "What makes this different from the obvious incumbents?", Help: "One short sentence. Example: Works offline, no account required, shares via a single QR code.", Default: ""},
	{ID: "competitor_inspiration", Kind: QText, Prompt: "Closest competitor or inspiration app", Help: "Optional. Paste a name or URL so the agent keeps the reference on hand.", Default: ""},
	{ID: "monetization", Kind: QChoice, Prompt: "How will it make money?", Help: "Defaults drive the generated paywall placement, auth gates, and store submission notes.", Choices: []string{"free", "freemium", "subscription", "one-time-purchase", "marketplace-commission", "ads", "b2b-contract"}, Default: "free"},
	{ID: "launch_timeline", Kind: QChoice, Prompt: "When do you want a first usable build in hand?", Help: "We scope the scaffold accordingly — a weekend build keeps things minimal; a three-month plan enables richer stubs.", Choices: []string{"weekend", "1-2-weeks", "1-month", "3-months", "whenever"}, Default: "1-2-weeks"},
	{ID: "success_metric", Kind: QChoice, Prompt: "What will tell you it is working?", Help: "Shapes the analytics + dashboard starter content.", Choices: []string{"daily-active-users", "monthly-recurring-revenue", "week-4-retention", "community-size", "enterprise-contracts", "personal-usage"}, Default: "daily-active-users"},
	{ID: "distribution_channel", Kind: QChoice, Prompt: "How will the first 100 users discover it?", Help: "Feeds the landing SEO copy, app-store keywords, and review notes.", Choices: []string{"app-store-seo", "social-tiktok-instagram", "email-newsletter", "word-of-mouth", "paid-ads", "dev-community", "niche-forum", "b2b-outreach"}, Default: "word-of-mouth"},

	// Identity
	{ID: "app_name", Kind: QText, Prompt: "What's the app called?", Help: "Short brand name — shown everywhere.", Required: true},
	{ID: "slug", Kind: QText, Prompt: "URL-safe slug", Help: "Used for the monorepo folder + package names + bundle IDs.", Default: "myapp"},
	{ID: "description", Kind: QText, Prompt: "Describe the app in one paragraph", Help: "Goes into the README, the landing page hero, and feeds the AI agent when it later helps you build features.", Default: ""},
	{ID: "tagline", Kind: QText, Prompt: "One-line tagline", Help: "Landing page subheader. If you press Enter I'll derive one from your problem statement or description.", Default: ""},
	{ID: "supported_languages", Kind: QText, Prompt: "Supported app languages", Help: "Comma-separated user-facing languages, e.g. English, Turkish. Leave blank for English only.", Default: "English"},

	// Domain + branding
	{ID: "domain", Kind: QText, Prompt: "Production domain (leave blank if not decided)", Help: "e.g. myapp.com — wired into wrangler + OAuth redirects.", Default: ""},
	{ID: "primary_color", Kind: QColor, Prompt: "Primary brand color (hex)", Default: "#4F46E5"},
	{ID: "secondary_color", Kind: QColor, Prompt: "Secondary brand color (hex)", Default: "#0EA5E9"},
	{ID: "accent_color", Kind: QColor, Prompt: "Accent color (hex)", Default: "#F59E0B"},
	{ID: "surface_color", Kind: QColor, Prompt: "Surface / card color (hex)", Default: "#111827"},
	{ID: "tone", Kind: QChoice, Prompt: "Visual tone", Choices: []string{"light", "dark", "system"}, Default: "system"},

	// Which surfaces to scaffold — defaults = everything the dev's own stack needs.
	{ID: "include_web", Kind: QBool, Prompt: "Include a web app (Next.js on Cloudflare)?", Default: "true"},
	{ID: "include_mobile", Kind: QBool, Prompt: "Include a mobile app (Expo RN for iOS + Android)?", Default: "true"},
	{ID: "include_backend", Kind: QBool, Prompt: "Include a Yaver backend?", Default: "true"},
	{ID: "include_landing", Kind: QBool, Prompt: "Include a marketing landing page?", Default: "true"},

	// Stack — only asked when the surface is on. Conditional
	// skipping happens in nextQuestion().
	{ID: "web_framework", Kind: QChoice, Prompt: "Web framework", Choices: []string{"nextjs", "remix", "astro"}, Default: "nextjs"},
	{ID: "web_host", Kind: QChoice, Prompt: "Host the web app on?", Choices: []string{"cloudflare", "vercel", "netlify", "self-host"}, Default: "cloudflare"},
	{ID: "backend", Kind: QChoice, Prompt: "Backend platform", Choices: []string{"sqlite", "postgres", "supabase", "convex", "pocketbase", "appwrite", "none"}, Default: "sqlite"},
	{ID: "mobile_stack", Kind: QChoice, Prompt: "Mobile stack", Choices: []string{"expo-rn", "native"}, Default: "expo-rn"},
	{ID: "mobile_nav_style", Kind: QChoice, Prompt: "Primary mobile navigation", Help: "Bottom tabs are the default because they work well for thumb-first product flows.", Choices: []string{"bottom-tabs", "top-tabs", "drawer", "stack-only"}, Default: "bottom-tabs"},
	{ID: "mobile_nav_count", Kind: QChoice, Prompt: "How many primary nav items?", Help: "3 to 5 is the comfortable range for bottom navigation.", Choices: []string{"3", "4", "5"}, Default: "4"},
	{ID: "mobile_nav_labels", Kind: QText, Prompt: "Navigation labels", Help: "Comma-separated labels. Leave blank and the starter uses template-aware defaults.", Default: ""},
	{ID: "design_source", Kind: QChoice, Prompt: "Design reference source", Help: "Tell the generator whether to rely on prompt-only direction, a Figma frame, Canva board, or screenshots.", Choices: []string{"prompt-only", "figma", "canva", "screenshots", "other-url"}, Default: "prompt-only"},
	{ID: "design_reference_url", Kind: QText, Prompt: "Design reference URL", Help: "Paste a share link to a Figma file, Canva board, or screenshot folder. Leave blank to skip.", Default: ""},
	{ID: "design_notes", Kind: QText, Prompt: "Design notes", Help: "Optional cues like 'dense admin UI', 'large cards for thumbs', 'use rounded playful illustrations'.", Default: ""},

	// Auth — only asked when any surface needs it.
	{ID: "oauth_apple", Kind: QBool, Prompt: "Add Apple Sign-In?", Default: "true"},
	{ID: "oauth_google", Kind: QBool, Prompt: "Add Google Sign-In?", Default: "true"},
	{ID: "oauth_microsoft", Kind: QBool, Prompt: "Add Microsoft / O365 Sign-In?", Default: "false"},
	{ID: "oauth_email", Kind: QBool, Prompt: "Add email + password fallback?", Default: "true"},

	// Mobile permissions — ask early, while product intent is still fresh,
	// so Info.plist / Android manifest copy and review docs are not
	// forgotten until App Store / Play submission week.
	{ID: "mobile_permission_camera", Kind: QBool, Prompt: "Will the mobile app use the camera?", Help: "Turn this on for QR scanning, document capture, profile photos, AR, or camera-based flows.", Default: "false"},
	{ID: "mobile_permission_camera_usage", Kind: QText, Prompt: "Short camera permission reason", Help: "One sentence for Info.plist and review docs. Example: Scan QR codes to pair nearby devices.", Default: "Scan QR codes and capture photos for setup and support flows."},
	{ID: "mobile_permission_photos", Kind: QBool, Prompt: "Will the mobile app read or save photos?", Help: "Turn this on if users attach screenshots, upload photos, or export generated media.", Default: "false"},
	{ID: "mobile_permission_photos_usage", Kind: QText, Prompt: "Short photos permission reason", Help: "One sentence for photo library usage text. Example: Attach screenshots to bug reports.", Default: "Attach screenshots and save generated media when the user asks."},
	{ID: "mobile_permission_microphone", Kind: QBool, Prompt: "Will the mobile app record audio?", Help: "Turn this on for voice input, voice notes, calls, or on-device transcription.", Default: "false"},
	{ID: "mobile_permission_microphone_usage", Kind: QText, Prompt: "Short microphone permission reason", Help: "One sentence for microphone usage text. Example: Record voice prompts and transcribe them on device.", Default: "Record voice prompts and notes for speech-to-text workflows."},
	{ID: "mobile_permission_location", Kind: QBool, Prompt: "Will the mobile app use location?", Help: "Turn this on only for maps, nearby discovery, logistics, or location-aware automation.", Default: "false"},
	{ID: "mobile_permission_location_usage", Kind: QText, Prompt: "Short location permission reason", Help: "One sentence for location usage text. Example: Show nearby jobs and route-aware updates.", Default: "Show nearby devices and location-aware results when the user requests them."},
	{ID: "mobile_permission_bluetooth", Kind: QBool, Prompt: "Will the mobile app use Bluetooth / BLE?", Help: "Turn this on for pairing, device discovery, sensors, or talking to local hardware.", Default: "false"},
	{ID: "mobile_permission_bluetooth_usage", Kind: QText, Prompt: "Short Bluetooth permission reason", Help: "One sentence for Bluetooth usage text. Example: Discover and connect to local hardware over BLE.", Default: "Discover and connect to nearby accessories and local hardware."},
	{ID: "mobile_permission_notifications", Kind: QBool, Prompt: "Will the mobile app send notifications?", Help: "Turn this on for task status, reminders, builds, device alerts, or messaging.", Default: "false"},
	{ID: "mobile_permission_notifications_usage", Kind: QText, Prompt: "Short notifications permission reason", Help: "One sentence for review docs and notification prompts. Example: Send build completion and task alerts.", Default: "Send build, sync, and task status alerts."},
	{ID: "mobile_permission_photos_save", Kind: QBool, Prompt: "Will the mobile app save photos or media to the user's library?", Help: "Turn this on if the app exports receipts, saves generated images, or writes captures back to the camera roll. Adds NSPhotoLibraryAddUsageDescription on iOS.", Default: "false"},
	{ID: "mobile_permission_photos_save_usage", Kind: QText, Prompt: "Short photos-save permission reason", Help: "One sentence for the write-to-library usage text. Example: Save generated images to your photo library when you tap Export.", Default: "Save generated media back to your photo library when you ask."},
	{ID: "mobile_permission_location_always", Kind: QBool, Prompt: "Does the app need background location (always)?", Help: "Only enable if the app truly needs location when backgrounded (fleet tracking, geofencing). Most apps should leave this off and use when-in-use only.", Default: "false"},
	{ID: "mobile_permission_location_always_usage", Kind: QText, Prompt: "Short always-on location reason", Help: "One sentence for NSLocationAlwaysAndWhenInUseUsageDescription. Example: Keep delivery routes updated while the app is backgrounded.", Default: "Continue location-aware features while the app is in the background."},
	{ID: "mobile_permission_tracking", Kind: QBool, Prompt: "Will the app use tracking or third-party analytics/ads SDKs (IDFA / cross-app tracking)?", Help: "Say yes only if you integrate Facebook SDK, AppsFlyer, Branch, AdMob, or similar. This triggers the iOS App Tracking Transparency prompt (NSUserTrackingUsageDescription).", Default: "false"},
	{ID: "mobile_permission_tracking_usage", Kind: QText, Prompt: "Short tracking permission reason", Help: "One sentence for the ATT prompt. Example: Measure ad performance and personalize offers across apps.", Default: "Help us measure performance and improve the experience across apps."},

	// Store submission + compliance posture.
	{ID: "mobile_account_deletion", Kind: QBool, Prompt: "Generate an in-app account deletion flow?", Help: "Apple requires this for any app that creates accounts (since June 2022). Google Play requires a delete URL (since May 2024). Default is yes.", Default: "true"},
	{ID: "mobile_data_collection", Kind: QChoice, Prompt: "Data collection profile for store disclosures", Help: "Drives Apple App Privacy nutrition label and Google Play Data Safety templates. 'none' = local-only, 'minimal' = auth + crash only, 'standard' = auth + crash + analytics, 'tracking' = standard plus cross-app tracking/ads.", Choices: []string{"none", "minimal", "standard", "tracking"}, Default: "minimal"},
	{ID: "audience_children", Kind: QBool, Prompt: "Is the app targeted at or likely to be used by children under 13?", Help: "Triggers COPPA disclosures in the privacy policy and Play 'Designed for Families' / Apple 'Kids Category' notes.", Default: "false"},

	// Payments.
	{ID: "payments", Kind: QChoice, Prompt: "Payments provider", Choices: []string{"stripe", "lemon-squeezy", "paddle", "none"}, Default: "stripe"},

	// Mobile identifiers — only asked when mobile is on.
	{ID: "ios_bundle_id", Kind: QText, Prompt: "iOS bundle ID", Default: "com.myco.myapp"},
	{ID: "android_package", Kind: QText, Prompt: "Android package name", Default: "com.myco.myapp"},

	// Deploy credentials — all optional so non-developers can skip.
	{ID: "apple_team_id", Kind: QText, Prompt: "Apple Team ID (leave blank to skip TestFlight)", Default: ""},
	{ID: "play_service_account", Kind: QText, Prompt: "Path to Play service account JSON (leave blank to skip)", Default: ""},
	{ID: "cloudflare_zone", Kind: QText, Prompt: "Cloudflare zone for the domain (leave blank if not on CF yet)", Default: ""},

	// Legal + store review posture.
	{ID: "legal_entity_name", Kind: QText, Prompt: "Legal entity or publisher name", Help: "Shown in privacy policy, terms, and store review notes. Leave blank and the app name will be used.", Default: ""},
	{ID: "legal_support_email", Kind: QText, Prompt: "Support / privacy contact email", Help: "Used in privacy policy, terms, and in-app legal screens.", Default: ""},
	{ID: "legal_jurisdiction", Kind: QText, Prompt: "Governing law / jurisdiction", Help: "Example: Republic of Turkiye or State of Delaware, United States.", Default: ""},
	{ID: "legal_privacy_notes", Kind: QText, Prompt: "Extra privacy / compliance notes", Help: "Optional short notes like local-only processing, no third-party ads, or export-delete support.", Default: ""},

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
		if (q.ID == "mobile_nav_style" || q.ID == "mobile_nav_count" || q.ID == "mobile_nav_labels") && !mobileOn {
			sess.Answers[q.ID] = ""
			continue
		}
		if strings.HasPrefix(q.ID, "mobile_permission_") && !mobileOn {
			if strings.HasSuffix(q.ID, "_usage") {
				sess.Answers[q.ID] = ""
			} else {
				sess.Answers[q.ID] = "false"
			}
			continue
		}
		// Save-to-library is only meaningful when photos is already on.
		if (q.ID == "mobile_permission_photos_save" || q.ID == "mobile_permission_photos_save_usage") && sess.Answers["mobile_permission_photos"] != "true" {
			if strings.HasSuffix(q.ID, "_usage") {
				sess.Answers[q.ID] = ""
			} else {
				sess.Answers[q.ID] = "false"
			}
			continue
		}
		// Always-on location only when when-in-use location is on.
		if (q.ID == "mobile_permission_location_always" || q.ID == "mobile_permission_location_always_usage") && sess.Answers["mobile_permission_location"] != "true" {
			if strings.HasSuffix(q.ID, "_usage") {
				sess.Answers[q.ID] = ""
			} else {
				sess.Answers[q.ID] = "false"
			}
			continue
		}
		// Store posture questions only when mobile is on.
		if (q.ID == "mobile_account_deletion" || q.ID == "mobile_data_collection" || q.ID == "audience_children") && !mobileOn {
			if q.ID == "mobile_data_collection" {
				sess.Answers[q.ID] = "none"
			} else {
				sess.Answers[q.ID] = "false"
			}
			continue
		}
		if strings.HasSuffix(q.ID, "_usage") {
			baseID := strings.TrimSuffix(q.ID, "_usage")
			if strings.HasPrefix(baseID, "mobile_permission_") && sess.Answers[baseID] != "true" {
				sess.Answers[q.ID] = ""
				continue
			}
		}
		// Backend-only questions
		if q.ID == "backend" && !backendOn {
			sess.Answers[q.ID] = "none"
			continue
		}
		if q.ID == "design_reference_url" && (sess.Answers["design_source"] == "" || sess.Answers["design_source"] == "prompt-only") {
			sess.Answers[q.ID] = ""
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
		if strings.HasPrefix(q.ID, "legal_") && !mobileOn && !webOn {
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
	OK              bool                   `json:"ok"`
	Directory       string                 `json:"directory"`
	Files           []string               `json:"files"`
	NextSteps       []string               `json:"nextSteps"`
	YaverOnboarding map[string]interface{} `json:"yaverOnboarding,omitempty"`
	ServicesLog     string                 `json:"servicesLog,omitempty"`
	ServicesError   string                 `json:"servicesError,omitempty"`
	ServicesStarted bool                   `json:"servicesStarted,omitempty"`
	Topology        map[string]interface{} `json:"topology,omitempty"`
	AutoInit        map[string]interface{} `json:"autoinit,omitempty"`
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
//	│   └── ...         ← Yaver backend manifest / backend-specific files (opt-in)
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
		// Prefer the problem statement when present — it usually makes
		// a better one-liner than the long-form description.
		base := strings.TrimSpace(a["problem_statement"])
		if base == "" {
			base = a["description"]
		}
		a["tagline"] = deriveTagline(base, a["app_name"])
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
	if err := write("legal/privacy.md", privacyPolicyMarkdown(a)); err != nil {
		return nil, err
	}
	if err := write("legal/terms.md", termsMarkdown(a)); err != nil {
		return nil, err
	}
	if err := write("legal/app-review.md", appReviewLegalChecklist(a)); err != nil {
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
		if err := write("apps/web/app/privacy/page.tsx", nextjsPrivacyPage(a)); err != nil {
			return nil, err
		}
		if err := write("apps/web/app/terms/page.tsx", nextjsTermsPage(a)); err != nil {
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
		if err := write("apps/landing/privacy.html", landingPolicyHTML(a, "Privacy Policy", privacyPolicyHTMLBody(a))); err != nil {
			return nil, err
		}
		if err := write("apps/landing/terms.html", landingPolicyHTML(a, "Terms & Conditions", termsHTMLBody(a))); err != nil {
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
		if err := write("apps/mobile/index.js", expoIndexJS()); err != nil {
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
		// iOS Privacy Manifest — required by Apple since 2024-05-01.
		// Expo's prebuild copies this into ios/<AppName>/ at build time.
		if err := write("apps/mobile/ios/PrivacyInfo.xcprivacy", iOSPrivacyManifest(a)); err != nil {
			return nil, err
		}
		if a["mobile_account_deletion"] == "true" {
			if err := write("apps/mobile/screens/DeleteAccount.tsx", mobileDeleteAccountScreen(a)); err != nil {
				return nil, err
			}
		}
	}

	// --- account deletion web route (Play Store requires a public URL) ---
	if a["include_web"] == "true" && a["mobile_account_deletion"] == "true" {
		if err := write("apps/web/app/account/delete/page.tsx", webAccountDeletePage(a)); err != nil {
			return nil, err
		}
	}
	if a["include_landing"] == "true" && a["mobile_account_deletion"] == "true" {
		if err := write("apps/landing/account-delete.html", landingAccountDeleteHTML(a)); err != nil {
			return nil, err
		}
	}

	// --- store disclosure templates ---
	if a["include_mobile"] == "true" {
		if err := write("legal/play-data-safety.md", playDataSafetyMarkdown(a)); err != nil {
			return nil, err
		}
		if err := write("legal/app-privacy-nutrition.md", applePrivacyNutritionMarkdown(a)); err != nil {
			return nil, err
		}
	}

	// --- backend (Yaver-first portable manifest, with escape-hatch backends optional) ---
	if a["include_backend"] == "true" && a["backend"] == "sqlite" {
		if err := write("backend/README.md", yaverBackendReadme(a)); err != nil {
			return nil, err
		}
		if err := write("backend/schema.yaml", yaverBackendSchema(a)); err != nil {
			return nil, err
		}
		if err := write("backend/auth.yaml", yaverBackendAuth(a)); err != nil {
			return nil, err
		}
		if err := write("backend/seed.json", yaverBackendSeed(a)); err != nil {
			return nil, err
		}
	}
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
		Topology:  detectRepoTopology(dir, DetectProjectInfo(dir)),
	}
	if pushResult != "" {
		res.NextSteps = append([]string{"Git remote: " + pushResult}, res.NextSteps...)
	}

	// Auto-start the local backend services the wizard just wired up. This
	// powers the Video 1 "tap Create Project and the backend is live"
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
	if autoinitResp, err := startAutoInitBackground(AutoInitStart{
		Project: slug,
		WorkDir: dir,
		Prompt:  wizardAutoInitHint(a),
	}); err == nil {
		res.AutoInit = autoinitResp
	} else {
		res.AutoInit = map[string]interface{}{
			"ok":      false,
			"started": false,
			"error":   err.Error(),
		}
	}
	return res, nil
}

// --- monorepo helpers ------------------------------------------------------

// finishWizardWithDefaults advances a partially answered wizard session to
// Done by accepting defaults for every remaining question. It is used by MCP
// and CLI one-shot flows where the caller provides only the high-signal fields.
func finishWizardWithDefaults(sess *WizardSession) {
	if sess == nil {
		return
	}
	for {
		q := nextQuestion(sess)
		if q == nil || q.Kind == QDone {
			return
		}
		answer := q.Default
		if q.ID == "confirm" {
			answer = "true"
		}
		_, _ = AnswerWizard(sess.ID, q.ID, answer)
	}
}

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

type mobilePermissionSpec struct {
	ID             string
	Label          string
	IOSKey         string
	AndroidPerms   []string
	PrivacySummary string
}

var mobilePermissionSpecs = []mobilePermissionSpec{
	{
		ID:             "mobile_permission_camera",
		Label:          "Camera",
		IOSKey:         "NSCameraUsageDescription",
		AndroidPerms:   []string{"android.permission.CAMERA"},
		PrivacySummary: "camera content the user chooses to capture",
	},
	{
		ID:             "mobile_permission_photos",
		Label:          "Photos",
		IOSKey:         "NSPhotoLibraryUsageDescription",
		AndroidPerms:   []string{"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO"},
		PrivacySummary: "photos, screenshots, and media the user chooses to attach",
	},
	{
		ID:             "mobile_permission_microphone",
		Label:          "Microphone",
		IOSKey:         "NSMicrophoneUsageDescription",
		AndroidPerms:   []string{"android.permission.RECORD_AUDIO"},
		PrivacySummary: "voice recordings used for voice input or notes",
	},
	{
		ID:             "mobile_permission_location",
		Label:          "Location",
		IOSKey:         "NSLocationWhenInUseUsageDescription",
		AndroidPerms:   []string{"android.permission.ACCESS_COARSE_LOCATION", "android.permission.ACCESS_FINE_LOCATION"},
		PrivacySummary: "coarse or precise location when the user starts a location-aware flow",
	},
	{
		ID:             "mobile_permission_bluetooth",
		Label:          "Bluetooth",
		IOSKey:         "NSBluetoothAlwaysUsageDescription",
		AndroidPerms:   []string{"android.permission.BLUETOOTH_SCAN", "android.permission.BLUETOOTH_CONNECT"},
		PrivacySummary: "nearby Bluetooth devices and accessories selected by the user",
	},
	{
		ID:             "mobile_permission_notifications",
		Label:          "Notifications",
		AndroidPerms:   []string{"android.permission.POST_NOTIFICATIONS"},
		PrivacySummary: "device tokens used to deliver push notifications",
	},
	{
		ID:             "mobile_permission_photos_save",
		Label:          "Save to photo library",
		IOSKey:         "NSPhotoLibraryAddUsageDescription",
		AndroidPerms:   []string{},
		PrivacySummary: "media the app writes back to the user's photo library at their request",
	},
	{
		ID:             "mobile_permission_location_always",
		Label:          "Background location",
		IOSKey:         "NSLocationAlwaysAndWhenInUseUsageDescription",
		AndroidPerms:   []string{"android.permission.ACCESS_BACKGROUND_LOCATION"},
		PrivacySummary: "location data used for background-aware features the user explicitly enables",
	},
	{
		ID:             "mobile_permission_tracking",
		Label:          "App Tracking Transparency (IDFA)",
		IOSKey:         "NSUserTrackingUsageDescription",
		AndroidPerms:   []string{"com.google.android.gms.permission.AD_ID"},
		PrivacySummary: "advertising or third-party identifiers used for cross-app measurement when the user allows it",
	},
}

func selectedMobilePermissions(a map[string]string) []mobilePermissionSpec {
	selected := []mobilePermissionSpec{}
	for _, spec := range mobilePermissionSpecs {
		if a[spec.ID] == "true" {
			selected = append(selected, spec)
		}
	}
	return selected
}

func permissionUsageText(a map[string]string, permissionID string) string {
	text := strings.TrimSpace(a[permissionID+"_usage"])
	if text != "" {
		return text
	}
	for _, spec := range mobilePermissionSpecs {
		if spec.ID == permissionID {
			return fmt.Sprintf("Use %s only when the user starts a feature that needs it in %s.", strings.ToLower(spec.Label), a["app_name"])
		}
	}
	return fmt.Sprintf("Use this permission only when it is needed in %s.", a["app_name"])
}

func legalEntityName(a map[string]string) string {
	if strings.TrimSpace(a["legal_entity_name"]) != "" {
		return strings.TrimSpace(a["legal_entity_name"])
	}
	return a["app_name"]
}

func legalSupportEmail(a map[string]string) string {
	if strings.TrimSpace(a["legal_support_email"]) != "" {
		return strings.TrimSpace(a["legal_support_email"])
	}
	if strings.TrimSpace(a["domain"]) != "" {
		return "support@" + strings.TrimSpace(a["domain"])
	}
	return "support@" + a["slug"] + ".local"
}

func legalJurisdiction(a map[string]string) string {
	if strings.TrimSpace(a["legal_jurisdiction"]) != "" {
		return strings.TrimSpace(a["legal_jurisdiction"])
	}
	return "the jurisdiction where the publisher of " + a["app_name"] + " is established"
}

func privacyNotes(a map[string]string) string {
	return strings.TrimSpace(a["legal_privacy_notes"])
}

func jsQuoted(v string) string {
	return fmt.Sprintf("%q", v)
}

func boolLiteral(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// wizardAutoInitHint returns the product-strategy context the user
// just gave us, formatted for autoinit's AI generator. This lets the
// generated init.md open with "who / what / why" instead of having
// to infer it from directory layout alone.
func wizardAutoInitHint(a map[string]string) string {
	parts := []string{
		"Product context captured by the phone-first wizard. Use this as the canonical 'Why this exists' section of init.md so every subsequent autodev / autoideas kick inherits it.",
		"",
		"- App name: " + a["app_name"],
		"- Template: " + nonEmpty(a["app_template"], "saas-dashboard"),
		"- Audience: " + nonEmpty(a["audience_type"], "consumers"),
		"- Monetization: " + nonEmpty(a["monetization"], "free"),
		"- Launch timeline: " + nonEmpty(a["launch_timeline"], "1-2-weeks"),
		"- Success metric: " + nonEmpty(a["success_metric"], "daily-active-users"),
		"- Distribution plan: " + nonEmpty(a["distribution_channel"], "word-of-mouth"),
	}
	if problem := strings.TrimSpace(a["problem_statement"]); problem != "" {
		parts = append(parts, "- Problem: "+problem)
	}
	if angle := strings.TrimSpace(a["unique_angle"]); angle != "" {
		parts = append(parts, "- Unique angle: "+angle)
	}
	if comp := strings.TrimSpace(a["competitor_inspiration"]); comp != "" {
		parts = append(parts, "- Reference / inspiration: "+comp)
	}
	if desc := strings.TrimSpace(a["description"]); desc != "" {
		parts = append(parts, "", "Longer description: "+desc)
	}
	return strings.Join(parts, "\n")
}

func csvItems(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func appLanguages(a map[string]string) []string {
	items := csvItems(a["supported_languages"])
	if len(items) == 0 {
		return []string{"English"}
	}
	return items
}

func defaultNavLabels(templateID, count string) []string {
	switch templateID {
	case "creator-marketplace":
		if count == "3" {
			return []string{"Discover", "Orders", "Profile"}
		}
		if count == "5" {
			return []string{"Discover", "Saved", "Sell", "Orders", "Profile"}
		}
		return []string{"Discover", "Saved", "Sell", "Profile"}
	case "internal-tool":
		if count == "3" {
			return []string{"Home", "Queue", "Profile"}
		}
		if count == "5" {
			return []string{"Home", "Queue", "Approvals", "Reports", "Profile"}
		}
		return []string{"Home", "Queue", "Reports", "Profile"}
	case "consumer-social":
		if count == "3" {
			return []string{"Feed", "Create", "Profile"}
		}
		if count == "5" {
			return []string{"Feed", "Explore", "Create", "Inbox", "Profile"}
		}
		return []string{"Feed", "Explore", "Create", "Profile"}
	case "commerce":
		if count == "3" {
			return []string{"Shop", "Cart", "Profile"}
		}
		if count == "5" {
			return []string{"Shop", "Categories", "Saved", "Cart", "Profile"}
		}
		return []string{"Shop", "Search", "Cart", "Profile"}
	case "booking":
		if count == "3" {
			return []string{"Explore", "Trips", "Profile"}
		}
		if count == "5" {
			return []string{"Explore", "Saved", "Bookings", "Inbox", "Profile"}
		}
		return []string{"Explore", "Saved", "Bookings", "Profile"}
	case "ai-companion":
		if count == "3" {
			return []string{"Chat", "Tasks", "Profile"}
		}
		if count == "5" {
			return []string{"Chat", "Tasks", "Library", "Insights", "Profile"}
		}
		return []string{"Chat", "Tasks", "Insights", "Profile"}
	default:
		if count == "3" {
			return []string{"Home", "Work", "Profile"}
		}
		if count == "5" {
			return []string{"Home", "Projects", "Create", "Inbox", "Profile"}
		}
		return []string{"Home", "Projects", "Activity", "Profile"}
	}
}

func mobileNavLabels(a map[string]string) []string {
	labels := csvItems(a["mobile_nav_labels"])
	count := a["mobile_nav_count"]
	want := 4
	if count == "3" {
		want = 3
	} else if count == "5" {
		want = 5
	}
	if len(labels) >= want {
		return labels[:want]
	}
	for _, fallback := range defaultNavLabels(a["app_template"], count) {
		if len(labels) >= want {
			break
		}
		labels = append(labels, fallback)
	}
	return labels
}

func bulletList(items []string) string {
	if len(items) == 0 {
		return "- None"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func jsStringArray(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf("%q", item))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
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
		if a["backend"] == "sqlite" {
			stack = append(stack, "- **Backend** (`backend/`) — Yaver portable backend (SQLite first, promotable to your hardware or Yaver Cloud)")
		} else {
			stack = append(stack, fmt.Sprintf("- **Backend** (`backend/`) — %s", a["backend"]))
		}
	}
	stack = append(stack, "- **Shared** (`packages/shared`) — cross-surface TS types + utils")
	productNotes := []string{
		"Template: " + a["app_template"],
		"Audience: " + nonEmpty(a["audience_type"], "consumers"),
		"Monetization: " + nonEmpty(a["monetization"], "free"),
		"Launch goal: " + nonEmpty(a["launch_timeline"], "1-2-weeks"),
		"Success metric: " + nonEmpty(a["success_metric"], "daily-active-users"),
		"Distribution: " + nonEmpty(a["distribution_channel"], "word-of-mouth"),
		"Supported languages: " + strings.Join(appLanguages(a), ", "),
		"Palette: primary " + a["primary_color"] + ", secondary " + a["secondary_color"] + ", accent " + a["accent_color"] + ", surface " + a["surface_color"],
	}
	if strings.TrimSpace(a["problem_statement"]) != "" {
		productNotes = append([]string{"Problem: " + strings.TrimSpace(a["problem_statement"])}, productNotes...)
	}
	if strings.TrimSpace(a["unique_angle"]) != "" {
		productNotes = append(productNotes, "Unique angle: "+strings.TrimSpace(a["unique_angle"]))
	}
	if strings.TrimSpace(a["competitor_inspiration"]) != "" {
		productNotes = append(productNotes, "Reference: "+strings.TrimSpace(a["competitor_inspiration"]))
	}
	if a["include_mobile"] == "true" {
		productNotes = append(productNotes, "Mobile navigation: "+a["mobile_nav_style"]+" ("+strings.Join(mobileNavLabels(a), ", ")+")")
		perms := selectedMobilePermissions(a)
		if len(perms) == 0 {
			productNotes = append(productNotes, "Mobile permissions: none requested at scaffold time")
		} else {
			names := make([]string, 0, len(perms))
			for _, spec := range perms {
				names = append(names, spec.Label)
			}
			productNotes = append(productNotes, "Mobile permissions: "+strings.Join(names, ", "))
		}
	}
	productNotes = append(productNotes, "Legal contact: "+legalSupportEmail(a))
	if a["design_source"] != "" && a["design_source"] != "prompt-only" {
		productNotes = append(productNotes, "Design reference: "+a["design_source"]+" — "+a["design_reference_url"])
	}
	return fmt.Sprintf(`# %s

> %s

%s

---

## Stack

%s

Auth: %s
Payments: %s

## Product defaults

%s

## Quick start

%s`+"```bash\nnpm install\n./scripts/dev.sh    # runs every app in dev mode\n./scripts/deploy.sh # builds + deploys every surface\n```\n\n"+`See [SETUP.md](./SETUP.md) for one-time signups (OAuth, Cloudflare, TestFlight, Play Store).

Generated by `+"`yaver new`"+` on %s.
`,
		a["app_name"], a["tagline"], a["description"],
		strings.Join(stack, "\n"),
		describeAuth(a), a["payments"],
		bulletList(productNotes),
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
export const APP_TEMPLATE = %q;
export const APP_LANGUAGES = %s;
export const MOBILE_NAV_ITEMS = %s;

export interface User {
  id: string;
  email: string;
  name?: string;
  avatarUrl?: string;
}
`, a["app_name"], a["app_name"], a["tagline"], a["app_template"], jsStringArray(appLanguages(a)), jsStringArray(mobileNavLabels(a)))
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
      footer { margin-top: 24px; display: flex; justify-content: center; gap: 16px; flex-wrap: wrap; }
      footer a { color: rgba(255,255,255,.8); text-decoration: none; }
    </style>
  </head>
  <body>
    <main>
      <div>
        <h1>%s</h1>
        <p>%s</p>
        <a class="cta" href="#waitlist">Get early access</a>
        <footer>
          <a href="/privacy.html">Privacy</a>
          <a href="/terms.html">Terms</a>
          <a href="mailto:%s">%s</a>
        </footer>
      </div>
    </main>
  </body>
</html>
`, a["app_name"], a["tagline"], a["primary_color"], a["accent_color"], a["app_name"], a["tagline"], legalSupportEmail(a), legalSupportEmail(a))
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
	parts := authProviders(a)
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, ", ")
}

func authProviders(a map[string]string) []string {
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
	return parts
}

func quickStartFor(a map[string]string) string {
	lines := []string{
		"```bash",
		"# 1. install deps",
	}
	if a["web_framework"] == "nextjs" {
		lines = append(lines, "cd web && npm install && cd ..")
	}
	if a["backend"] == "sqlite" {
		lines = append(lines, "Yaver backend: backend/schema.yaml + backend/auth.yaml + backend/seed.json")
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

	b.WriteString("## 2. Design handoff\n\n")
	b.WriteString("- Palette chosen in the wizard:\n")
	b.WriteString("  - Primary: `" + a["primary_color"] + "`\n")
	b.WriteString("  - Secondary: `" + a["secondary_color"] + "`\n")
	b.WriteString("  - Accent: `" + a["accent_color"] + "`\n")
	b.WriteString("  - Surface: `" + a["surface_color"] + "`\n")
	b.WriteString("- Starter template: `" + a["app_template"] + "`\n")
	b.WriteString("- Supported app languages: `" + strings.Join(appLanguages(a), ", ") + "`\n")
	if a["design_source"] != "" && a["design_source"] != "prompt-only" {
		b.WriteString("- Linked reference (" + a["design_source"] + "): " + a["design_reference_url"] + "\n")
	}
	if strings.TrimSpace(a["design_notes"]) != "" {
		b.WriteString("- Visual notes: " + a["design_notes"] + "\n")
	}
	b.WriteString("- If you later import more screens from Figma, Canva, or screenshots, keep this section updated so the next agent pass has the same visual contract.\n\n")

	if a["include_mobile"] == "true" {
		b.WriteString("## 3. Mobile permissions & review notes\n\n")
		perms := selectedMobilePermissions(a)
		if len(perms) == 0 {
			b.WriteString("- No runtime mobile permissions were selected during scaffold.\n")
		} else {
			b.WriteString("- Review `legal/app-review.md` before store submission. It contains the generated rationale text that now feeds Expo config, App Store review notes, and policy docs.\n")
			for _, spec := range perms {
				b.WriteString("- " + spec.Label + ": `" + permissionUsageText(a, spec.ID) + "`\n")
			}
		}
		b.WriteString("- If product scope changes later, update the permission toggles and rationale before shipping a build that requests anything new.\n\n")

		b.WriteString("## 3a. Store submission gates (iOS + Android)\n\n")
		b.WriteString("The scaffold wires up the pieces that the stores reject builds for when missing. Review each before your first submission:\n\n")
		b.WriteString("- **iOS Privacy Manifest** — `apps/mobile/ios/PrivacyInfo.xcprivacy`. Apple required since 2024-05-01. Extend `NSPrivacyAccessedAPITypes` for every required-reason API your SDKs actually call.\n")
		b.WriteString("- **Export compliance** — `ITSAppUsesNonExemptEncryption=false` is set in `app.json`. Flip it to `true` only if you ship custom non-exempt crypto.\n")
		if a["mobile_account_deletion"] == "true" {
			b.WriteString("- **Account deletion** — in-app screen at `apps/mobile/screens/DeleteAccount.tsx`, public URL at `/account/delete`. Both Apple (2022-06) and Play (2024-05) require a working flow.\n")
		} else {
			b.WriteString("- **Account deletion** — not scaffolded. Add one before submission; both stores require it.\n")
		}
		b.WriteString("- **App Privacy / Data Safety** — templates in `legal/app-privacy-nutrition.md` (App Store Connect) and `legal/play-data-safety.md` (Play Console). Paste, don't re-write.\n")
		if a["mobile_permission_tracking"] == "true" {
			b.WriteString("- **App Tracking Transparency** — enabled. The iOS prompt text is set; Google Play Data Safety calls this out as `AD_ID`.\n")
		}
		if a["mobile_permission_location_always"] == "true" {
			b.WriteString("- **Background location** — both stores review this manually. Add a prominent disclosure screen before the permission prompt.\n")
		}
		if a["audience_children"] == "true" {
			b.WriteString("- **Children** — COPPA copy is in the privacy policy. Complete the Families Policy / Kids Category declarations in both consoles.\n")
		}
		b.WriteString("- **Android target API** — Play requires the current API level (API 35 / Android 15 at time of scaffold). Keep `targetSdkVersion` up to date.\n\n")
	}

	// OAuth
	b.WriteString("## 4. OAuth providers\n\n")
	b.WriteString("The scaffold already includes stub wiring and `.env` placeholders for each provider you turn on here. You still need to create the provider app yourself and paste the real client IDs, secrets, and redirect URLs before production auth will work.\n\n")
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
		if a["backend"] == "sqlite" {
			b.WriteString("- No external signup; the starter ships a portable Yaver auth manifest and seed that can run phone-first or on your own hardware.\n\n")
		} else {
			b.WriteString("- No external signup; wire email auth into the selected backend before first deploy.\n\n")
		}
	}

	// Mobile
	if a["include_mobile"] == "true" && a["mobile_stack"] == "expo-rn" {
		b.WriteString("## 5. iOS TestFlight\n\n")
		if a["apple_team_id"] != "" {
			b.WriteString("- Team ID: `" + a["apple_team_id"] + "`\n")
		} else {
			b.WriteString("- Grab your Team ID from https://developer.apple.com/account (top right).\n")
		}
		b.WriteString("- https://appstoreconnect.apple.com/access/api — create an App Store Connect API key with Admin or App Manager role.\n")
		b.WriteString("- Save the .p8 file and note the Key ID + Issuer ID.\n")
		b.WriteString("- Put them in `.env` under `APP_STORE_KEY_PATH`, `APP_STORE_KEY_ID`, `APP_STORE_KEY_ISSUER`, `APPLE_TEAM_ID`.\n")
		b.WriteString("- Deploy with `./scripts/deploy.sh testflight`.\n\n")

		b.WriteString("## 6. Android Play Store\n\n")
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
		b.WriteString("## 7. Stripe\n\n")
		b.WriteString("- https://dashboard.stripe.com/apikeys — grab publishable + secret keys.\n")
		b.WriteString("- https://dashboard.stripe.com/webhooks — add `https://" + a["domain"] + "/api/stripe/webhook` and copy the signing secret.\n")
		b.WriteString("- Put them in `.env` under `STRIPE_PUBLIC_KEY`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`.\n\n")
	}

	b.WriteString("## 8. Privacy policy & terms\n\n")
	b.WriteString("- Generated source files live in `legal/privacy.md` and `legal/terms.md`.\n")
	if a["include_web"] == "true" {
		b.WriteString("- Web routes are prewired at `/privacy` and `/terms`.\n")
	}
	if a["include_landing"] == "true" {
		b.WriteString("- Landing-page copies are prewired at `/privacy.html` and `/terms.html`.\n")
	}
	b.WriteString("- In-app legal text is also embedded in the mobile starter so you can expose it before your real settings screen exists.\n")
	b.WriteString("- Review the generated text with counsel before production if your app processes regulated data, health data, payments, minors, or enterprise customer data.\n\n")

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
	if a["include_mobile"] == "true" {
		steps = append(steps, "Review legal/app-review.md and trim any permissions your first release does not truly need.")
		steps = append(steps, "Extend apps/mobile/ios/PrivacyInfo.xcprivacy with NSPrivacyAccessedAPITypes entries for every required-reason API your SDKs use.")
		steps = append(steps, "Copy legal/app-privacy-nutrition.md into App Store Connect and legal/play-data-safety.md into Play Console.")
		if a["mobile_account_deletion"] != "true" {
			steps = append(steps, "Add an account deletion flow before store submission — both Apple and Google Play reject builds without one.")
		}
	}
	if a["include_mobile"] == "true" || a["include_web"] == "true" || a["include_landing"] == "true" {
		steps = append(steps, "Review legal/privacy.md and legal/terms.md, then replace placeholders before launch.")
	}
	if a["design_source"] != "" && a["design_source"] != "prompt-only" {
		steps = append(steps, "Review the linked "+a["design_source"]+" reference and align the starter UI before generating more screens.")
	}
	if a["include_backend"] == "true" && a["backend"] == "sqlite" {
		steps = append(steps, "Review backend/schema.yaml, backend/auth.yaml, and backend/seed.json to shape the Yaver backend before first prompt-driven build.")
	}
	if a["include_backend"] == "true" && a["backend"] == "convex" {
		steps = append(steps, "Run `cd backend && npx convex dev` for local Convex during self-hosted vibing, then `cd backend && npx convex deploy --yes` when you want the hosted Convex backend.")
	}
	if a["web_host"] == "cloudflare" {
		if strings.TrimSpace(a["domain"]) != "" {
			steps = append(steps, "Add the Cloudflare zone for "+a["domain"]+" and swap nameservers at your registrar.")
		} else {
			steps = append(steps, "Pick a domain later, then add a Cloudflare zone and set the route in apps/web/wrangler.toml.")
		}
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
	deployScript := "echo 'self-host deploy not configured yet'"
	devDeps := []string{
		`"typescript": "^5.5.0"`,
	}
	if a["web_host"] == "cloudflare" {
		deployScript = "opennextjs-cloudflare && wrangler deploy"
		devDeps = append([]string{
			`"@opennextjs/cloudflare": "^0.5.0"`,
			`"wrangler": "^3.80.0"`,
		}, devDeps...)
	}
	return fmt.Sprintf(`{
  "name": "%s-web",
  "version": "0.0.1",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "deploy": %q
  },
  "dependencies": {
    "next": "^14.2.0",
    "react": "^18.3.0",
    "react-dom": "^18.3.0"
  },
  "devDependencies": {
    %s
  }
}
`, a["slug"], deployScript, strings.Join(devDeps, ",\n    "))
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
        <div style={{ marginTop: 18, display: "flex", gap: 14, justifyContent: "center", flexWrap: "wrap" }}>
          <a href="/privacy" style={{ color: "white" }}>Privacy</a>
          <a href="/terms" style={{ color: "white" }}>Terms</a>
          <a href=%q style={{ color: "white" }}>Contact</a>
        </div>
      </div>
    </main>
  );
}
`, a["primary_color"], a["app_name"], a["tagline"], a["accent_color"], "mailto:"+legalSupportEmail(a))
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
	if strings.TrimSpace(a["domain"]) == "" {
		return fmt.Sprintf(`name = "%s-web"
main = ".open-next/worker.js"
compatibility_date = "2024-09-23"
compatibility_flags = ["nodejs_compat"]
workers_dev = true
`, a["slug"])
	}
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
	var b strings.Builder
	fmt.Fprintf(&b, `// Convex HTTP actions that handle OAuth callbacks for %s.
// Providers enabled: %s.
//
// Each handler exchanges the provider authorization code for an
// access token, fetches the user profile, and upserts a row into
// the users table via the internal mutation upsertUserAndSession.
// On success the user is redirected to APP_URL with a session token
// in the fragment so the browser-side JS can pick it up.
//
// Required Convex env vars (set with `+"`npx convex env set <KEY> <VALUE>`"+`):
//   APP_URL                     — your front-end origin (e.g. https://app.example.com)
//   CONVEX_SITE_URL             — your Convex HTTP URL (auto-set when you deploy)
`, a["app_name"], describeAuth(a))

	if a["oauth_apple"] == "true" {
		b.WriteString("//   OAUTH_APPLE_CLIENT_ID       — Apple Services ID\n")
		b.WriteString("//   OAUTH_APPLE_CLIENT_SECRET   — Apple-signed JWT (rotate every 6 months)\n")
	}
	if a["oauth_google"] == "true" {
		b.WriteString("//   OAUTH_GOOGLE_CLIENT_ID      — Google OAuth client ID\n")
		b.WriteString("//   OAUTH_GOOGLE_CLIENT_SECRET  — Google OAuth client secret\n")
	}
	if a["oauth_microsoft"] == "true" {
		b.WriteString("//   OAUTH_MICROSOFT_CLIENT_ID   — Azure app registration client ID\n")
		b.WriteString("//   OAUTH_MICROSOFT_CLIENT_SECRET — Azure app secret\n")
		b.WriteString("//   OAUTH_MICROSOFT_TENANT      — tenant id (default: \"common\")\n")
	}

	b.WriteString(`import { httpRouter } from "convex/server";
import { httpAction, internalMutation } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";

const http = httpRouter();

// ── Shared helpers ──────────────────────────────────────────

function appUrl(): string {
  return process.env.APP_URL || "http://localhost:3000";
}

function siteUrl(): string {
  return process.env.CONVEX_SITE_URL || "";
}

function randomToken(): string {
  const buf = new Uint8Array(32);
  crypto.getRandomValues(buf);
  return Array.from(buf).map((b) => b.toString(16).padStart(2, "0")).join("");
}

function redirect(url: string): Response {
  return new Response(null, { status: 302, headers: { Location: url } });
}

function fail(message: string): Response {
  const u = new URL(appUrl());
  u.pathname = "/auth/error";
  u.searchParams.set("error", message);
  return redirect(u.toString());
}

async function exchangeJSON(url: string, body: URLSearchParams): Promise<any> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json" },
    body: body.toString(),
  });
  if (!res.ok) throw new Error("token exchange failed: " + res.status);
  return res.json();
}

async function fetchJSON(url: string, accessToken: string): Promise<any> {
  const res = await fetch(url, { headers: { Authorization: "Bearer " + accessToken } });
  if (!res.ok) throw new Error("userinfo failed: " + res.status);
  return res.json();
}

// ── Internal mutation: upsert user + session ────────────────

export const upsertUserAndSession = internalMutation({
  args: {
    email: v.string(),
    name: v.optional(v.string()),
    avatarUrl: v.optional(v.string()),
    provider: v.string(),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();
    const now = Date.now();
    let userId;
    if (existing) {
      await ctx.db.patch(existing._id, {
        name: args.name ?? existing.name,
        avatarUrl: args.avatarUrl ?? existing.avatarUrl,
        provider: args.provider,
      });
      userId = existing._id;
    } else {
      userId = await ctx.db.insert("users", {
        email: args.email,
        name: args.name,
        avatarUrl: args.avatarUrl,
        provider: args.provider,
        createdAt: now,
      });
    }
    const token = Array.from(crypto.getRandomValues(new Uint8Array(32)))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    await ctx.db.insert("sessions", {
      userId,
      token,
      createdAt: now,
      expiresAt: now + 30 * 24 * 60 * 60 * 1000, // 30 days
    });
    return token;
  },
});

async function completeAuth(
  ctx: any,
  email: string,
  name: string | undefined,
  avatarUrl: string | undefined,
  provider: string,
): Promise<Response> {
  if (!email) return fail("provider_missing_email");
  const token: string = await ctx.runMutation(internal.auth.upsertUserAndSession, {
    email, name, avatarUrl, provider,
  });
  const u = new URL(appUrl());
  u.pathname = "/auth/callback";
  u.hash = "token=" + token;
  return redirect(u.toString());
}
`)

	if a["oauth_google"] == "true" {
		b.WriteString(`
// ── Google ──────────────────────────────────────────────────

http.route({
  path: "/auth/callback/google",
  method: "GET",
  handler: httpAction(async (ctx, req) => {
    const code = new URL(req.url).searchParams.get("code");
    if (!code) return fail("google_missing_code");
    try {
      const tok = await exchangeJSON("https://oauth2.googleapis.com/token", new URLSearchParams({
        code,
        client_id: process.env.OAUTH_GOOGLE_CLIENT_ID || "",
        client_secret: process.env.OAUTH_GOOGLE_CLIENT_SECRET || "",
        redirect_uri: siteUrl() + "/auth/callback/google",
        grant_type: "authorization_code",
      }));
      const profile = await fetchJSON("https://openidconnect.googleapis.com/v1/userinfo", tok.access_token);
      return await completeAuth(ctx, profile.email, profile.name, profile.picture, "google");
    } catch (e: any) {
      return fail("google_oauth_failed");
    }
  }),
});
`)
	}

	if a["oauth_microsoft"] == "true" {
		b.WriteString(`
// ── Microsoft ───────────────────────────────────────────────

http.route({
  path: "/auth/callback/microsoft",
  method: "GET",
  handler: httpAction(async (ctx, req) => {
    const code = new URL(req.url).searchParams.get("code");
    if (!code) return fail("microsoft_missing_code");
    const tenant = process.env.OAUTH_MICROSOFT_TENANT || "common";
    try {
      const tok = await exchangeJSON("https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token", new URLSearchParams({
        code,
        client_id: process.env.OAUTH_MICROSOFT_CLIENT_ID || "",
        client_secret: process.env.OAUTH_MICROSOFT_CLIENT_SECRET || "",
        redirect_uri: siteUrl() + "/auth/callback/microsoft",
        grant_type: "authorization_code",
        scope: "openid email profile",
      }));
      const profile = await fetchJSON("https://graph.microsoft.com/oidc/userinfo", tok.access_token);
      return await completeAuth(ctx, profile.email, profile.name, undefined, "microsoft");
    } catch (e: any) {
      return fail("microsoft_oauth_failed");
    }
  }),
});
`)
	}

	if a["oauth_apple"] == "true" {
		b.WriteString(`
// ── Apple ───────────────────────────────────────────────────
// Apple posts back form-encoded data on the callback (POST), and
// returns the user's email inside the id_token JWT. We decode
// without verifying the signature here for simplicity — for
// production, fetch Apple's JWK set and verify the RS256
// signature against https://appleid.apple.com/auth/keys.

function decodeJwtPayload(jwt: string): any {
  const parts = jwt.split(".");
  if (parts.length !== 3) return null;
  const pad = (s: string) => s + "=".repeat((4 - (s.length % 4)) % 4);
  const b64 = pad(parts[1].replace(/-/g, "+").replace(/_/g, "/"));
  try { return JSON.parse(atob(b64)); } catch { return null; }
}

async function appleCallback(ctx: any, req: Request): Promise<Response> {
  let code = "", idToken = "";
  if (req.method === "POST") {
    const body = await req.text();
    const params = new URLSearchParams(body);
    code = params.get("code") || "";
    idToken = params.get("id_token") || "";
  } else {
    const u = new URL(req.url);
    code = u.searchParams.get("code") || "";
    idToken = u.searchParams.get("id_token") || "";
  }
  if (!code) return fail("apple_missing_code");
  try {
    const tok = await exchangeJSON("https://appleid.apple.com/auth/token", new URLSearchParams({
      code,
      client_id: process.env.OAUTH_APPLE_CLIENT_ID || "",
      client_secret: process.env.OAUTH_APPLE_CLIENT_SECRET || "",
      redirect_uri: siteUrl() + "/auth/callback/apple",
      grant_type: "authorization_code",
    }));
    const claims = decodeJwtPayload(tok.id_token || idToken);
    if (!claims?.email) return fail("apple_missing_email");
    return await completeAuth(ctx, claims.email, undefined, undefined, "apple");
  } catch (e: any) {
    return fail("apple_oauth_failed");
  }
}

http.route({ path: "/auth/callback/apple", method: "GET",  handler: httpAction(appleCallback) });
http.route({ path: "/auth/callback/apple", method: "POST", handler: httpAction(appleCallback) });
`)
	}

	if a["oauth_email"] == "true" {
		b.WriteString(`
// ── Email + password (simple scaffold) ─────────────────────
// Uses a basic SHA-256(salt + password) hash. For production,
// switch to bcrypt / argon2 — Convex actions can call npm
// packages. Treats this as a bootstrap pattern, not final.

http.route({
  path: "/auth/email/signup",
  method: "POST",
  handler: httpAction(async (ctx, req) => {
    const { email, password, name } = await req.json();
    if (!email || !password) return new Response("missing_fields", { status: 400 });
    const enc = new TextEncoder();
    const hash = await crypto.subtle.digest("SHA-256", enc.encode("yaver:" + password));
    const passwordHash = Array.from(new Uint8Array(hash)).map((b) => b.toString(16).padStart(2, "0")).join("");
    const token: string = await ctx.runMutation(internal.auth.upsertUserAndSession, {
      email, name, provider: "password",
    });
    return new Response(JSON.stringify({ token, passwordHash }), {
      headers: { "Content-Type": "application/json" },
    });
  }),
});
`)
	}

	b.WriteString(`
export default http;
`)
	return b.String()
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

func yaverBackendReadme(a map[string]string) string {
	return fmt.Sprintf("# %s backend\n\nYaver-native portable backend manifest.\n\n- `schema.yaml` defines the data model\n- `auth.yaml` defines the default personas\n- `seed.json` provides starter rows\n\nThis backend starts SQLite-first and is intended to move unchanged across:\n\n- phone\n- your hardware\n- Yaver Cloud\n\nGitHub, GitLab, OpenAI, Claude, Codex, Ollama, and similar tools are integrations around this backend, not the backend itself.\n", a["app_name"])
}

func yaverBackendSchema(a map[string]string) string {
	return fmt.Sprintf(`tables:
  - name: users
    columns:
      - name: id
        type: text
        primary: true
      - name: email
        type: text
        required: true
        unique: true
      - name: name
        type: text
      - name: created_at
        type: datetime
        required: true
        default: now
  - name: notes
    columns:
      - name: id
        type: text
        primary: true
      - name: user_id
        type: text
        required: true
      - name: title
        type: text
        required: true
      - name: body
        type: text
      - name: created_at
        type: datetime
        required: true
        default: now

meta:
  app_name: %q
  backend: sqlite
  portability: yaver-continuum
`, a["app_name"])
}

func yaverBackendAuth(a map[string]string) string {
	return fmt.Sprintf(`providers:
  email_password: %v
  apple: %v
  google: %v
  microsoft: %v

personas:
  - email: founder@%s.local
    password: demo-password
    role: owner
`, a["oauth_email"] == "true", a["oauth_apple"] == "true", a["oauth_google"] == "true", a["oauth_microsoft"] == "true", a["slug"])
}

func yaverBackendSeed(a map[string]string) string {
	return fmt.Sprintf(`{
  "users": [
    {
      "id": "owner",
      "email": "founder@%s.local",
      "name": "%s Founder",
      "created_at": "now"
    }
  ],
  "notes": [
    {
      "id": "welcome",
      "user_id": "owner",
      "title": "Welcome",
      "body": "This starter backend is portable across phone, your hardware, and Yaver Cloud.",
      "created_at": "now"
    }
  ]
}
`, a["slug"], a["app_name"])
}

func expoAppJSON(a map[string]string) string {
	// Always emit an infoPlist block. ITSAppUsesNonExemptEncryption=false
	// avoids the "Missing Compliance" TestFlight block on every upload
	// for apps that only use standard HTTPS / OS-provided crypto.
	// Change to true and add the YES export compliance code only if the
	// app ships custom / non-exempt encryption.
	infoPlistLines := []string{
		`        "ITSAppUsesNonExemptEncryption": false`,
	}
	for _, spec := range selectedMobilePermissions(a) {
		if spec.IOSKey == "" {
			continue
		}
		infoPlistLines = append(infoPlistLines, fmt.Sprintf(`        %q: %q`, spec.IOSKey, permissionUsageText(a, spec.ID)))
	}
	iosLines := []string{
		`      "bundleIdentifier": ` + jsQuoted(a["ios_bundle_id"]),
		`      "supportsTablet": true`,
		`      "infoPlist": {`,
		strings.Join(infoPlistLines, ",\n"),
		`      },`,
		`      "privacyManifests": {`,
		`        "NSPrivacyTracking": ` + boolLiteral(a["mobile_permission_tracking"] == "true") + `,`,
		`        "NSPrivacyAccessedAPITypes": []`,
		`      }`,
	}
	androidLines := []string{
		`      "package": ` + jsQuoted(a["android_package"]),
	}
	androidPerms := []string{}
	seenPerm := map[string]bool{}
	for _, spec := range selectedMobilePermissions(a) {
		for _, perm := range spec.AndroidPerms {
			if seenPerm[perm] {
				continue
			}
			seenPerm[perm] = true
			androidPerms = append(androidPerms, jsQuoted(perm))
		}
	}
	if len(androidPerms) > 0 {
		androidLines = append(androidLines, `      "permissions": [`+strings.Join(androidPerms, ", ")+`]`)
	}
	return fmt.Sprintf(`{
  "expo": {
    "name": "%s",
    "slug": "%s",
    "entryPoint": "./index.js",
    "version": "0.1.0",
    "orientation": "portrait",
    "userInterfaceStyle": "%s",
    "ios": {
%s
    },
    "android": {
%s
    },
    "newArchEnabled": true
  }
}
`, a["app_name"], a["slug"], a["tone"], strings.Join(iosLines, ",\n"), strings.Join(androidLines, ",\n"))
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
  "main": "./index.js",
  "scripts": {
    "prebuild": "expo prebuild",
    "start": "expo start",
    "ios": "cd ios && xcodebuild -workspace *.xcworkspace -scheme %s -configuration Debug",
    "android": "cd android && ./gradlew assembleDebug"
  },
  "dependencies": {
    "expo": "~52.0.0",
    "expo-status-bar": "~2.0.0",
    "react": "18.3.1",
    "react-native": "0.76.3"
  }
}
`, a["slug"], a["app_name"])
}

func expoIndexJS() string {
	return `import { registerRootComponent } from "expo";
import App from "./App";

registerRootComponent(App);
`
}

func expoAppTSX(a map[string]string) string {
	type mobilePermissionView struct {
		Label string `json:"label"`
		Usage string `json:"usage"`
	}
	permissions := []mobilePermissionView{}
	for _, spec := range selectedMobilePermissions(a) {
		permissions = append(permissions, mobilePermissionView{
			Label: spec.Label,
			Usage: permissionUsageText(a, spec.ID),
		})
	}
	permissionItems := []string{}
	for _, item := range permissions {
		permissionItems = append(permissionItems, fmt.Sprintf(`{ label: %q, usage: %q }`, item.Label, item.Usage))
	}
	return fmt.Sprintf(`import { StatusBar } from "expo-status-bar";
import { useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";

const APP_LANGUAGES = %s;
const NAV_ITEMS = %s;
const AUTH_PROVIDERS = %s;
const MOBILE_PERMISSIONS = [%s];
const DESIGN_SOURCE = %q;
const DESIGN_REFERENCE_URL = %q;
const DESIGN_NOTES = %q;
const LEGAL_ENTITY = %q;
const LEGAL_EMAIL = %q;
const LEGAL_JURISDICTION = %q;
const LEGAL_PRIVACY_NOTES = %q;

export default function App() {
  const [activeTab, setActiveTab] = useState(0);

  return (
    <View style={{ flex: 1, backgroundColor: "#020617" }}>
      <ScrollView contentContainerStyle={{ paddingHorizontal: 20, paddingTop: 72, paddingBottom: 132 }}>
        <View
          style={{
            borderRadius: 30,
            padding: 22,
            backgroundColor: "%s",
            shadowColor: "%s",
            shadowOpacity: 0.25,
            shadowRadius: 24,
            shadowOffset: { width: 0, height: 18 },
            elevation: 8,
          }}
        >
          <Text style={{ color: "#ffffff", fontSize: 12, fontWeight: "800", letterSpacing: 1.2 }}>
            MOBILE-FIRST STARTER
          </Text>
          <Text style={{ color: "#ffffff", fontSize: 34, fontWeight: "800", marginTop: 12 }}>
            %s
          </Text>
          <Text style={{ color: "rgba(255,255,255,0.88)", fontSize: 16, lineHeight: 23, marginTop: 8 }}>
            %s
          </Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 16 }}>
            <HeroPill label="%s" />
            <HeroPill label={"Nav: %s"} />
            <HeroPill label={"Backend: %s"} />
          </View>
        </View>

        <Section
          title="Builder preferences"
          subtitle="These are the product decisions captured in the phone-first setup flow."
        >
          <Card surfaceColor="%s">
            <Text style={styles.kicker}>Supported languages</Text>
            <View style={styles.rowWrap}>
              {APP_LANGUAGES.map((lang) => (
                <Token key={lang} label={lang} />
              ))}
            </View>
          </Card>
          <Card surfaceColor="%s">
            <Text style={styles.kicker}>Authentication</Text>
            <View style={styles.rowWrap}>
              {AUTH_PROVIDERS.length ? (
                AUTH_PROVIDERS.map((provider) => <Token key={provider} label={provider} />)
              ) : (
                <Text style={styles.body}>No auth providers selected yet.</Text>
              )}
            </View>
            <Text style={styles.caption}>
              Provider apps are stubbed only. Add the real client IDs, secrets, and redirect URLs in .env before release.
            </Text>
          </Card>
        </Section>

        <Section
          title="Permissions & policies"
          subtitle="Collected during initialization so native prompts, store review, privacy copy, and terms stay in sync."
        >
          <Card surfaceColor="%s">
            <Text style={styles.kicker}>Runtime permissions</Text>
            {MOBILE_PERMISSIONS.length ? (
              <View style={{ gap: 10 }}>
                {MOBILE_PERMISSIONS.map((item) => (
                  <View key={item.label} style={styles.permissionRow}>
                    <Text style={styles.permissionTitle}>{item.label}</Text>
                    <Text style={styles.caption}>{item.usage}</Text>
                  </View>
                ))}
              </View>
            ) : (
              <Text style={styles.body}>No extra runtime permissions were requested for the first release.</Text>
            )}
          </Card>
          <Card surfaceColor="%s">
            <Text style={styles.kicker}>Privacy & terms</Text>
            <Text style={styles.body}>Publisher: {LEGAL_ENTITY}</Text>
            <Text style={styles.body}>Contact: {LEGAL_EMAIL}</Text>
            <Text style={styles.caption}>Governing law: {LEGAL_JURISDICTION}</Text>
            {LEGAL_PRIVACY_NOTES ? <Text style={styles.caption}>{LEGAL_PRIVACY_NOTES}</Text> : null}
            <Text style={styles.caption}>
              Full starter copies are generated in legal/privacy.md and legal/terms.md and should be reviewed before launch.
            </Text>
          </Card>
        </Section>

        <Section
          title="Design intake"
          subtitle="Attach screenshots, Figma, or Canva references early so future generated screens stay closer to the intended look."
        >
          <Card surfaceColor="%s">
            <Text style={styles.kicker}>Reference source</Text>
            <Text style={styles.body}>
              {DESIGN_SOURCE === "prompt-only" ? "Prompt-only for now. No external reference linked yet." : DESIGN_SOURCE}
            </Text>
            {DESIGN_REFERENCE_URL ? <Text style={styles.link}>{DESIGN_REFERENCE_URL}</Text> : null}
            {DESIGN_NOTES ? <Text style={styles.caption}>{DESIGN_NOTES}</Text> : null}
          </Card>
        </Section>

        <Section
          title="Navigation preview"
          subtitle="Keep the first release opinionated and small. You can expand the IA once real usage data shows where it should bend."
        >
          <Card surfaceColor="%s">
            <View style={styles.navPreview}>
              {NAV_ITEMS.map((item, index) => (
                <Pressable
                  key={item}
                  onPress={() => setActiveTab(index)}
                  style={[
                    styles.navChip,
                    activeTab === index && { backgroundColor: "%s", borderColor: "%s" },
                  ]}
                >
                  <Text style={[styles.navText, activeTab === index && { color: "#08111f" }]}>{item}</Text>
                </Pressable>
              ))}
            </View>
            <Text style={styles.caption}>Active tab preview: {NAV_ITEMS[activeTab] ?? NAV_ITEMS[0] ?? "Home"}</Text>
          </Card>
        </Section>
      </ScrollView>

      <View style={[styles.bottomBar, { backgroundColor: "%s", borderColor: "%s" }]}>
        {NAV_ITEMS.map((item, index) => (
          <Pressable key={item} onPress={() => setActiveTab(index)} style={styles.bottomItem}>
            <Text
              style={[
                styles.bottomLabel,
                activeTab === index && { color: "%s", fontWeight: "800" },
              ]}
            >
              {item}
            </Text>
          </Pressable>
        ))}
      </View>
      <StatusBar style="light" />
    </View>
  );
}

function Section({ title, subtitle, children }) {
  return (
    <View style={{ marginTop: 26 }}>
      <Text style={styles.sectionTitle}>{title}</Text>
      <Text style={styles.sectionSubtitle}>{subtitle}</Text>
      <View style={{ gap: 12, marginTop: 14 }}>{children}</View>
    </View>
  );
}

function Card({ children, surfaceColor }) {
  return <View style={[styles.card, { backgroundColor: surfaceColor }]}>{children}</View>;
}

function HeroPill({ label }) {
  return (
    <View style={styles.heroPill}>
      <Text style={styles.heroPillText}>{label}</Text>
    </View>
  );
}

function Token({ label }) {
  return (
    <View style={styles.token}>
      <Text style={styles.tokenText}>{label}</Text>
    </View>
  );
}

const styles = {
  sectionTitle: { color: "#e2e8f0", fontSize: 22, fontWeight: "800" },
  sectionSubtitle: { color: "#94a3b8", fontSize: 14, marginTop: 6, lineHeight: 20 },
  card: {
    borderRadius: 24,
    padding: 18,
    borderWidth: 1,
    borderColor: "rgba(148,163,184,0.16)",
  },
  kicker: { color: "#f8fafc", fontSize: 15, fontWeight: "700", marginBottom: 10 },
  body: { color: "#dbe7f5", fontSize: 15, lineHeight: 22 },
  caption: { color: "#94a3b8", fontSize: 13, lineHeight: 19, marginTop: 10 },
  link: { color: "#7dd3fc", fontSize: 13, marginTop: 8 },
  rowWrap: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  heroPill: { backgroundColor: "rgba(255,255,255,0.14)", borderRadius: 999, paddingHorizontal: 12, paddingVertical: 8 },
  heroPillText: { color: "#ffffff", fontSize: 12, fontWeight: "700" },
  token: {
    backgroundColor: "rgba(255,255,255,0.06)",
    borderRadius: 999,
    paddingHorizontal: 11,
    paddingVertical: 7,
    borderWidth: 1,
    borderColor: "rgba(255,255,255,0.08)",
  },
  tokenText: { color: "#e2e8f0", fontSize: 12, fontWeight: "700" },
  permissionRow: {
    borderRadius: 16,
    padding: 12,
    backgroundColor: "rgba(255,255,255,0.04)",
    borderWidth: 1,
    borderColor: "rgba(255,255,255,0.08)",
  },
  permissionTitle: { color: "#f8fafc", fontSize: 14, fontWeight: "700" },
  navPreview: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  navChip: {
    borderRadius: 16,
    paddingHorizontal: 14,
    paddingVertical: 12,
    backgroundColor: "#0f172a",
    borderWidth: 1,
    borderColor: "rgba(148,163,184,0.16)",
  },
  navText: { color: "#cbd5e1", fontSize: 13, fontWeight: "700" },
  bottomBar: {
    position: "absolute",
    left: 16,
    right: 16,
    bottom: 24,
    borderRadius: 24,
    paddingHorizontal: 10,
    paddingVertical: 8,
    flexDirection: "row",
    justifyContent: "space-between",
    borderWidth: 1,
  },
  bottomItem: { flex: 1, alignItems: "center", paddingVertical: 8 },
  bottomLabel: { color: "#94a3b8", fontSize: 12, fontWeight: "700" },
};
`, jsStringArray(appLanguages(a)), jsStringArray(mobileNavLabels(a)), jsStringArray(authProviders(a)), strings.Join(permissionItems, ", "), a["design_source"], a["design_reference_url"], a["design_notes"], legalEntityName(a), legalSupportEmail(a), legalJurisdiction(a), privacyNotes(a), a["primary_color"], a["secondary_color"], a["app_name"], a["tagline"], a["app_template"], a["mobile_nav_style"], a["backend"], a["surface_color"], a["surface_color"], a["surface_color"], a["surface_color"], a["surface_color"], a["surface_color"], a["accent_color"], a["accent_color"], a["surface_color"], a["secondary_color"], a["accent_color"])
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

func privacyPolicyMarkdown(a map[string]string) string {
	var b strings.Builder
	b.WriteString("# Privacy Policy\n\n")
	b.WriteString("Last updated: " + time.Now().Format("2006-01-02") + "\n\n")
	b.WriteString("This Privacy Policy explains how " + legalEntityName(a) + " collects, uses, and protects information when you use " + a["app_name"] + ". If you have questions, contact " + legalSupportEmail(a) + ".\n\n")
	b.WriteString("## Information we collect\n\n")
	b.WriteString("- Account data: identifiers such as name, email address, and authentication provider details.\n")
	if len(selectedMobilePermissions(a)) == 0 {
		b.WriteString("- Device data: standard diagnostics and session metadata needed to operate the service.\n")
	} else {
		for _, spec := range selectedMobilePermissions(a) {
			b.WriteString("- " + spec.Label + ": " + permissionUsageText(a, spec.ID) + " This may include " + spec.PrivacySummary + ".\n")
		}
	}
	b.WriteString("- Usage data: logs, crash reports, and interactions needed to secure, operate, and improve the product.\n\n")
	b.WriteString("## How we use information\n\n")
	b.WriteString("- To provide core app features, authenticate users, sync data, and support your requests.\n")
	b.WriteString("- To maintain security, prevent abuse, and troubleshoot issues.\n")
	b.WriteString("- To communicate service updates, support responses, and operational notices.\n")
	if a["mobile_permission_notifications"] == "true" {
		b.WriteString("- To send notifications that you have opted into: " + permissionUsageText(a, "mobile_permission_notifications") + "\n")
	}
	b.WriteString("\n## Sharing\n\n")
	b.WriteString("- We may share data with infrastructure, analytics, authentication, and payment providers only as needed to run the service.\n")
	b.WriteString("- We may disclose information if required by law, to enforce our terms, or to protect users and the service.\n\n")
	b.WriteString("## Retention and security\n\n")
	b.WriteString("- We retain information for as long as needed to provide the service, comply with legal obligations, resolve disputes, and enforce agreements.\n")
	b.WriteString("- We use reasonable administrative, technical, and organizational safeguards, but no system is perfectly secure.\n\n")
	b.WriteString("## Your choices\n\n")
	b.WriteString("- You can request access, correction, export, or deletion by contacting " + legalSupportEmail(a) + ".\n")
	b.WriteString("- You can disable optional device permissions in system settings at any time, but related features may stop working.\n\n")
	// Account deletion — Apple (June 2022) and Google Play (May 2024)
	// require this section and a working deletion path both in-app
	// and at a public URL.
	if a["mobile_account_deletion"] == "true" {
		b.WriteString("## Account deletion\n\n")
		b.WriteString("- You can delete your account from inside the app at Settings &rarr; Account &rarr; Delete account, or from the web at ")
		if a["include_web"] == "true" || a["include_landing"] == "true" {
			if strings.TrimSpace(a["domain"]) != "" {
				b.WriteString("https://" + a["domain"] + "/account/delete")
			} else {
				b.WriteString("your public /account/delete page")
			}
			b.WriteString(".\n")
		} else {
			b.WriteString("by emailing " + legalSupportEmail(a) + ".\n")
		}
		b.WriteString("- Deletion removes your profile, authentication identities, content, and any personal identifiers tied to your account.\n")
		b.WriteString("- Invoices, tax records, abuse logs, and other legally required data may be retained for the minimum period required by law.\n")
		b.WriteString("- Deletion requests are actioned within 30 days.\n\n")
	}
	b.WriteString("## Your rights under GDPR (EU / UK / EEA users)\n\n")
	b.WriteString("- You have the right to access, rectify, erase, restrict, object to processing, and port your personal data.\n")
	b.WriteString("- You can lodge a complaint with your local data protection authority.\n")
	b.WriteString("- The legal basis for processing is performance of contract, legitimate interest (security, product operation), or consent (optional permissions and tracking).\n")
	b.WriteString("- International transfers, if any, rely on Standard Contractual Clauses or equivalent safeguards.\n\n")
	b.WriteString("## Your rights under CCPA / CPRA (California residents)\n\n")
	b.WriteString("- You have the right to know what personal information we collect, to delete it, to correct it, and to opt out of any sale or sharing.\n")
	b.WriteString("- We do not sell personal information. ")
	if a["mobile_permission_tracking"] == "true" || a["mobile_data_collection"] == "tracking" {
		b.WriteString("If we share advertising identifiers for cross-app measurement, you can opt out via the App Tracking Transparency prompt on iOS and the \"Delete advertising ID\" setting on Android.\n\n")
	} else {
		b.WriteString("We do not engage in cross-context behavioral advertising.\n\n")
	}
	if a["audience_children"] == "true" {
		b.WriteString("## Children (COPPA)\n\n")
		b.WriteString("- This app is directed at children. We comply with the Children's Online Privacy Protection Act (COPPA).\n")
		b.WriteString("- We do not knowingly collect more information from children than is reasonably necessary to operate the app.\n")
		b.WriteString("- Parents or legal guardians can contact " + legalSupportEmail(a) + " to review, delete, or refuse further collection of their child's information.\n")
		b.WriteString("- We do not enable advertising identifiers or third-party tracking for accounts flagged as child accounts.\n\n")
	} else {
		b.WriteString("## Children\n\n")
		b.WriteString("- The app is not directed at children under 13 (or under the age required in your jurisdiction). If you believe a child has provided us personal information, contact " + legalSupportEmail(a) + " and we will delete it.\n\n")
	}
	if a["mobile_permission_tracking"] == "true" || a["mobile_data_collection"] == "tracking" {
		b.WriteString("## App Tracking Transparency (iOS) and Advertising ID (Android)\n\n")
		b.WriteString("- On iOS, we will prompt you with the App Tracking Transparency dialog before accessing the IDFA.\n")
		b.WriteString("- On Android, we request the `com.google.android.gms.permission.AD_ID` permission only when advertising features are enabled.\n")
		b.WriteString("- You can revoke this permission at any time in system settings; the rest of the app will continue to work.\n\n")
	}
	if privacyNotes(a) != "" {
		b.WriteString("## Additional notes\n\n")
		b.WriteString(privacyNotes(a) + "\n\n")
	}
	b.WriteString("## Governing law and contact\n\n")
	b.WriteString("This policy is governed by the laws of " + legalJurisdiction(a) + ". Contact: " + legalSupportEmail(a) + ".\n")
	return b.String()
}

func termsMarkdown(a map[string]string) string {
	return fmt.Sprintf(`# Terms and Conditions

Last updated: %s

These Terms and Conditions govern your use of %s, operated by %s. By accessing or using the service, you agree to these terms. If you do not agree, do not use the service.

## Use of the service

- You must use the service lawfully and only for legitimate purposes.
- You are responsible for the activity that occurs under your account.
- You must not interfere with the service, reverse engineer restricted components, or use the service to harm others.

## Accounts and content

- You are responsible for keeping account credentials secure.
- You retain ownership of content you submit, but you grant the service the limited rights needed to host, process, and transmit that content to operate the product.
- You represent that you have the rights needed to upload and process your content.

## Acceptable use

- No abuse, fraud, infringement, spam, malware, unauthorized access attempts, or illegal content.
- We may suspend or terminate access if use creates security, legal, operational, or reputational risk.

## Availability and changes

- The service may change over time. Features may be added, modified, or removed.
- We do not promise uninterrupted or error-free availability.

## Disclaimers

- The service is provided on an "as is" and "as available" basis to the extent permitted by law.
- Except where required by law, %s disclaims warranties of merchantability, fitness for a particular purpose, and non-infringement.

## Limitation of liability

- To the maximum extent permitted by law, %s will not be liable for indirect, incidental, special, consequential, exemplary, or punitive damages, or for lost profits, revenue, data, or goodwill.
- Total liability for claims arising from the service will not exceed the amounts paid by you to use the service during the 12 months before the claim, or USD 100 if no fees were paid.

## Governing law

These terms are governed by the laws of %s, without regard to conflict-of-law principles.

## Contact

Questions about these terms can be sent to %s.
`, time.Now().Format("2006-01-02"), a["app_name"], legalEntityName(a), legalEntityName(a), legalEntityName(a), legalJurisdiction(a), legalSupportEmail(a))
}

func appReviewLegalChecklist(a map[string]string) string {
	var b strings.Builder
	b.WriteString("# App Review Checklist\n\n")
	b.WriteString("Generated: " + time.Now().Format("2006-01-02") + "\n\n")
	b.WriteString("## Mobile permissions\n\n")
	perms := selectedMobilePermissions(a)
	if len(perms) == 0 {
		b.WriteString("- No extra runtime permissions were selected.\n")
	} else {
		for _, spec := range perms {
			b.WriteString("- " + spec.Label + "\n")
			b.WriteString("  - Reason: " + permissionUsageText(a, spec.ID) + "\n")
			if spec.IOSKey != "" {
				b.WriteString("  - iOS key: " + spec.IOSKey + "\n")
			}
			if len(spec.AndroidPerms) > 0 {
				b.WriteString("  - Android: " + strings.Join(spec.AndroidPerms, ", ") + "\n")
			}
		}
	}
	b.WriteString("\n## Policy surfaces\n\n")
	b.WriteString("- Source docs: legal/privacy.md and legal/terms.md\n")
	if a["include_web"] == "true" {
		b.WriteString("- Web: /privacy and /terms\n")
	}
	if a["include_landing"] == "true" {
		b.WriteString("- Landing: /privacy.html and /terms.html\n")
	}
	if a["include_mobile"] == "true" {
		b.WriteString("- In-app: starter legal section in apps/mobile/App.tsx\n")
	}
	// 2026 store-submission items — each one blocks a real submission
	// if missing. Keep this list ordered by blast radius.
	if a["include_mobile"] == "true" {
		b.WriteString("\n## iOS submission gates\n\n")
		b.WriteString("- [x] Privacy Manifest generated at `apps/mobile/ios/PrivacyInfo.xcprivacy` — required since 2024-05-01.\n")
		b.WriteString("  - Extend `NSPrivacyAccessedAPITypes` for every required-reason API your SDKs use before first submission.\n")
		b.WriteString("  - If your app uses tracking, list domains under `NSPrivacyTrackingDomains`.\n")
		b.WriteString("- [x] Export compliance: `ITSAppUsesNonExemptEncryption=false` is set in `apps/mobile/app.json`. Change to `true` if the app ships non-exempt custom crypto, and fill in the export code in App Store Connect.\n")
		if a["mobile_account_deletion"] == "true" {
			b.WriteString("- [x] Account deletion — in-app screen at `apps/mobile/screens/DeleteAccount.tsx`, public URL at /account/delete. Apple has required this since 2022-06-30.\n")
		} else {
			b.WriteString("- [ ] Account deletion — **NOT GENERATED**. Apple will reject the build. Re-run the wizard with `mobile_account_deletion=true` or implement manually.\n")
		}
		if a["mobile_permission_tracking"] == "true" {
			b.WriteString("- [x] App Tracking Transparency prompt text: " + permissionUsageText(a, "mobile_permission_tracking") + "\n")
		}
		b.WriteString("- [ ] Fill the App Privacy nutrition label in App Store Connect — template at `legal/app-privacy-nutrition.md`.\n")
		b.WriteString("- [ ] Age rating, support URL, marketing URL, privacy policy URL on the App Store Connect listing.\n")

		b.WriteString("\n## Android / Play Store submission gates\n\n")
		b.WriteString("- [ ] Data safety form — paste template from `legal/play-data-safety.md` into Play Console.\n")
		if a["mobile_account_deletion"] == "true" {
			b.WriteString("- [x] Account deletion URL published at `/account/delete`. Paste the full URL into Play Console &rarr; App content &rarr; Data deletion. Required since 2024-05-31.\n")
		}
		if a["mobile_permission_location_always"] == "true" {
			b.WriteString("- [ ] Prominent disclosure + declaration form for background location. Play reviews this manually.\n")
		}
		if a["mobile_permission_tracking"] == "true" {
			b.WriteString("- [ ] Advertising ID declaration — confirm `com.google.android.gms.permission.AD_ID` in the manifest matches the declared use in Data Safety.\n")
		}
		b.WriteString("- [ ] Target API level must match current Play requirements (currently API 35, Android 15).\n")
		if a["audience_children"] == "true" {
			b.WriteString("- [ ] Complete the Families Policy / Designed for Families declaration.\n")
		}
	}
	b.WriteString("\n## Contacts\n\n")
	b.WriteString("- Publisher: " + legalEntityName(a) + "\n")
	b.WriteString("- Contact: " + legalSupportEmail(a) + "\n")
	b.WriteString("- Jurisdiction: " + legalJurisdiction(a) + "\n")
	return b.String()
}

// iOSPrivacyManifest emits a PrivacyInfo.xcprivacy file (plist XML)
// that Apple has required for every app submitted since 2024-05-01.
// We only set the tracking flag. Required-reason APIs (UserDefaults,
// file timestamps, disk space, system boot time, active keyboards)
// should be declared by the specific SDK that uses them — we cannot
// infer this from wizard answers. The project owner should extend
// NSPrivacyAccessedAPITypes before submission.
func iOSPrivacyManifest(a map[string]string) string {
	tracking := "false"
	if a["mobile_permission_tracking"] == "true" {
		tracking = "true"
	}
	collected := ""
	profile := a["mobile_data_collection"]
	if profile == "" {
		profile = "minimal"
	}
	// Map the collection profile + auth on/off to nutrition-label entries.
	anyAuth := a["oauth_apple"] == "true" || a["oauth_google"] == "true" || a["oauth_microsoft"] == "true" || a["oauth_email"] == "true"
	entries := []string{}
	if anyAuth {
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeEmailAddress", false, false, []string{"NSPrivacyCollectedDataTypePurposeAppFunctionality"}))
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeName", false, false, []string{"NSPrivacyCollectedDataTypePurposeAppFunctionality"}))
	}
	if profile == "minimal" || profile == "standard" || profile == "tracking" {
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeCrashData", false, false, []string{"NSPrivacyCollectedDataTypePurposeAppFunctionality"}))
	}
	if profile == "standard" || profile == "tracking" {
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeProductInteraction", false, false, []string{"NSPrivacyCollectedDataTypePurposeAnalytics"}))
	}
	if profile == "tracking" {
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeAdvertisingData", false, true, []string{"NSPrivacyCollectedDataTypePurposeThirdPartyAdvertising"}))
	}
	if a["mobile_permission_location"] == "true" {
		entries = append(entries, plistCollectedType("NSPrivacyCollectedDataTypeCoarseLocation", false, false, []string{"NSPrivacyCollectedDataTypePurposeAppFunctionality"}))
	}
	if len(entries) == 0 {
		collected = "\t<array/>"
	} else {
		collected = "\t<array>\n" + strings.Join(entries, "\n") + "\n\t</array>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>NSPrivacyTracking</key>
	<%s/>
	<key>NSPrivacyTrackingDomains</key>
	<array/>
	<key>NSPrivacyCollectedDataTypes</key>
%s
	<key>NSPrivacyAccessedAPITypes</key>
	<array>
		<!--
		  Add one dict per required-reason API your SDKs use. Common examples:
		    NSPrivacyAccessedAPICategoryUserDefaults → CA92.1
		    NSPrivacyAccessedAPICategoryFileTimestamp → C617.1
		    NSPrivacyAccessedAPICategorySystemBootTime → 35F9.1
		    NSPrivacyAccessedAPICategoryDiskSpace → E174.1
		  See: https://developer.apple.com/documentation/bundleresources/privacy_manifest_files/describing_use_of_required_reason_api
		-->
	</array>
</dict>
</plist>
`, tracking, collected)
}

func plistCollectedType(dataType string, linked, tracking bool, purposes []string) string {
	linkedVal := "false"
	if linked {
		linkedVal = "true"
	}
	trackingVal := "false"
	if tracking {
		trackingVal = "true"
	}
	purposeItems := make([]string, 0, len(purposes))
	for _, p := range purposes {
		purposeItems = append(purposeItems, "\t\t\t\t<string>"+p+"</string>")
	}
	return fmt.Sprintf(`		<dict>
			<key>NSPrivacyCollectedDataType</key>
			<string>%s</string>
			<key>NSPrivacyCollectedDataTypeLinked</key>
			<%s/>
			<key>NSPrivacyCollectedDataTypeTracking</key>
			<%s/>
			<key>NSPrivacyCollectedDataTypePurposes</key>
			<array>
%s
			</array>
		</dict>`, dataType, linkedVal, trackingVal, strings.Join(purposeItems, "\n"))
}

func mobileDeleteAccountScreen(a map[string]string) string {
	return fmt.Sprintf(`import { useState } from "react";
import { Alert, Pressable, ScrollView, Text, View } from "react-native";

// In-app account deletion flow. Apple (June 2022) and Google Play
// (May 2024) require any app that creates accounts to let the user
// delete their account from inside the app as well as via a public
// web URL (see apps/web/app/account/delete). Wire the onConfirm
// handler to your backend's delete endpoint before shipping.
const SUPPORT_EMAIL = %q;
const APP_NAME = %q;

export default function DeleteAccountScreen() {
  const [confirmText, setConfirmText] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const onConfirm = async () => {
    if (confirmText.trim().toUpperCase() !== "DELETE") {
      Alert.alert("Type DELETE to confirm");
      return;
    }
    setSubmitting(true);
    try {
      // TODO: call your backend's delete-account endpoint here.
      // await fetch("/api/account", { method: "DELETE" });
      Alert.alert("Request received", "Your account will be removed within 30 days.");
    } catch (err) {
      Alert.alert("Error", String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <ScrollView contentContainerStyle={{ padding: 20, backgroundColor: "#0f172a", minHeight: "100%%" }}>
      <Text style={{ color: "#fff", fontSize: 28, fontWeight: "700" }}>Delete your {APP_NAME} account</Text>
      <Text style={{ color: "rgba(255,255,255,0.7)", marginTop: 12, lineHeight: 22 }}>
        Deleting your account permanently removes your profile, your content, and any data linked to your identity.
        Some records (invoices, abuse logs, legally required data) may be retained for up to the period required by law.
      </Text>
      <Text style={{ color: "rgba(255,255,255,0.7)", marginTop: 12, lineHeight: 22 }}>
        If you just want to stop receiving notifications, you can turn them off in Settings instead.
      </Text>
      <Text style={{ color: "#fff", marginTop: 24, fontWeight: "600" }}>Type DELETE to confirm</Text>
      <Text
        style={{
          marginTop: 8,
          padding: 12,
          borderRadius: 12,
          borderColor: "rgba(255,255,255,0.2)",
          borderWidth: 1,
          color: "#fff",
        }}
      >
        {confirmText || "DELETE"}
      </Text>
      <Pressable
        onPress={onConfirm}
        disabled={submitting}
        style={{ marginTop: 24, padding: 16, borderRadius: 12, backgroundColor: "#dc2626", opacity: submitting ? 0.6 : 1 }}
      >
        <Text style={{ color: "#fff", textAlign: "center", fontWeight: "700" }}>Delete my account</Text>
      </Pressable>
      <Text style={{ color: "rgba(255,255,255,0.5)", marginTop: 16, fontSize: 12 }}>
        Need help? Email {SUPPORT_EMAIL}.
      </Text>
    </ScrollView>
  );
}
`, legalSupportEmail(a), a["app_name"])
}

func webAccountDeletePage(a map[string]string) string {
	return fmt.Sprintf(`export default function DeleteAccountPage() {
  return (
    <main style={{ maxWidth: 720, margin: "0 auto", padding: "48px 20px 72px", color: "#e5e7eb" }}>
      <h1 style={{ fontSize: 40 }}>Delete your %s account</h1>
      <p style={{ lineHeight: 1.6 }}>
        You can delete your %s account and associated data at any time. Google Play and the App Store require this URL
        to be available without signing up.
      </p>
      <h2 style={{ marginTop: 32 }}>From inside the app</h2>
      <ol style={{ lineHeight: 1.7 }}>
        <li>Open %s on your device.</li>
        <li>Go to <strong>Settings &rarr; Account &rarr; Delete account</strong>.</li>
        <li>Confirm by typing DELETE and tapping the red button.</li>
      </ol>
      <h2 style={{ marginTop: 32 }}>By email</h2>
      <p style={{ lineHeight: 1.6 }}>
        Send a deletion request from the email address on your account to{" "}
        <a href="mailto:%s" style={{ color: "#7dd3fc" }}>%s</a>. We will confirm and action the request within 30 days.
      </p>
      <h2 style={{ marginTop: 32 }}>What gets deleted</h2>
      <ul style={{ lineHeight: 1.7 }}>
        <li>Your profile, authentication identities, and any personal identifiers.</li>
        <li>Content you have created, uploaded, or saved in your account.</li>
        <li>Device tokens, sessions, and any cached data tied to your identity.</li>
      </ul>
      <h2 style={{ marginTop: 32 }}>What may be retained</h2>
      <ul style={{ lineHeight: 1.7 }}>
        <li>Invoices, tax records, and other legally required data.</li>
        <li>Abuse / security logs for the minimum retention window.</li>
      </ul>
      <p style={{ marginTop: 32, color: "rgba(255,255,255,0.6)" }}>
        Governed by the laws of %s. Contact <a href="mailto:%s" style={{ color: "#7dd3fc" }}>%s</a> for any escalation.
      </p>
    </main>
  );
}
`, a["app_name"], a["app_name"], a["app_name"], legalSupportEmail(a), legalSupportEmail(a), legalJurisdiction(a), legalSupportEmail(a), legalSupportEmail(a))
}

func landingAccountDeleteHTML(a map[string]string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>%s · Delete account</title>
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: #0f172a; color: #e5e7eb; }
      main { max-width: 720px; margin: 0 auto; padding: 48px 20px 72px; }
      h1 { font-size: 36px; }
      a { color: #7dd3fc; }
      ul, ol { line-height: 1.7; }
    </style>
  </head>
  <body>
    <main>
      <h1>Delete your %s account</h1>
      <p>You can delete your %s account and associated data at any time.</p>
      <h2>From inside the app</h2>
      <ol>
        <li>Open %s on your device.</li>
        <li>Go to Settings &rarr; Account &rarr; Delete account.</li>
        <li>Confirm by typing DELETE.</li>
      </ol>
      <h2>By email</h2>
      <p>Email <a href="mailto:%s">%s</a> from the address on your account. Requests are actioned within 30 days.</p>
      <p><a href="/">Back to home</a></p>
    </main>
  </body>
</html>
`, a["app_name"], a["app_name"], a["app_name"], a["app_name"], legalSupportEmail(a), legalSupportEmail(a))
}

func playDataSafetyMarkdown(a map[string]string) string {
	profile := a["mobile_data_collection"]
	if profile == "" {
		profile = "minimal"
	}
	var b strings.Builder
	b.WriteString("# Google Play Data Safety\n\n")
	b.WriteString("Paste these answers into Play Console &rarr; Policy &rarr; App content &rarr; Data safety. ")
	b.WriteString("Keep this file in sync with the privacy policy at `legal/privacy.md`.\n\n")
	b.WriteString("## Data collection summary\n\n")
	b.WriteString("- Profile chosen at scaffold: **" + profile + "**\n")
	b.WriteString("- Data encrypted in transit: **Yes** (HTTPS to all endpoints)\n")
	b.WriteString("- Users can request deletion: **Yes** — see account-deletion flow at `/account/delete`\n\n")
	b.WriteString("## Data types collected\n\n")
	anyAuth := a["oauth_apple"] == "true" || a["oauth_google"] == "true" || a["oauth_microsoft"] == "true" || a["oauth_email"] == "true"
	rows := [][]string{
		{"Data type", "Collected?", "Shared?", "Purpose", "Optional?"},
	}
	if anyAuth {
		rows = append(rows, []string{"Name", "Yes", "No", "Account management", "No"})
		rows = append(rows, []string{"Email address", "Yes", "No", "Account management, support", "No"})
		rows = append(rows, []string{"User IDs", "Yes", "No", "Account management", "No"})
	}
	if profile == "minimal" || profile == "standard" || profile == "tracking" {
		rows = append(rows, []string{"Crash logs", "Yes", "No", "App functionality, analytics", "Yes"})
	}
	if profile == "standard" || profile == "tracking" {
		rows = append(rows, []string{"App interactions", "Yes", "No", "Analytics, app functionality", "Yes"})
	}
	if profile == "tracking" {
		rows = append(rows, []string{"Advertising ID (AD_ID)", "Yes", "Yes", "Advertising or marketing", "Yes"})
	}
	if a["mobile_permission_location"] == "true" {
		rows = append(rows, []string{"Approximate location", "Yes", "No", "App functionality", "Yes"})
	}
	if a["mobile_permission_location_always"] == "true" {
		rows = append(rows, []string{"Precise location", "Yes", "No", "App functionality (background)", "Yes"})
	}
	if a["mobile_permission_photos"] == "true" {
		rows = append(rows, []string{"Photos and videos", "Yes (on user action)", "No", "App functionality", "Yes"})
	}
	if a["mobile_permission_microphone"] == "true" {
		rows = append(rows, []string{"Voice or sound recordings", "Yes (on user action)", "No", "App functionality", "Yes"})
	}
	for _, row := range rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if row[0] == "Data type" {
			b.WriteString("|" + strings.Repeat("---|", len(row)) + "\n")
		}
	}
	b.WriteString("\n## Security practices\n\n")
	b.WriteString("- Data is encrypted in transit.\n")
	b.WriteString("- Users can request that their data be deleted.\n")
	b.WriteString("- The app follows the Families Policy: **" + boolLiteral(a["audience_children"] == "true") + "**\n")
	b.WriteString("- Independent security review: answer in Play Console based on your own audits.\n\n")
	b.WriteString("## Account deletion\n\n")
	b.WriteString("- Publish and paste: `https://" + a["domain"] + "/account/delete` into Play Console &rarr; App content &rarr; Data deletion. Required since 2024-05-31.\n")
	b.WriteString("- In-app entry point: Settings &rarr; Account &rarr; Delete account (see `apps/mobile/screens/DeleteAccount.tsx`).\n")
	return b.String()
}

func applePrivacyNutritionMarkdown(a map[string]string) string {
	profile := a["mobile_data_collection"]
	if profile == "" {
		profile = "minimal"
	}
	var b strings.Builder
	b.WriteString("# Apple App Privacy — Nutrition Label\n\n")
	b.WriteString("Paste these answers into App Store Connect &rarr; App Privacy. ")
	b.WriteString("Keep this file in sync with `ios/PrivacyInfo.xcprivacy` and `legal/privacy.md`.\n\n")
	b.WriteString("## Tracking\n\n")
	if a["mobile_permission_tracking"] == "true" {
		b.WriteString("- **Yes — this app uses data to track you.** Prompt text: " + permissionUsageText(a, "mobile_permission_tracking") + "\n")
		b.WriteString("- Tracking domains must be listed in `ios/PrivacyInfo.xcprivacy` under `NSPrivacyTrackingDomains`.\n")
	} else {
		b.WriteString("- **No — this app does not track users across apps and websites owned by other companies.**\n")
	}
	b.WriteString("\n## Data used to track you\n\n")
	if a["mobile_permission_tracking"] == "true" {
		b.WriteString("- Identifiers &rarr; Device ID (IDFA), when the user allows tracking via ATT.\n")
		b.WriteString("- Usage data &rarr; Product Interaction, when the user allows tracking.\n")
	} else {
		b.WriteString("- None.\n")
	}
	b.WriteString("\n## Data linked to you\n\n")
	anyAuth := a["oauth_apple"] == "true" || a["oauth_google"] == "true" || a["oauth_microsoft"] == "true" || a["oauth_email"] == "true"
	if anyAuth {
		b.WriteString("- Contact Info &rarr; Email Address — purpose: App Functionality.\n")
		b.WriteString("- Contact Info &rarr; Name — purpose: App Functionality.\n")
		b.WriteString("- Identifiers &rarr; User ID — purpose: App Functionality.\n")
	}
	if profile == "standard" || profile == "tracking" {
		b.WriteString("- Usage Data &rarr; Product Interaction — purpose: Analytics.\n")
	}
	if a["mobile_permission_location"] == "true" {
		b.WriteString("- Location &rarr; Coarse Location — purpose: App Functionality.\n")
	}
	if a["mobile_permission_location_always"] == "true" {
		b.WriteString("- Location &rarr; Precise Location — purpose: App Functionality (background).\n")
	}
	b.WriteString("\n## Data not linked to you\n\n")
	if profile == "minimal" || profile == "standard" || profile == "tracking" {
		b.WriteString("- Diagnostics &rarr; Crash Data — purpose: App Functionality.\n")
	} else {
		b.WriteString("- None declared.\n")
	}
	b.WriteString("\n## Children\n\n")
	if a["audience_children"] == "true" {
		b.WriteString("- App is directed at children. Review the Kids Category review notes and age-gating flow before release.\n")
	} else {
		b.WriteString("- App is not directed at children.\n")
	}
	b.WriteString("\n## Data deletion\n\n")
	b.WriteString("- In-app deletion: apps/mobile/screens/DeleteAccount.tsx (Settings &rarr; Account &rarr; Delete).\n")
	b.WriteString("- Public URL: https://" + a["domain"] + "/account/delete\n")
	return b.String()
}

func privacyPolicyHTMLBody(a map[string]string) string {
	return strings.ReplaceAll(strings.ReplaceAll(privacyPolicyMarkdown(a), "\n\n", "</p><p>"), "\n", "<br/>")
}

func termsHTMLBody(a map[string]string) string {
	return strings.ReplaceAll(strings.ReplaceAll(termsMarkdown(a), "\n\n", "</p><p>"), "\n", "<br/>")
}

func landingPolicyHTML(a map[string]string, title, body string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>%s · %s</title>
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: #0f172a; color: white; }
      main { max-width: 880px; margin: 0 auto; padding: 48px 20px 72px; }
      article { background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); border-radius: 24px; padding: 24px; line-height: 1.7; }
      a { color: #7dd3fc; }
    </style>
  </head>
  <body>
    <main>
      <h1>%s</h1>
      <article><p>%s</p></article>
      <p><a href="/">Back to home</a></p>
    </main>
  </body>
</html>
`, a["app_name"], title, title, body)
}

func nextjsPrivacyPage(a map[string]string) string {
	return fmt.Sprintf(`export default function PrivacyPage() {
  return (
    <main style={{ maxWidth: 880, margin: "0 auto", padding: "48px 20px 72px", color: "#e5e7eb" }}>
      <h1 style={{ fontSize: 40 }}>Privacy Policy</h1>
      <div style={{ marginTop: 20, borderRadius: 24, padding: 24, background: "#111827", lineHeight: 1.7, whiteSpace: "pre-wrap" }}>
        {%s}
      </div>
    </main>
  );
}
`, jsQuoted(privacyPolicyMarkdown(a)))
}

func nextjsTermsPage(a map[string]string) string {
	return fmt.Sprintf(`export default function TermsPage() {
  return (
    <main style={{ maxWidth: 880, margin: "0 auto", padding: "48px 20px 72px", color: "#e5e7eb" }}>
      <h1 style={{ fontSize: 40 }}>Terms & Conditions</h1>
      <div style={{ marginTop: 20, borderRadius: 24, padding: 24, background: "#111827", lineHeight: 1.7, whiteSpace: "pre-wrap" }}>
        {%s}
      </div>
    </main>
  );
}
`, jsQuoted(termsMarkdown(a)))
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
