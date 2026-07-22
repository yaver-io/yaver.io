package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// wire_surfaces.go — what `yaver wire push` can ACTUALLY install, per surface.
//
// ─── The incident this file exists to prevent ───────────────────────────────
//
// 2026-07-21, reported by the user: "i have watch but i dont see yaver app,
// it's installed on my phone from TestFlight."
//
// Nothing was broken. The watch target is present and correct in the iOS
// project (50 pbxproj references, 4 `Embed Watch Content` phases,
// `scripts/deploy-testflight.sh` runs `add-watch-ios-target.js` on every
// build). The app shipped. It just does not INSTALL by itself: a watchOS
// companion rides inside the iPhone .app and the user must tap INSTALL in the
// Watch app on their phone.
//
// The product failure was that nothing anywhere said so. `yaver wire push`
// accepted only ios|android, so a user asking "push to my watch" got
// `--platform must be ios or android` — a syntax error in response to a
// reasonable question, which teaches nothing.
//
// ─── The rule this encodes ──────────────────────────────────────────────────
//
// Surfaces do not share an install model, and the differences are not
// cosmetic:
//
//	ios       direct     install onto the device
//	android   direct     install onto the device
//	wearos    direct     a Wear OS watch is a first-class adb target
//	tvos      direct     over the network, after pairing
//	visionos  direct     over the network, after pairing
//	watchOS   COMPANION  no install channel exists AT ALL
//
// watchOS is the odd one out and it is odd in a way no amount of retrying
// fixes. Apple ships no API — not devicectl, not ios-deploy, not Xcode
// itself — that installs a watch app onto a paired watch independently of its
// host iPhone app. So the honest answer to `yaver wire push --surface watchos`
// is not an error and not a spinner: it is "push the iOS host app, then finish
// on the phone", with the exact taps named.
//
// Per CLAUDE.md's cross-surface parity rule this registry is the single source
// every surface reads — CLI, MCP verb, web, mobile, tablet, car, glass. A
// diagnosis only the CLI can see does not exist for a user on their phone,
// which is precisely how the watch question went unanswered until it was asked
// out loud.

// WireSurface is a physical target family a build can be installed onto.
type WireSurface string

const (
	SurfaceIOS      WireSurface = "ios"      // iPhone, iPad
	SurfaceAndroid  WireSurface = "android"  // phone, tablet
	SurfaceWatchOS  WireSurface = "watchos"  // Apple Watch
	SurfaceWearOS   WireSurface = "wearos"   // Wear OS watch
	SurfaceTVOS     WireSurface = "tvos"     // Apple TV
	SurfaceVisionOS WireSurface = "visionos" // Vision Pro / AR-VR
)

// WireInstallChannel is HOW a build reaches the device — the distinction that
// makes watchOS behave unlike everything else.
type WireInstallChannel string

const (
	// ChannelDirect: a tool can install onto the device. Success is a push.
	ChannelDirect WireInstallChannel = "direct"
	// ChannelCompanion: the artifact is embedded in a HOST app's bundle and
	// arrives only when the host does. There is no push to perform, so
	// reporting failure would be as wrong as reporting success.
	ChannelCompanion WireInstallChannel = "companion"
)

// WireSurfaceSpec is the full truth about one surface.
type WireSurfaceSpec struct {
	Surface WireSurface        `json:"surface"`
	Label   string             `json:"label"`
	Channel WireInstallChannel `json:"channel"`

	// HostSurface is set only for ChannelCompanion — the surface whose install
	// actually carries this one.
	HostSurface WireSurface `json:"hostSurface,omitempty"`

	// HostOS is the developer-machine OS required to build/install. Apple
	// surfaces are darwin-only; this is a toolchain fact, not a policy.
	HostOS string `json:"hostOS,omitempty"`

	// Tool is the binary that performs a direct install.
	Tool string `json:"tool,omitempty"`

	// Transport describes how the device attaches, which decides whether
	// "plug it in" is even applicable advice.
	Transport string `json:"transport"`

	// ManualStep is the human action that completes a companion install. It
	// names the literal taps — "check your configuration" costs whole
	// sessions, per CLAUDE.md.
	ManualStep string `json:"manualStep,omitempty"`

	// Note carries the surface-specific gotcha worth knowing before starting.
	Note string `json:"note,omitempty"`
}

// wireSurfaceRegistry is the single source of truth.
//
// Ordered map access goes through the helpers below so every surface renders
// surfaces in the same order.
var wireSurfaceRegistry = map[WireSurface]WireSurfaceSpec{
	SurfaceIOS: {
		Surface:   SurfaceIOS,
		Label:     "iPhone / iPad",
		Channel:   ChannelDirect,
		HostOS:    "darwin",
		Tool:      "devicectl",
		Transport: "usb-or-network",
	},
	SurfaceAndroid: {
		Surface:   SurfaceAndroid,
		Label:     "Android phone / tablet",
		Channel:   ChannelDirect,
		Tool:      "adb",
		Transport: "usb-or-network",
	},
	SurfaceWearOS: {
		Surface:   SurfaceWearOS,
		Label:     "Wear OS watch",
		Channel:   ChannelDirect,
		Tool:      "adb",
		Transport: "usb-or-network",
		Note: "a Wear OS watch is an ordinary adb target — enable ADB debugging in " +
			"Developer options, then pair over Wi-Fi with `adb connect <watch-ip>:5555`",
	},
	SurfaceTVOS: {
		Surface:   SurfaceTVOS,
		Label:     "Apple TV",
		Channel:   ChannelDirect,
		HostOS:    "darwin",
		Tool:      "devicectl",
		Transport: "network",
		Note: "Apple TV pairs over the network only — there is no cable path. Pair once in " +
			"Xcode (Window ▸ Devices and Simulators) before the first push",
	},
	SurfaceVisionOS: {
		Surface:   SurfaceVisionOS,
		Label:     "Apple Vision Pro",
		Channel:   ChannelDirect,
		HostOS:    "darwin",
		Tool:      "devicectl",
		Transport: "network",
		Note: "Vision Pro pairs over the network, same flow as Apple TV. Developer Mode must " +
			"be enabled on the headset first",
	},

	// The reason this file exists.
	SurfaceWatchOS: {
		Surface:     SurfaceWatchOS,
		Label:       "Apple Watch",
		Channel:     ChannelCompanion,
		HostSurface: SurfaceIOS,
		HostOS:      "darwin",
		Transport:   "via-paired-iphone",
		ManualStep: "On the iPhone: open the Watch app ▸ scroll to AVAILABLE APPS ▸ tap INSTALL " +
			"next to the app",
		Note: "Apple ships NO tool that installs a watchOS app onto a paired watch on its own — " +
			"not devicectl, not ios-deploy, not Xcode. The watch app is embedded inside the " +
			"iPhone .app and arrives with it; the final install is always a manual tap on the " +
			"phone. A watch app missing after a TestFlight install is almost always this, not a " +
			"broken build",
	},
}

// wireSurfaceOrder fixes presentation order: the two everyday surfaces first,
// then wearables, then the big-screen/spatial pair.
var wireSurfaceOrder = []WireSurface{
	SurfaceIOS, SurfaceAndroid, SurfaceWatchOS, SurfaceWearOS, SurfaceTVOS, SurfaceVisionOS,
}

// AllWireSurfaces returns every known surface in stable display order.
func AllWireSurfaces() []WireSurfaceSpec {
	out := make([]WireSurfaceSpec, 0, len(wireSurfaceOrder))
	for _, s := range wireSurfaceOrder {
		out = append(out, wireSurfaceRegistry[s])
	}
	return out
}

// LookupWireSurface resolves a user-typed surface name.
//
// Accepts the aliases people actually type. "watch" is deliberately NOT one of
// them: it is ambiguous across two ecosystems whose install models differ in
// the single way that matters here, so guessing would hand the user the wrong
// instructions with full confidence.
func LookupWireSurface(name string) (WireSurfaceSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.TrimPrefix(key, "--")
	switch key {
	case "ios", "iphone", "ipad":
		key = "ios"
	case "android", "droid":
		key = "android"
	case "watchos", "apple-watch", "applewatch":
		key = "watchos"
	case "wearos", "wear-os", "wear":
		key = "wearos"
	case "tvos", "appletv", "apple-tv", "tv":
		key = "tvos"
	case "visionos", "visionpro", "vision-pro", "vision", "arvr", "ar-vr", "xr":
		key = "visionos"
	}
	spec, ok := wireSurfaceRegistry[WireSurface(key)]
	return spec, ok
}

// AmbiguousWatchSurface reports whether a name means "a watch" without saying
// which ecosystem. Callers should ask rather than assume.
func AmbiguousWatchSurface(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "watch", "smartwatch", "watches":
		return true
	}
	return false
}

// WirePushPlan is what will happen if the user runs the push, decided BEFORE
// anything is built.
//
// Deciding up front matters: a 20-minute watchOS archive that ends in "now go
// tap something on your phone" should have said so at second zero.
type WirePushPlan struct {
	Surface WireSurface        `json:"surface"`
	Channel WireInstallChannel `json:"channel"`

	// CanInstallDirectly is true only when this run will put bits on the
	// device by itself.
	CanInstallDirectly bool `json:"canInstallDirectly"`

	// BuildSurface is what actually gets built — for watchOS this is the iOS
	// host app, which is the whole point.
	BuildSurface WireSurface `json:"buildSurface"`

	// Blocked marks "cannot proceed at all", distinct from "proceeds but ends
	// with a manual step".
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`

	// Summary is one watch-sized sentence. If it does not fit on a watch it is
	// too long for anyone.
	Summary string `json:"summary"`

	// NextStep is the human action needed after the tool finishes, or "".
	NextStep string `json:"nextStep,omitempty"`
}

// PlanWirePush resolves what a push to this surface will really do on this host.
func PlanWirePush(surface WireSurface, hostGOOS string) WirePushPlan {
	spec, ok := wireSurfaceRegistry[surface]
	if !ok {
		return WirePushPlan{
			Surface: surface,
			Blocked: true,
			Reason:  fmt.Sprintf("unknown surface %q", surface),
			Summary: "Unknown surface",
		}
	}

	plan := WirePushPlan{
		Surface:      surface,
		Channel:      spec.Channel,
		BuildSurface: surface,
	}

	// Apple toolchains are darwin-only. Say which surface and which host, so
	// the message survives being read out of context.
	if spec.HostOS != "" && spec.HostOS != hostGOOS {
		plan.Blocked = true
		plan.Reason = fmt.Sprintf("%s builds require a %s host (this machine is %s)",
			spec.Label, spec.HostOS, hostGOOS)
		plan.Summary = spec.Label + " needs a Mac"
		return plan
	}

	if spec.Channel == ChannelCompanion {
		// Not blocked, and not a direct install either. Build the host app,
		// then hand off honestly.
		plan.BuildSurface = spec.HostSurface
		plan.CanInstallDirectly = false
		plan.Summary = fmt.Sprintf("Installs with the %s app, then one tap on the phone",
			wireSurfaceRegistry[spec.HostSurface].Label)
		plan.NextStep = spec.ManualStep
		return plan
	}

	plan.CanInstallDirectly = true
	plan.Summary = "Installs directly onto " + spec.Label
	return plan
}

// ─── device discovery ───────────────────────────────────────────────────────

// listAppleWireDevicesBySurface asks devicectl for every paired Apple device
// and buckets them by surface.
//
// One call, not four: devicectl already returns watch/tv/vision alongside
// iPhone, and the existing listIOSWireDevices() throws all of that away by
// filtering to iphone|ipad (wire_cmd.go:246). That filter is why Yaver could
// not see an Apple TV or a Vision Pro it was already paired with.
func listAppleWireDevicesBySurface(ctx context.Context) map[WireSurface][]wireDevice {
	out := map[WireSurface][]wireDevice{}
	if runtime.GOOS != "darwin" {
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	tmp := "/tmp/yaver-devicectl-surfaces.json"
	cmd := exec.CommandContext(cctx, "xcrun", "devicectl", "list", "devices",
		"--quiet", "--json-output", tmp)
	if err := cmd.Run(); err != nil {
		return out
	}
	raw, err := readWireJSONBounded(tmp, 8<<20)
	if err != nil {
		return out
	}
	var parsed struct {
		Result struct {
			Devices []struct {
				Identifier       string `json:"identifier"`
				DeviceProperties struct {
					Name      string `json:"name"`
					OSVersion string `json:"osVersionNumber"`
				} `json:"deviceProperties"`
				HardwareProperties struct {
					Platform   string `json:"platform"`
					DeviceType string `json:"deviceType"`
					UDID       string `json:"udid"`
				} `json:"hardwareProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out
	}

	for _, d := range parsed.Result.Devices {
		surface := appleSurfaceFor(d.HardwareProperties.Platform, d.HardwareProperties.DeviceType)
		if surface == "" {
			continue
		}
		udid := d.HardwareProperties.UDID
		if udid == "" {
			udid = d.Identifier
		}
		out[surface] = append(out[surface], wireDevice{
			UDID:     udid,
			Name:     d.DeviceProperties.Name,
			Platform: string(surface),
			OS:       d.DeviceProperties.OSVersion,
		})
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].Name < out[k][j].Name })
	}
	return out
}

// readWireJSONBounded reads a devicectl dump with a hard ceiling, so a
// pathological file cannot balloon agent memory during what is only a device
// listing.
func readWireJSONBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

// appleSurfaceFor maps devicectl's platform/deviceType onto a Yaver surface.
//
// Both fields are consulted because Apple has changed which one carries the
// family across Xcode versions; matching either is the resilient read.
func appleSurfaceFor(platform, deviceType string) WireSurface {
	h := strings.ToLower(platform + " " + deviceType)
	switch {
	case strings.Contains(h, "watch"):
		return SurfaceWatchOS
	case strings.Contains(h, "tv"):
		return SurfaceTVOS
	case strings.Contains(h, "vision"), strings.Contains(h, "reality"), strings.Contains(h, "xr"):
		return SurfaceVisionOS
	case strings.Contains(h, "iphone"), strings.Contains(h, "ipad"), strings.Contains(h, "ios"):
		return SurfaceIOS
	}
	return ""
}

// WearOSDeviceHeuristic reports whether an adb device looks like a Wear OS
// watch rather than a phone or tablet.
//
// adb does not expose a device family, so this reads the model string. It is a
// heuristic and is named one: a false negative costs a wrong label, never a
// wrong install, because the install path is identical either way.
func WearOSDeviceHeuristic(model, characteristics string) bool {
	h := strings.ToLower(model + " " + characteristics)
	for _, marker := range []string{"watch", "wear", "galaxy watch", "pixel watch", "ticwatch"} {
		if strings.Contains(h, marker) {
			return true
		}
	}
	return false
}

// ─── reporting ──────────────────────────────────────────────────────────────

// WireSurfaceReport is the cross-surface payload: one struct rendered by CLI,
// web, mobile, tablet, car, glass and watch alike.
type WireSurfaceReport struct {
	HostOS   string              `json:"hostOS"`
	Surfaces []WireSurfaceStatus `json:"surfaces"`
}

// WireSurfaceStatus is one surface's spec, plan and attached devices.
type WireSurfaceStatus struct {
	Spec    WireSurfaceSpec `json:"spec"`
	Plan    WirePushPlan    `json:"plan"`
	Devices []wireDevice    `json:"devices,omitempty"`
}

// BuildWireSurfaceReport probes every surface and returns the whole picture.
func BuildWireSurfaceReport(ctx context.Context) WireSurfaceReport {
	rep := WireSurfaceReport{HostOS: runtime.GOOS}
	apple := listAppleWireDevicesBySurface(ctx)

	for _, spec := range AllWireSurfaces() {
		st := WireSurfaceStatus{
			Spec: spec,
			Plan: PlanWirePush(spec.Surface, runtime.GOOS),
		}
		switch spec.Surface {
		case SurfaceIOS, SurfaceWatchOS, SurfaceTVOS, SurfaceVisionOS:
			st.Devices = apple[spec.Surface]
		case SurfaceAndroid, SurfaceWearOS:
			// Android and Wear OS share one adb namespace; splitting them is
			// the caller's job via WearOSDeviceHeuristic on enriched info.
			st.Devices = nil
		}
		rep.Surfaces = append(rep.Surfaces, st)
	}
	return rep
}

// JSON renders the report for every client surface.
func (r WireSurfaceReport) JSON() ([]byte, error) { return json.Marshal(r) }

// ─── project-side surface detection (third-party repos) ─────────────────────

// DetectProjectSurfaces reports which surfaces a project ACTUALLY builds.
//
// This is the third-party monorepo case: `yaver wire push` run from someone
// else's repo must discover what that repo produces rather than assume the
// Yaver layout. Detection reads the build files, because a repo containing a
// watch directory is not the same as a repo that BUILDS a watch target —
// inventory versus operation again.
//
// Signals, in the order they are cheap to check:
//
//	watchOS   pbxproj SDKROOT watchos / an Embed Watch Content phase
//	tvOS      pbxproj SDKROOT appletvos
//	visionOS  pbxproj SDKROOT xros
//	Wear OS   an Android manifest declaring android.hardware.type.watch
//
// A false negative costs a surface the user must name explicitly with
// --surface; a false positive would start a 20-minute build for a target that
// does not exist, so the markers are deliberately specific.
func DetectProjectSurfaces(root, stack string) []WireSurface {
	var found []WireSurface
	seen := map[WireSurface]bool{}
	add := func(s WireSurface) {
		if !seen[s] {
			seen[s] = true
			found = append(found, s)
		}
	}

	switch stack {
	case "native-ios":
		add(SurfaceIOS)
	case "native-android":
		add(SurfaceAndroid)
	default:
		// Cross-platform stacks (expo, react-native, flutter) build both once
		// their platform dirs exist.
		if wireExists(filepath.Join(root, "ios")) {
			add(SurfaceIOS)
		}
		if wireExists(filepath.Join(root, "android")) {
			add(SurfaceAndroid)
		}
	}

	for _, pb := range findPbxprojFiles(root) {
		data, err := readWireJSONBounded(pb, 12<<20)
		if err != nil {
			continue
		}
		s := strings.ToLower(string(data))
		if strings.Contains(s, "sdkroot = watchos") || strings.Contains(s, "embed watch content") {
			add(SurfaceWatchOS)
			add(SurfaceIOS) // a watch target implies its iOS host
		}
		if strings.Contains(s, "sdkroot = appletvos") {
			add(SurfaceTVOS)
		}
		if strings.Contains(s, "sdkroot = xros") || strings.Contains(s, "sdkroot = visionos") {
			add(SurfaceVisionOS)
		}
	}

	if androidDeclaresWatch(root) {
		add(SurfaceWearOS)
	}
	return found
}

// findPbxprojFiles locates Xcode project files without walking the whole tree.
//
// Bounded on purpose: a full walk of a monorepo with node_modules is the
// unbounded-breadth mistake CLAUDE.md names — advisory work must never sit in
// the critical path. Only the conventional locations are checked.
func findPbxprojFiles(root string) []string {
	var out []string
	candidates := []string{root, filepath.Join(root, "ios"), filepath.Join(root, "apple")}
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
				p := filepath.Join(dir, e.Name(), "project.pbxproj")
				if wireFileExists(p) {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// androidDeclaresWatch looks for the manifest feature that makes an Android
// module a Wear OS app.
//
// `android.hardware.type.watch` is the definitive marker — a module named
// "wear" proves nothing, and Wear modules are not always named that.
func androidDeclaresWatch(root string) bool {
	candidates := []string{
		filepath.Join(root, "wear", "src", "main", "AndroidManifest.xml"),
		filepath.Join(root, "android", "wear", "src", "main", "AndroidManifest.xml"),
		filepath.Join(root, "wearos", "src", "main", "AndroidManifest.xml"),
		filepath.Join(root, "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join(root, "android", "app", "src", "main", "AndroidManifest.xml"),
	}
	for _, p := range candidates {
		data, err := readWireJSONBounded(p, 1<<20)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "android.hardware.type.watch") {
			return true
		}
	}
	return false
}

// ─── CLI integration ────────────────────────────────────────────────────────

// applyWireSurface resolves --surface into a concrete push, or explains why
// there is nothing to push and stops.
//
// Returns true to continue with the normal ios|android push machinery, false
// when the surface has already been fully answered (a companion hand-off, a
// plan listing, or a blocked host).
//
// The honesty rule here: a surface whose BUILD path is not implemented must
// say so rather than silently building an iPhone app and reporting success.
// tvOS and visionOS need their own scheme/SDK selection, which native_build.go
// does not yet do — so they report exactly that, and the detection + device
// listing they DO have is still useful.
func applyWireSurface(root, stack, name string, opts *wirePushOpts) bool {
	if AmbiguousWatchSurface(name) {
		fmt.Fprintln(os.Stderr, "yaver wire push: --surface watch is ambiguous — Apple Watch and Wear OS")
		fmt.Fprintln(os.Stderr, "  install in completely different ways, so guessing would give you the")
		fmt.Fprintln(os.Stderr, "  wrong instructions confidently. Use --surface watchos or --surface wearos.")
		os.Exit(2)
	}

	if strings.EqualFold(strings.TrimSpace(name), "all") {
		printWireSurfaceMatrix(root, stack)
		return false
	}

	spec, ok := LookupWireSurface(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "yaver wire push: unknown surface %q\n", name)
		fmt.Fprint(os.Stderr, "  known: ")
		names := make([]string, 0, len(wireSurfaceOrder))
		for _, s := range wireSurfaceOrder {
			names = append(names, string(s))
		}
		fmt.Fprintln(os.Stderr, strings.Join(names, ", ")+", all")
		os.Exit(2)
	}

	plan := PlanWirePush(spec.Surface, runtime.GOOS)
	fmt.Printf("→ surface:  %s (%s)\n", spec.Surface, spec.Label)

	if plan.Blocked {
		fmt.Fprintf(os.Stderr, "\n✗ %s\n", plan.Reason)
		os.Exit(2)
	}

	// Warn when the repo shows no sign of building this surface. A warning, not
	// a refusal: detection reads conventional locations only, and being wrong
	// about someone else's monorepo layout must not block them.
	detected := DetectProjectSurfaces(root, stack)
	if len(detected) > 0 && !containsSurface(detected, spec.Surface) {
		fmt.Printf("  ⚠️  no %s target detected in this project (found: %s)\n",
			spec.Surface, joinSurfaces(detected))
		fmt.Println("     continuing anyway — detection only reads conventional locations")
	}

	if spec.Note != "" {
		fmt.Printf("  note: %s\n", spec.Note)
	}

	switch spec.Surface {
	case SurfaceIOS:
		opts.platform = "ios"
		return true

	case SurfaceAndroid, SurfaceWearOS:
		// A Wear OS watch is an ordinary adb target, so the Android install
		// path is already correct for it — no new machinery needed.
		opts.platform = "android"
		return true

	case SurfaceWatchOS:
		// The whole reason this file exists. Build and push the iOS HOST app,
		// then hand off with the literal taps.
		fmt.Println()
		fmt.Println("  Apple Watch apps have no direct install channel. Pushing the iOS host")
		fmt.Println("  app instead — the watch app is embedded in it and rides along.")
		fmt.Println()
		opts.platform = "ios"
		wirePendingManualStep = plan.NextStep
		return true

	case SurfaceTVOS, SurfaceVisionOS:
		fmt.Fprintf(os.Stderr, "\n✗ %s builds are not wired yet.\n", spec.Label)
		fmt.Fprintf(os.Stderr, "  Detection and device listing work (`yaver wire detect` will show a\n")
		fmt.Fprintf(os.Stderr, "  paired %s), but the build path needs its own scheme + SDK selection\n", spec.Label)
		fmt.Fprintf(os.Stderr, "  and native_build.go does not do that yet. Building the iOS target and\n")
		fmt.Fprintf(os.Stderr, "  calling it a %s push would install the wrong app and report success.\n", spec.Label)
		os.Exit(2)
	}
	return true
}

// wirePendingManualStep carries a companion-install instruction to be printed
// AFTER a successful push, when the user is ready to act on it.
var wirePendingManualStep string

// PrintWirePendingManualStep emits the deferred hand-off, if any.
func PrintWirePendingManualStep() {
	if wirePendingManualStep == "" {
		return
	}
	fmt.Println()
	fmt.Println("─── one more step, on the phone ───────────────────────────────")
	fmt.Println("  " + wirePendingManualStep)
	fmt.Println()
	fmt.Println("  This is not a bug and not a failed push: Apple ships no tool that")
	fmt.Println("  installs a watch app on its own. The build is on the phone already.")
	wirePendingManualStep = ""
}

// printWireSurfaceMatrix shows every surface, what this project builds, and
// what would happen for each.
func printWireSurfaceMatrix(root, stack string) {
	detected := DetectProjectSurfaces(root, stack)
	fmt.Printf("→ project surfaces detected: %s\n\n", joinSurfaces(detected))
	fmt.Printf("  %-10s %-24s %-12s %s\n", "SURFACE", "DEVICE", "IN PROJECT", "WHAT HAPPENS")
	for _, spec := range AllWireSurfaces() {
		plan := PlanWirePush(spec.Surface, runtime.GOOS)
		in := "—"
		if containsSurface(detected, spec.Surface) {
			in = "yes"
		}
		what := plan.Summary
		if plan.Blocked {
			what = "blocked: " + plan.Reason
		}
		fmt.Printf("  %-10s %-24s %-12s %s\n", spec.Surface, spec.Label, in, what)
	}
	fmt.Println()
	fmt.Println("  Push one with:  yaver wire push --surface <name>")
}

func containsSurface(list []WireSurface, want WireSurface) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func joinSurfaces(list []WireSurface) string {
	if len(list) == 0 {
		return "(none detected)"
	}
	parts := make([]string, 0, len(list))
	for _, s := range list {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ", ")
}
