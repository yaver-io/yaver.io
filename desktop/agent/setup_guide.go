package main

// setup_guide.go — the store-onboarding CONCIERGE for normie (third-party)
// developers. A data-driven catalogue of every step to get an app from
// "no developer account" to "shipping on TestFlight / Play with IAP +
// Sign-in", each step tagged with how much Yaver can do for them:
//
//   auto     — Yaver does it (a yaver command / API call). No store UI.
//   assisted — Yaver does part; the rest is one click on an official page
//              (we open the EXACT Apple/Google URL — never make them hunt).
//   manual   — only the human can (legal identity, payment, tax/banking).
//              We can't and shouldn't automate it; we route + track it.
//
// This is the single source of truth the CLI (`yaver setup`) and the
// web/mobile checklist render. Status is best-effort from the local vault
// (a task whose required secrets are all present reads as done) — never
// from Convex (secrets are vault-only per the privacy contract).
//
// HARD TRUTH encoded here: neither Apple nor Google expose an enrollment
// API, and creating accounts on a user's behalf / sharing one account
// violates both ToS. So account creation is `manual` + routed, by design.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// handleStores serves the store-onboarding catalogue + best-effort status
// for the web/mobile checklist (agent GET /stores). The catalogue is the
// single source of truth (setup_guide.go); the UIs only render + route to
// the official Apple/Google URLs each task carries.
func (s *HTTPServer) handleStores(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": storesPayload()})
}

type setupAutomation string

const (
	setupAuto     setupAutomation = "auto"
	setupAssisted setupAutomation = "assisted"
	setupManual   setupAutomation = "manual"
)

type setupTask struct {
	ID          string          `json:"id"`
	Platform    string          `json:"platform"` // "apple" | "google" | "both"
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Automation  setupAutomation `json:"automation"`
	RouteURL    string          `json:"routeUrl,omitempty"`    // EXACT official page to open
	Steps       []string        `json:"steps,omitempty"`       // human-readable, ordered
	NeedsSecret []string        `json:"needsSecret,omitempty"` // vault keys ⇒ done when all present
	DependsOn   []string        `json:"dependsOn,omitempty"`   // task IDs satisfied first
	YaverCmd    string          `json:"yaverCmd,omitempty"`    // the yaver command (if any)
}

// setupTasks — ordered as the normie's journey. Keep auto/assisted/manual
// honest: don't claim auto for anything gated by legal identity, payment,
// tax/banking, or store review.
var setupTasks = []setupTask{
	// ── Apple ────────────────────────────────────────────────────────
	{
		ID: "apple-account", Platform: "apple",
		Title:      "Enroll in the Apple Developer Program ($99/yr)",
		Summary:    "Only you can do this — Apple verifies your legal identity (gov ID + sometimes a selfie). No API exists; sharing one account across people breaks Apple's ToS.",
		Automation: setupManual,
		RouteURL:   "https://developer.apple.com/programs/enroll/",
		Steps: []string{
			"Apple Account with two-factor auth ON, using your LEGAL name.",
			"Individual: government ID ready. Organization: a D-U-N-S number + authority to bind the org.",
			"Pay the $99/yr fee. Enrollment can take minutes to a few days to verify.",
			"In select regions you can enroll faster in the Apple Developer iPhone app.",
		},
	},
	{
		ID: "apple-asc-key", Platform: "apple",
		Title:      "App Store Connect API key (.p8 + Key ID + Issuer ID)",
		Summary:    "Create the API key once; Yaver stores it in your vault and uses it for every signing + upload. Download the .p8 — Apple only shows it once.",
		Automation: setupAssisted,
		RouteURL:   "https://appstoreconnect.apple.com/access/integrations/api",
		DependsOn:  []string{"apple-account"},
		Steps: []string{
			"Open the Integrations → App Store Connect API page.",
			"Generate an Admin (or App Manager) key; download the .p8 NOW (one-time).",
			"Copy the Key ID + Issuer ID; find your Team ID under Membership.",
			"Save them: yaver vault add APP_STORE_KEY_PATH/_ID/_ISSUER + APPLE_TEAM_ID --project <app>",
		},
		NeedsSecret: []string{"APP_STORE_KEY_PATH", "APP_STORE_KEY_ID", "APP_STORE_KEY_ISSUER", "APPLE_TEAM_ID"},
		YaverCmd:    "yaver vault add APP_STORE_KEY_PATH --project <app> --value <path-to-.p8>",
	},
	{
		ID: "apple-signing", Platform: "apple",
		Title:      "iOS distribution certificate + provisioning",
		Summary:    "Yaver creates the distribution certificate via the App Store Connect API (CSR generated locally) — no Keychain wrangling. Stays in your vault.",
		Automation: setupAuto,
		DependsOn:  []string{"apple-asc-key"},
		YaverCmd:   "yaver keys init --project <app> --platform ios",
	},
	{
		ID: "apple-testflight", Platform: "apple",
		Title:      "Ship to TestFlight",
		Summary:    "Yaver archives + uploads with your key. Internal testing is instant; EXTERNAL testing needs a one-time Beta App Review (Apple-side), plus export-compliance answers.",
		Automation: setupAssisted,
		RouteURL:   "https://appstoreconnect.apple.com/apps",
		DependsOn:  []string{"apple-signing"},
		YaverCmd:   "yaver deploy ship --app <app> --target testflight",
		Steps: []string{
			"Run the deploy — Yaver builds + uploads the build.",
			"Internal testers (up to 100, your team) get it immediately.",
			"For external testers: submit the build for Beta App Review in App Store Connect (one-time per major change).",
		},
	},
	{
		ID: "apple-iap", Platform: "apple",
		Title:      "In-app purchases / subscriptions (Apple)",
		Summary:    "Yaver can create products via the App Store Connect API. But you must first sign the Paid Apps agreement and complete tax + banking — only you can (legal/financial).",
		Automation: setupAssisted,
		RouteURL:   "https://appstoreconnect.apple.com/business",
		DependsOn:  []string{"apple-account"},
		Steps: []string{
			"Agreements, Tax, and Banking → accept the Paid Apps agreement; fill tax + bank details (manual, legal).",
			"Yaver creates your products/subscriptions via the ASC API and wires StoreKit 2 into the app.",
			"Server-side status via the App Store Server API + Server Notifications V2 (or use RevenueCat — see iap-cross-platform).",
		},
	},
	{
		ID: "apple-signin", Platform: "apple",
		Title:      "Sign in with Apple",
		Summary:    "Yaver creates the Services ID + key and wires the client. Note: Apple Guideline 4.8 REQUIRES Sign in with Apple if your app offers any other social login (Google, Facebook…).",
		Automation: setupAssisted,
		RouteURL:   "https://developer.apple.com/account/resources/identifiers/list/serviceId",
		DependsOn:  []string{"apple-account"},
		YaverCmd:   "yaver keys init --project <app> --platform ios --signin-apple",
		Steps: []string{
			"Yaver enables the Sign in with Apple capability + creates a Services ID and a key (.p8) via the API.",
			"Confirm the return/redirect URLs for web/Android flows on the linked page.",
			"Yaver injects the client config into your app's auth setup.",
		},
	},

	// ── Google ───────────────────────────────────────────────────────
	{
		ID: "google-account", Platform: "google",
		Title:      "Register a Google Play Console account ($25 once)",
		Summary:    "Only you can do this — Google verifies your identity (gov ID for personal accounts) + a Payments profile. No enrollment API.",
		Automation: setupManual,
		RouteURL:   "https://play.google.com/console/signup",
		Steps: []string{
			"Google account with 2-Step Verification ON; pay the one-time $25.",
			"Provide legal name, address, phone; personal accounts upload a government ID.",
			"Organization accounts need a D-U-N-S number.",
			"Heads-up: a NEW personal account must run a closed test with 12 testers for 14 days before it can publish to production.",
		},
	},
	{
		ID: "google-keystore", Platform: "google",
		Title:       "Upload keystore + Play App Signing",
		Summary:     "Yaver generates your upload keystore with keytool and stores it in the vault. Opt INTO Play App Signing so Google holds the real signing key — a lost upload key is then recoverable.",
		Automation:  setupAuto,
		YaverCmd:    "yaver keys init --project <app> --platform android",
		NeedsSecret: []string{"ANDROID_KEYSTORE_PASSWORD", "ANDROID_KEY_ALIAS", "ANDROID_KEY_PASSWORD"},
		Steps: []string{
			"Yaver runs keytool, creates the keystore + passwords, saves them to your vault.",
			"On first upload, opt into Play App Signing (strongly recommended — your safety net).",
		},
	},
	{
		ID: "google-service-account", Platform: "google",
		Title:       "Play Developer API service account (JSON)",
		Summary:     "Create a service account in Google Cloud and grant it in Play Console so Yaver can upload builds + manage tracks. Yaver guides each click and stores the JSON in your vault.",
		Automation:  setupAssisted,
		RouteURL:    "https://console.cloud.google.com/iam-admin/serviceaccounts",
		DependsOn:   []string{"google-account"},
		NeedsSecret: []string{"PLAY_STORE_KEY_FILE"},
		Steps: []string{
			"Create a service account + JSON key in Google Cloud.",
			"In Play Console → Users & permissions, invite the service-account email with release permissions.",
			"yaver vault add PLAY_STORE_KEY_FILE --project <app> --value <path-to-json>",
		},
	},
	{
		ID: "google-internal", Platform: "google",
		Title:      "Ship to Play internal testing",
		Summary:    "Yaver builds the release AAB and uploads to the internal track of YOUR app (reads your package id from gradle). Promote to production with the production target.",
		Automation: setupAuto,
		DependsOn:  []string{"google-keystore", "google-service-account"},
		YaverCmd:   "yaver deploy ship --app <app> --target playstore   # or --target playstore-production",
	},
	{
		ID: "google-iap", Platform: "google",
		Title:      "In-app products / subscriptions (Google)",
		Summary:    "Yaver creates products via the Play Developer API. You must first set up a Payments (merchant) profile — only you can (financial).",
		Automation: setupAssisted,
		RouteURL:   "https://play.google.com/console",
		DependsOn:  []string{"google-account"},
		Steps: []string{
			"Set up your Payments/merchant profile in Play Console (manual, financial).",
			"Yaver creates in-app products + subscriptions via the Play Developer API and wires Play Billing into the app.",
			"Server-side events via Real-time Developer Notifications (Pub/Sub) — or use RevenueCat (see iap-cross-platform).",
		},
	},
	{
		ID: "google-signin", Platform: "google",
		Title:      "Google Sign-In (OAuth client IDs)",
		Summary:    "Needs an OAuth client per platform; Android needs your keystore's SHA-1 — Yaver computes it for you. Create the clients on the linked Google Cloud page.",
		Automation: setupAssisted,
		RouteURL:   "https://console.cloud.google.com/apis/credentials",
		DependsOn:  []string{"google-keystore"},
		YaverCmd:   "yaver keys signin-google --project <app>   # prints package + SHA-1 to paste",
		Steps: []string{
			"Yaver prints your package name + SHA-1 (from the keystore) to paste into the client config.",
			"Create OAuth client IDs: Android (package + SHA-1), iOS (bundle id), and Web if you have a backend.",
			"Yaver injects the client IDs into your app's auth setup.",
		},
	},

	// ── Cross-platform ───────────────────────────────────────────────
	{
		ID: "iap-cross-platform", Platform: "both",
		Title:      "Optional: RevenueCat for cross-platform IAP",
		Summary:    "Apple and Google billing are entirely separate systems. RevenueCat unifies receipts, entitlements, and webhooks across both — free under $2.5k/mo tracked revenue, then 1%. The pragmatic path for a solo dev.",
		Automation: setupAssisted,
		RouteURL:   "https://app.revenuecat.com",
		Steps: []string{
			"Create a RevenueCat project; connect the App Store + Play apps.",
			"Yaver scaffolds the RevenueCat SDK + entitlement checks into your app.",
			"Skip this if you prefer to own native StoreKit 2 + Play Billing directly (more control, more work).",
		},
	},
}

func setupTaskByID(id string) (*setupTask, bool) {
	for i := range setupTasks {
		if setupTasks[i].ID == id {
			return &setupTasks[i], true
		}
	}
	return nil, false
}

// vaultSecretNames returns the set of secret names present across all
// vault projects, best-effort. Empty (not an error) when the vault is
// locked / not authenticated — status simply reads "unknown" then.
func vaultSecretNames() map[string]bool {
	out := map[string]bool{}
	vs, err := openVaultOptional()
	if err != nil || vs == nil {
		return out
	}
	for _, p := range vs.ListProjects() {
		for _, e := range vs.List(p) {
			out[e.Name] = true
		}
	}
	return out
}

type setupStatus string

const (
	statusDone    setupStatus = "done"
	statusTodo    setupStatus = "todo"
	statusManual  setupStatus = "action" // human action we can't detect
	statusBlocked setupStatus = "blocked"
	statusUnknown setupStatus = "unknown"
)

// evalSetupStatus computes a best-effort status for a task. secretsKnown
// is false when the vault couldn't be opened (so "todo"/"done" would be
// misleading → unknown).
func evalSetupStatus(t *setupTask, have map[string]bool, secretsKnown bool, doneByID map[string]setupStatus) setupStatus {
	// Blocked only if a dependency is explicitly incomplete (todo/blocked).
	// A manual/unknown dep is "can't detect" — don't let it permanently
	// block the subtree (e.g. account enrollment can't be detected, yet the
	// child's own secrets are the real signal it got done).
	for _, dep := range t.DependsOn {
		if s, ok := doneByID[dep]; ok && (s == statusTodo || s == statusBlocked) {
			return statusBlocked
		}
	}
	if len(t.NeedsSecret) > 0 {
		if !secretsKnown {
			return statusUnknown
		}
		for _, s := range t.NeedsSecret {
			if !have[s] {
				return statusTodo
			}
		}
		return statusDone
	}
	// No secret signal to detect completion.
	if t.Automation == setupManual {
		return statusManual
	}
	return statusUnknown
}

var setupGlyph = map[setupStatus]string{
	statusDone: "✓", statusTodo: "○", statusManual: "◆",
	statusBlocked: "⋯", statusUnknown: "·",
}

func runStores(args []string) {
	jsonOut := false
	var positional []string
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println("Usage: yaver stores [task-id] [--json]")
			fmt.Println("  no args   list every store-onboarding step + status")
			fmt.Println("  task-id   show one step in detail (steps + the exact Apple/Google URL)")
			fmt.Println("  --json    machine-readable catalogue + status (for web/mobile)")
			return
		default:
			positional = append(positional, a)
		}
	}

	have := vaultSecretNames()
	secretsKnown := len(have) > 0 // empty also means "couldn't open" — conservative

	// First pass: status by id (deps resolve against this).
	doneByID := map[string]setupStatus{}
	for i := range setupTasks {
		doneByID[setupTasks[i].ID] = evalSetupStatus(&setupTasks[i], have, secretsKnown, doneByID)
	}

	if len(positional) == 1 {
		t, ok := setupTaskByID(positional[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown setup task %q. Run `yaver stores` to list them.\n", positional[0])
			os.Exit(1)
		}
		if jsonOut {
			emitSetupJSON([]setupTask{*t}, doneByID)
			return
		}
		printSetupDetail(t, doneByID[t.ID])
		return
	}

	if jsonOut {
		emitSetupJSON(setupTasks, doneByID)
		return
	}
	printSetupList(doneByID)
}

func printSetupList(doneByID map[string]setupStatus) {
	fmt.Println("Yaver store setup — get your app onto the App Store & Google Play")
	fmt.Println("  ✓ done   ○ to do   ◆ your action (legal/payment)   ⋯ blocked by a prior step   · unknown")
	fmt.Println()
	groups := []struct{ key, label string }{
		{"apple", "Apple"}, {"google", "Google"}, {"both", "Cross-platform"},
	}
	for _, g := range groups {
		printed := false
		for i := range setupTasks {
			t := &setupTasks[i]
			if t.Platform != g.key {
				continue
			}
			if !printed {
				fmt.Printf("%s\n", g.label)
				printed = true
			}
			st := doneByID[t.ID]
			auto := strings.ToUpper(string(t.Automation))
			fmt.Printf("  %s [%-8s] %-22s %s\n", setupGlyph[st], auto, t.ID, t.Title)
		}
		if printed {
			fmt.Println()
		}
	}
	fmt.Println("Details:  yaver stores <task-id>       (steps + the exact page to open)")
}

func printSetupDetail(t *setupTask, st setupStatus) {
	fmt.Printf("%s  %s\n", setupGlyph[st], t.Title)
	fmt.Printf("  id:        %s\n", t.ID)
	fmt.Printf("  platform:  %s\n", t.Platform)
	fmt.Printf("  Yaver does: %s\n", map[setupAutomation]string{
		setupAuto:     "all of it (no store UI)",
		setupAssisted: "part — the rest is one click on the page below",
		setupManual:   "nothing automatable — only you can (we route + track it)",
	}[t.Automation])
	fmt.Printf("\n  %s\n", t.Summary)
	if len(t.DependsOn) > 0 {
		fmt.Printf("\n  Needs first: %s\n", strings.Join(t.DependsOn, ", "))
	}
	if len(t.Steps) > 0 {
		fmt.Println("\n  Steps:")
		for i, s := range t.Steps {
			fmt.Printf("    %d. %s\n", i+1, s)
		}
	}
	if t.YaverCmd != "" {
		fmt.Printf("\n  Run:  %s\n", t.YaverCmd)
	}
	if t.RouteURL != "" {
		fmt.Printf("\n  Open: %s\n", t.RouteURL)
	}
	if len(t.NeedsSecret) > 0 {
		fmt.Printf("\n  Done when these are in your vault: %s\n", strings.Join(t.NeedsSecret, ", "))
	}
}

// StoreTaskStatus pairs a catalogue task with its computed status — the
// shape served to web/mobile (agent GET /stores) and by `yaver stores --json`.
type StoreTaskStatus struct {
	setupTask
	Status setupStatus `json:"status"`
}

// storesPayload builds the full catalogue + best-effort status. Single
// source of truth for every surface (CLI, agent HTTP, web, mobile).
func storesPayload() []StoreTaskStatus {
	have := vaultSecretNames()
	secretsKnown := len(have) > 0
	doneByID := map[string]setupStatus{}
	for i := range setupTasks {
		doneByID[setupTasks[i].ID] = evalSetupStatus(&setupTasks[i], have, secretsKnown, doneByID)
	}
	out := make([]StoreTaskStatus, 0, len(setupTasks))
	for i := range setupTasks {
		out = append(out, StoreTaskStatus{setupTask: setupTasks[i], Status: doneByID[setupTasks[i].ID]})
	}
	return out
}

func emitSetupJSON(tasks []setupTask, doneByID map[string]setupStatus) {
	out := make([]StoreTaskStatus, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, StoreTaskStatus{setupTask: t, Status: doneByID[t.ID]})
	}
	// Stable order for deterministic UIs.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
