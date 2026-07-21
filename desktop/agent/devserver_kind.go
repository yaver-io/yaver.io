package main

// devserver_kind.go — classifies each dev server as web / mobile / hybrid.
//
// The Web Reload dashboard tab (iframe-based browser preview) and the
// Hot Reload mobile tab (phone mockup + Hermes push) both share the
// /dev/* plumbing but serve different surfaces. Kind() lets either
// surface filter out dev servers that don't belong to it so the user
// never sees, e.g., Metro listed in Web Reload.
//
// Mapping rationale:
//   - vite / nextjs      — web-only dev servers. Kind = web.
//   - flutter            — agent uses `flutter run -d web-port`, so
//                          even though Flutter the framework is
//                          hybrid, *this dev server* is web. Kind = web.
//   - react-native       — Metro, mobile-only. Kind = mobile.
//   - expo               — can do mobile (dev-client) + web. Kind = hybrid.
//                          The Web Reload surface accepts hybrid when the
//                          current config has expo web enabled; otherwise
//                          the user gets a helpful error.

// DevServerKind classifies a dev server by the surface it targets.
type DevServerKind string

const (
	DevServerKindWeb    DevServerKind = "web"
	DevServerKindMobile DevServerKind = "mobile"
	DevServerKindHybrid DevServerKind = "hybrid"
)

// Kind methods for each registered dev server. Keep these trivial —
// more refined hybrid detection (e.g. reading app.json to check whether
// expo.web is configured) happens at Start time, not here.

func (*ExpoDevServer) Kind() DevServerKind        { return DevServerKindHybrid }
func (*ReactNativeDevServer) Kind() DevServerKind { return DevServerKindMobile }
func (*FlutterDevServer) Kind() DevServerKind     { return DevServerKindWeb }
func (*ViteDevServer) Kind() DevServerKind        { return DevServerKindWeb }
func (*NextDevServer) Kind() DevServerKind        { return DevServerKindWeb }

// ─── Default preview surface ────────────────────────────────────────────────
//
// PreviewMode says HOW a workspace should be previewed when the user has not
// chosen. The default is the browser (web dev server, streamed to the client),
// never an emulator.
//
// This is the decision that lets the default machine be 2c/4GB and the $29 tier
// hold ~71% margin. Redroid needs ~6.5 GB before the app under test loads, so
// defaulting to it would force 8-16 GB on EVERY workspace to serve a case most
// users never hit. Browser preview costs ~nothing, and for the flagship
// React Native path the real device target is the user's OWN phone via Hermes
// push — which is both free to us and a more honest test than an emulator.
//
// See docs/architecture/yaver-four-tier-deep-analysis.md §9.
type PreviewMode string

const (
	// PreviewBrowser — web dev server surfaced in a browser/WebView, streamed
	// over WebRTC. Passthrough, not encoding, so 2 cores is enough.
	PreviewBrowser PreviewMode = "browser"
	// PreviewDevicePush — Hermes bundle pushed to the user's own phone. The
	// flagship RN loop. Zero server cost.
	PreviewDevicePush PreviewMode = "device-push"
	// PreviewRedroid — Android in a container, ON THE SERVER. Opt-in only:
	// it is the single workload that forces a bigger machine class.
	PreviewRedroid PreviewMode = "redroid"
)

// DefaultPreviewModeForStack picks the preview surface for a stack when the
// user has expressed no preference.
//
// Never returns PreviewRedroid. Redroid is a deliberate opt-in for "I need an
// Android device and do not have one" — it must never be reached by default,
// because the machine class follows the preview mode and a silent upgrade to
// 8 GB is a silent halving of the margin.
func DefaultPreviewModeForStack(stack string) PreviewMode {
	switch StackToDevServerKind(stack) {
	case DevServerKindMobile:
		// Metro alone has no browser surface, so the real device is the target.
		return PreviewDevicePush
	case DevServerKindHybrid:
		// Expo can do both; the browser is the lightweight half and needs no
		// device paired, so it is the better default for a first run.
		return PreviewBrowser
	case DevServerKindWeb:
		return PreviewBrowser
	default:
		// Unknown or non-UI stacks (go, rust, python, …) still get a browser
		// surface if they serve anything; nothing here justifies an emulator.
		return PreviewBrowser
	}
}

// PreviewModeNeedsHeavyMachine reports whether a preview mode forces a machine
// class above the 2c/4GB default. Only server-side Android does.
func PreviewModeNeedsHeavyMachine(mode PreviewMode) bool {
	return mode == PreviewRedroid
}

// StackToDevServerKind maps a workspace manifest `stack` value to the
// kind of dev server it would produce. Returns empty string when the
// stack has no dev-server representation (go, rust, python, convex,
// gradle, …) — callers should filter empty results out of web/mobile
// surfaces.
func StackToDevServerKind(stack string) DevServerKind {
	switch stack {
	case "nextjs", "vite", "flutter", "astro", "remix":
		return DevServerKindWeb
	case "react-native-expo":
		return DevServerKindHybrid
	case "react-native":
		return DevServerKindMobile
	default:
		return ""
	}
}

// StackToFramework maps a workspace manifest `stack` to the framework
// identifier expected by DevServerManager.Start. Returns empty when
// the stack has no matching dev server.
func StackToFramework(stack string) string {
	switch stack {
	case "nextjs":
		return "nextjs"
	case "vite":
		return "vite"
	case "flutter":
		return "flutter"
	case "react-native-expo":
		return "expo"
	case "react-native":
		return "react-native"
	default:
		return ""
	}
}

// FrameworkToDevServerKind maps a framework identifier (the value
// passed to /dev/start without going through a workspace manifest) to
// the kind of dev server it produces. Mirrors the per-impl Kind()
// methods so the surface gate in handleDevServerStart can run even
// when no yaver.workspace.yaml exists. Returns empty string for unknown
// values; callers should treat that as "let the manager auto-detect
// then re-check via DevServer.Kind()".
//
// Expo is bucketed as Mobile here even though Expo can technically
// serve a web build — the Web Reload surface is reserved for projects
// whose primary target is the browser. Mobile-first projects like
// sfmg / talos that happen to have expo-web wired up should be driven
// from Hot Reload (real device) rather than the iframe surface that
// frequently breaks on HMR WebSocket reconnects through the proxy.
// If a future "expo-web" framework string lands explicitly, route it
// to Web here.
func FrameworkToDevServerKind(framework string) DevServerKind {
	switch framework {
	case "nextjs", "next", "vite", "flutter", "astro", "remix", "expo-web":
		return DevServerKindWeb
	case "expo", "react-native":
		return DevServerKindMobile
	default:
		return ""
	}
}
