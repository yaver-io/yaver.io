package studio

import "context"

// CaptureSurface is the device-like target Studio drives to produce store
// assets: a redroid Android container (redroid.go), an iOS Simulator on a Mac
// runner (ios.go seam), or a real device. The agent orchestrates a surface the
// same way regardless of where it physically lives — on a Yaver-managed-cloud
// farm box (the third-party flow: the user picks "Yaver cloud", we provision a
// box, run the surface, bill the run) or on the owner's own on-prem box (free).
//
// All methods take a context so a hung job is bounded; Teardown MUST be safe to
// call (defer) and is where billing stops.
type CaptureSurface interface {
	// Provision brings the surface up (load kernel deps, boot the container/sim)
	// and returns when it is ready to install + drive.
	Provision(ctx context.Context) error

	// Install installs an app artifact (APK/IPA path on the local machine that
	// built it; the surface handles transferring it to its host).
	Install(ctx context.Context, artifactPath string) error

	// Driver exposes the high-level automation verbs (the "Yaver layer").
	Driver() Driver

	// Teardown stops + removes the surface. Idempotent. Always defer this.
	Teardown(ctx context.Context) error

	// Platform is "android" or "ios".
	Platform() string
}

// Driver is the high-level automation vocabulary Studio flows are written in —
// the verbs that guide a simulator/emulator for screenshots, videos, and
// account lifecycle. Implementations map each to the surface's native tooling
// (redroid → docker exec + am/input/uiautomator/screenrecord; iOS → simctl +
// XCUITest/Maestro).
type Driver interface {
	// --- app lifecycle ---
	Launch(ctx context.Context, app App) error
	ForceStop(ctx context.Context, app App) error

	// --- foreground service (permission demos) ---
	StartForegroundService(ctx context.Context, component, action string) error
	StopService(ctx context.Context, component string) error

	// --- UI interaction ---
	Tap(ctx context.Context, x, y int) error
	TapText(ctx context.Context, text string) error // find by visible text, tap center
	Type(ctx context.Context, text string) error
	Key(ctx context.Context, key string) error // e.g. "HOME", "BACK", "ENTER"
	WaitText(ctx context.Context, text string, timeoutSec int) error
	Back(ctx context.Context) error
	Home(ctx context.Context) error

	// --- notifications (FGS proof) ---
	ExpandNotifications(ctx context.Context) error
	CollapseNotifications(ctx context.Context) error
	NotificationText(ctx context.Context) (string, error) // current posted notifications

	// --- capture ---
	Screenshot(ctx context.Context) ([]byte, error) // PNG bytes
	RecordStart(ctx context.Context, maxSec int) error
	RecordStop(ctx context.Context) ([]byte, error) // MP4 bytes
}

// Dumper is an optional capability: a surface whose Driver can return a
// UIAutomator-style view hierarchy implements it (redroid does; the iOS sim does
// not without WDA). Consumers type-assert Driver() to Dumper — keeping the core
// Driver interface unchanged for surfaces that can't dump.
type Dumper interface {
	ViewTree(ctx context.Context) (string, error)
}

// LogReader is an optional capability: a surface whose Driver can return the
// device log tail (redroid via logcat). The crash/red-box oracles read it.
type LogReader interface {
	Logcat(ctx context.Context, lines int) (string, error)
}

// CrashReader is an optional capability: a surface whose Driver can return the
// dedicated crash buffer (the durable record of app FATALs), used by DiagDir to
// pinpoint launch crashes that the main buffer rotates out.
type CrashReader interface {
	CrashLog(ctx context.Context) (string, error)
}

// App identifies the app under capture.
type App struct {
	Package  string // android package / iOS bundle id
	Activity string // android launch activity (e.g. ".MainActivity"); optional
}

// AccountSpec describes how to put the app into a signed-in state so a feature
// behind auth (Yaver's sandbox, most apps' main UI) is reachable for capture.
// The actual provider flow is app-specific; AccountFlow (flow.go) turns this
// into Driver verbs. For shared/managed runners NEVER use the owner's real
// account — provision a throwaway, capture, then RemoveAccount on teardown.
type AccountSpec struct {
	Provider string // "email" | "apple" | "google" | "github" | "gitlab" | "microsoft" | "passkey"
	Email    string // for email provider
	// CodeSource yields the email/SMS verification code when the provider needs
	// one (e.g. a mailbox poller). Nil → the flow expects no code (or fails
	// loudly, surfacing that the owner must supply one). This is the seam that
	// keeps account-open automatable without baking in a specific inbox.
	CodeSource func(ctx context.Context) (string, error)
	// Disposable marks a throwaway account that RemoveAccount tears down.
	Disposable bool
}

// Compile-time assertions that both surfaces satisfy the interfaces.
var (
	_ CaptureSurface = (*RedroidSurface)(nil)
	_ CaptureSurface = (*IOSSimSurface)(nil)
	_ Driver         = (*redroidDriver)(nil)
	_ Driver         = (*iosDriver)(nil)
	_ Dumper         = (*redroidDriver)(nil)
	_ LogReader      = (*redroidDriver)(nil)
)
