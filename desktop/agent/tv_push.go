package main

// tv_push.go — `yaver tv detect` / `yaver tv push`: build a tvOS app and install
// it on a paired Apple TV over the LAN.
//
//	yaver tv detect                     list paired Apple TVs
//	yaver tv push                       build the tvOS project in CWD, install, launch
//	yaver tv push --project App.xcodeproj --scheme App
//	yaver tv push --device <udid>       when several TVs are paired
//
// Works for ANY tvOS Xcode project, not just Yaver's own — the project is
// discovered by walking up from the working directory, exactly like
// `yaver wire push` finds a mobile project. Third-party tvOS apps are the point.
//
// Three facts shaped this, each learned against a real Apple TV:
//
//  1. devicectl already speaks tvOS. `installAppOnDevice` /`launchAppOnDevice`
//     shell `xcrun devicectl device install app` and `process launch`, which
//     treat an Apple TV exactly like an iPhone. Nothing there needed changing.
//     What blocked tvOS was discovery: wire_cmd.go deliberately drops anything
//     that is not an iPhone or iPad ("skip non-iOS devices (Apple TV, Vision Pro
//     etc come through too)"). So we re-parse the same devicectl output and keep
//     the TVs it was throwing away.
//
//  2. First pairing cannot be automated. `devicectl manage pair` could not find
//     the Apple TV by UUID, by hostname, or by IP, and the TV never advertises
//     _remotepairing. Only Xcode's Devices window surfaces it, and that window
//     exposes no buttons to Accessibility. So an unpaired TV is a first-class,
//     actionable error here — never a silent failure.
//
//  3. Signing needs a GUI login session. codesign reaches the login keychain,
//     which a Background-session process cannot unlock: it fails with
//     errSecInternalComponent. The agent daemon runs in exactly that session, so
//     a daemon-driven push cannot sign. We detect it and say so, instead of
//     surfacing Apple's opaque error.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// tvDevice is a paired Apple TV as devicectl reports it.
type tvDevice struct {
	UDID      string `json:"udid"`
	Name      string `json:"name"`
	OS        string `json:"os,omitempty"`
	Model     string `json:"model,omitempty"`
	Transport string `json:"transport,omitempty"`
}

// tvProject is a discovered tvOS Xcode project.
type tvProject struct {
	// Path is a .xcworkspace or .xcodeproj.
	Path string
	// IsWorkspace selects the xcodebuild flag (-workspace vs -project).
	IsWorkspace bool
	// Scheme to build. Empty means "let xcodebuild pick the only one".
	Scheme string
	// Root is the directory the project was found in.
	Root string
}

func (p tvProject) flag() string {
	if p.IsWorkspace {
		return "-workspace"
	}
	return "-project"
}

// listTVDevices returns Apple TVs devicectl can see. An Apple TV only appears
// here once it has been paired through Xcode; before that devicectl does not
// know it exists, however reachable it is on the network.
func listTVDevices(ctx context.Context) []tvDevice {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil
	}
	tmp, err := os.CreateTemp("", "yaver-tv-devices-*.json")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, "xcrun", "devicectl", "list", "devices", "--json-output", tmpPath, "--quiet")
	if err := cmd.Run(); err != nil {
		return nil
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var raw struct {
		Result struct {
			Devices []struct {
				Identifier           string `json:"identifier"`
				ConnectionProperties struct {
					TransportType string `json:"transportType"`
				} `json:"connectionProperties"`
				DeviceProperties struct {
					Name            string `json:"name"`
					OSVersionNumber string `json:"osVersionNumber"`
					DeviceClass     string `json:"deviceClass"`
				} `json:"deviceProperties"`
				HardwareProperties struct {
					DeviceType  string `json:"deviceType"`
					ProductType string `json:"productType"`
					Platform    string `json:"platform"`
					UDID        string `json:"udid"`
				} `json:"hardwareProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}

	out := make([]tvDevice, 0, 2)
	for _, d := range raw.Result.Devices {
		if !isAppleTVDevice(d.HardwareProperties.Platform, d.HardwareProperties.DeviceType,
			d.DeviceProperties.DeviceClass, d.HardwareProperties.ProductType) {
			continue
		}
		udid := d.HardwareProperties.UDID
		if udid == "" {
			udid = d.Identifier
		}
		if udid == "" {
			continue
		}
		out = append(out, tvDevice{
			UDID:      udid,
			Name:      d.DeviceProperties.Name,
			OS:        d.DeviceProperties.OSVersionNumber,
			Model:     d.HardwareProperties.ProductType,
			Transport: d.ConnectionProperties.TransportType,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// isAppleTVDevice recognises a TV across the several places devicectl spells it.
// Apple has moved this string around between Xcode releases (platform "tvOS" vs
// deviceType "appletv" vs productType "AppleTV14,1"), so match on any of them
// rather than pinning one field.
func isAppleTVDevice(platform, deviceType, deviceClass, productType string) bool {
	blob := strings.ToLower(strings.Join([]string{platform, deviceType, deviceClass, productType}, " "))
	return strings.Contains(blob, "tvos") || strings.Contains(blob, "appletv") || strings.Contains(blob, "apple tv")
}

// detectTVProject walks up from dir looking for an Xcode project that can build
// for tvOS. A workspace wins over a bare project in the same directory, because
// CocoaPods/SPM setups only build through the workspace.
//
// "Can build for tvOS" is decided by the project file mentioning the appletvos
// SDK or a TVOS deployment target — the same signal Xcode uses, and one a
// third-party project sets without knowing Yaver exists.
func detectTVProject(dir string) (tvProject, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return tvProject{}, err
	}
	for cur := abs; ; {
		if p, ok := tvProjectNear(cur); ok {
			return p, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return tvProject{}, fmt.Errorf("no tvOS Xcode project found at or above %s — pass --project <path.xcodeproj>", abs)
}

// tvConventionalSubdirs are where a repo keeps its tvOS target when the root is
// a monorepo rather than an Xcode project. `yaver wire push` descends into
// ./mobile for the same reason: a user standing at the repo root means "this
// repo's TV app", not "nothing here".
var tvConventionalSubdirs = []string{"tvos", "tv", "appletv", "apple-tv"}

// tvProjectNear checks dir itself, then the conventional subdirectories. The
// directory itself wins, so a real project is never shadowed by a stray folder.
func tvProjectNear(dir string) (tvProject, bool) {
	if p, ok := tvProjectIn(dir); ok {
		return p, true
	}
	for _, sub := range tvConventionalSubdirs {
		candidate := filepath.Join(dir, sub)
		if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
			continue
		}
		if p, ok := tvProjectIn(candidate); ok {
			return p, true
		}
	}
	return tvProject{}, false
}

func tvProjectIn(dir string) (tvProject, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tvProject{}, false
	}
	var workspace, project string
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".xcworkspace":
			// Skip the one Xcode auto-generates inside every .xcodeproj.
			if !strings.HasSuffix(dir, ".xcodeproj") {
				workspace = filepath.Join(dir, e.Name())
			}
		case ".xcodeproj":
			project = filepath.Join(dir, e.Name())
		}
	}
	if project == "" && workspace == "" {
		return tvProject{}, false
	}
	// tvOS capability is decided by the .xcodeproj, even when we build the
	// workspace: the workspace itself declares no platforms.
	probe := project
	if probe == "" {
		probe = strings.TrimSuffix(workspace, ".xcworkspace") + ".xcodeproj"
	}
	if !projectSupportsTVOS(probe) {
		return tvProject{}, false
	}
	if workspace != "" {
		return tvProject{Path: workspace, IsWorkspace: true, Root: dir, Scheme: schemeGuess(workspace)}, true
	}
	return tvProject{Path: project, Root: dir, Scheme: schemeGuess(project)}, true
}

func projectSupportsTVOS(xcodeproj string) bool {
	data, err := os.ReadFile(filepath.Join(xcodeproj, "project.pbxproj"))
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "appletvos") ||
		strings.Contains(s, "TVOS_DEPLOYMENT_TARGET") ||
		strings.Contains(s, "SUPPORTED_PLATFORMS = \"appletvos")
}

// schemeGuess uses the project's own name, which is the scheme Xcode creates by
// default. Callers override with --scheme when a project ships several.
func schemeGuess(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(strings.TrimSuffix(base, ".xcworkspace"), ".xcodeproj")
}

// tvPushOpts is the parsed `yaver tv push` invocation.
type tvPushOpts struct {
	projectPath string
	scheme      string
	config      string
	deviceUDID  string
	bundleID    string
	noLaunch    bool
}

func runTVCommand(args []string) {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "yaver tv: Apple TV install requires macOS + Xcode (xcrun devicectl)")
		os.Exit(1)
	}
	if len(args) == 0 {
		printTVUsage()
		return
	}
	switch args[0] {
	case "detect", "devices", "list":
		runTVDetect()
	case "push", "install":
		runTVPush(args[1:])
	case "help", "-h", "--help":
		printTVUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown tv subcommand: %s\n\n", args[0])
		printTVUsage()
		os.Exit(1)
	}
}

func runTVDetect() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	devices := listTVDevices(ctx)
	if len(devices) == 0 {
		fmt.Println(tvNoDeviceHelp())
		return
	}
	fmt.Printf("Paired Apple TV(s):\n")
	for _, d := range devices {
		fmt.Printf("  %-24s %s  tvOS %s  [%s]\n", d.Name, d.Model, d.OS, d.Transport)
		fmt.Printf("    udid: %s\n", d.UDID)
	}
}

// tvNoDeviceHelp explains the ONE thing that cannot be automated. Pairing an
// Apple TV requires Xcode's Devices window: devicectl cannot discover an
// unpaired TV (by UUID, hostname, or IP), and the TV does not advertise the
// _remotepairing service that CoreDevice looks for.
func tvNoDeviceHelp() string {
	return strings.Join([]string{
		"No paired Apple TV found.",
		"",
		"An Apple TV must be paired through Xcode once — devicectl cannot discover",
		"an unpaired TV, and Apple will not issue a tvOS provisioning profile until",
		"the device is registered to your team.",
		"",
		"  1. Apple TV: Settings -> Remotes and Devices -> Remote App and Devices",
		"  2. Mac:      Xcode -> Window -> Devices and Simulators (Shift-Cmd-2)",
		"  3. Click the Apple TV under 'Discovered', press Pair,",
		"     and type the 6-digit code the TV shows.",
		"",
		"Then `yaver tv detect` will list it, and `yaver tv push` will install.",
		"",
		"To only RUN an already-released build, TestFlight on the Apple TV needs",
		"none of this.",
	}, "\n")
}

func runTVPush(args []string) {
	opts := parseTVPushArgs(args)

	if reason := tvSigningSessionWarning(); reason != "" {
		fmt.Fprintln(os.Stderr, reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// Project first: it is the cheaper check, and it is the one the user got
	// wrong when they ran this from the wrong directory. Complaining about a
	// missing Apple TV to someone standing in an iOS repo helps nobody.
	project, err := resolveTVProject(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver tv push: %v\n", err)
		os.Exit(1)
	}

	scheme := opts.scheme
	if scheme == "" {
		scheme = project.Scheme
	}
	config := opts.config
	if config == "" {
		config = "Debug"
	}
	// Say what we found before the next thing can fail, so a wrong project is
	// obvious even when the failure is about something else.
	fmt.Printf("→ project: %s (scheme %s, %s)\n", project.Path, scheme, config)

	device, err := resolveTVDevice(ctx, opts.deviceUDID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("→ target:  %s [%s]\n", device.Name, device.UDID)

	appPath, err := buildTVApp(ctx, project, scheme, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver tv push: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("→ built:   %s\n", appPath)

	bundleID := opts.bundleID
	if bundleID == "" {
		bundleID = readBundleIDFromApp(appPath)
	}

	if err := installTVApp(ctx, device.UDID, appPath); err != nil {
		fmt.Fprintf(os.Stderr, "yaver tv push: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("App installed: %s\n", filepath.Base(appPath))
	if bundleID != "" {
		fmt.Printf("  bundleID: %s\n", bundleID)
	}

	if opts.noLaunch || bundleID == "" {
		return
	}
	if err := launchAppOnDevice(ctx, device.UDID, bundleID); err != nil {
		// The app is on the TV; a failed launch is a nuisance, not a failure.
		fmt.Fprintf(os.Stderr, "  (installed, but launch failed: %v)\n", err)
		return
	}
	fmt.Printf("  launched on %s\n", device.Name)
}

func parseTVPushArgs(args []string) tvPushOpts {
	var o tvPushOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--project" || a == "-p":
			o.projectPath = next()
		case strings.HasPrefix(a, "--project="):
			o.projectPath = strings.TrimPrefix(a, "--project=")
		case a == "--scheme" || a == "-s":
			o.scheme = next()
		case strings.HasPrefix(a, "--scheme="):
			o.scheme = strings.TrimPrefix(a, "--scheme=")
		case a == "--configuration" || a == "-c":
			o.config = next()
		case strings.HasPrefix(a, "--configuration="):
			o.config = strings.TrimPrefix(a, "--configuration=")
		case a == "--device" || a == "-d":
			o.deviceUDID = next()
		case strings.HasPrefix(a, "--device="):
			o.deviceUDID = strings.TrimPrefix(a, "--device=")
		case strings.HasPrefix(a, "--bundle-id="):
			o.bundleID = strings.TrimPrefix(a, "--bundle-id=")
		case a == "--no-launch":
			o.noLaunch = true
		}
	}
	return o
}

func resolveTVProject(opts tvPushOpts) (tvProject, error) {
	if p := strings.TrimSpace(opts.projectPath); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return tvProject{}, err
		}
		if _, err := os.Stat(abs); err != nil {
			return tvProject{}, fmt.Errorf("no such project: %s", abs)
		}
		return tvProject{
			Path:        abs,
			IsWorkspace: strings.HasSuffix(abs, ".xcworkspace"),
			Root:        filepath.Dir(abs),
			Scheme:      schemeGuess(abs),
		}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return tvProject{}, err
	}
	return detectTVProject(cwd)
}

func resolveTVDevice(ctx context.Context, wantUDID string) (tvDevice, error) {
	devices := listTVDevices(ctx)
	if len(devices) == 0 {
		return tvDevice{}, fmt.Errorf("%s", tvNoDeviceHelp())
	}
	if u := strings.TrimSpace(wantUDID); u != "" {
		for _, d := range devices {
			if strings.EqualFold(d.UDID, u) || strings.EqualFold(d.Name, u) {
				return d, nil
			}
		}
		return tvDevice{}, fmt.Errorf("no paired Apple TV matches %q — run `yaver tv detect`", u)
	}
	if len(devices) == 1 {
		return devices[0], nil
	}
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, d.Name)
	}
	return tvDevice{}, fmt.Errorf("several Apple TVs are paired (%s) — pick one with --device <udid|name>",
		strings.Join(names, ", "))
}

// tvSigningSessionWarning returns a warning when this process cannot reach the
// login keychain. codesign then fails with the opaque `errSecInternalComponent`,
// and the user is left guessing. The agent daemon runs in the Background
// session, so an MCP-driven push hits this every time.
func tvSigningSessionWarning() string {
	out, err := exec.Command("launchctl", "managername").Output()
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(out)) == "Aqua" {
		return ""
	}
	return strings.Join([]string{
		"warning: this process is not in a GUI login session (launchctl managername != Aqua).",
		"         codesign cannot unlock the login keychain here and will fail with",
		"         errSecInternalComponent. Run `yaver tv push` from Terminal.app.",
	}, "\n")
}

// buildTVApp builds for a real Apple TV and returns the .app it produced.
// Automatic signing plus -allowProvisioningUpdates lets Xcode mint the tvOS
// profile — which is why the device must already be registered to the team,
// i.e. paired.
func buildTVApp(ctx context.Context, project tvProject, scheme, config string) (string, error) {
	derived, err := os.MkdirTemp("", "yaver-tv-build-*")
	if err != nil {
		return "", err
	}

	args := []string{
		project.flag(), project.Path,
		"-configuration", config,
		"-sdk", "appletvos",
		"-derivedDataPath", derived,
		"-allowProvisioningUpdates",
	}
	if scheme != "" {
		args = append(args, "-scheme", scheme)
	}
	if team := strings.TrimSpace(os.Getenv("APPLE_TEAM_ID")); team != "" {
		args = append(args, "DEVELOPMENT_TEAM="+team, "CODE_SIGN_STYLE=Automatic")
	}
	// The App Store Connect key lets xcodebuild register the device and create
	// the profile without a signed-in Xcode GUI.
	if k := strings.TrimSpace(os.Getenv("APP_STORE_KEY_PATH")); k != "" {
		args = append(args,
			"-authenticationKeyPath", k,
			"-authenticationKeyID", os.Getenv("APP_STORE_KEY_ID"),
			"-authenticationKeyIssuerID", os.Getenv("APP_STORE_KEY_ISSUER"))
	}
	args = append(args, "build")

	cmd := exec.CommandContext(ctx, "xcodebuild", args...)
	cmd.Dir = project.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xcodebuild failed: %w\n%s", err, tvBuildErrorHint(string(out)))
	}
	app := findTVAppInDerivedData(derived)
	if app == "" {
		return "", fmt.Errorf("build reported success but produced no .app under %s", derived)
	}
	return app, nil
}

// tvBuildErrorHint pulls the lines that matter out of xcodebuild's wall of
// output, and translates the two failures a tvOS push actually hits.
func tvBuildErrorHint(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if strings.Contains(l, "error:") || strings.Contains(l, "** BUILD FAILED **") {
			keep = append(keep, "  "+l)
		}
	}
	if len(keep) > 6 {
		keep = keep[len(keep)-6:]
	}
	msg := strings.Join(keep, "\n")
	switch {
	case strings.Contains(out, "no devices from which to generate a provisioning profile"):
		msg += "\n\nApple has no tvOS device registered to your team. Pair the Apple TV first:\n" + tvNoDeviceHelp()
	case strings.Contains(out, "errSecInternalComponent"):
		msg += "\n\ncodesign could not reach the login keychain. " + strings.TrimSpace(tvSigningSessionWarning())
	}
	return msg
}

func findTVAppInDerivedData(derived string) string {
	for _, pattern := range []string{
		filepath.Join(derived, "Build", "Products", "*-appletvos", "*.app"),
		filepath.Join(derived, "Build", "Products", "*", "*.app"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if !strings.Contains(m, "appletvsimulator") {
				return m
			}
		}
	}
	return ""
}

// installTVApp is installAppOnDevice with the device chosen by the caller.
// The underlying devicectl call is identical — it never cared what kind of
// device it was talking to.
func installTVApp(ctx context.Context, udid, appPath string) error {
	cmd := exec.CommandContext(ctx, "xcrun", "devicectl", "device", "install", "app", "--device", udid, appPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("devicectl install failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func printTVUsage() {
	fmt.Print(`yaver tv — build and install a tvOS app on a paired Apple TV

  yaver tv detect                 list paired Apple TVs
  yaver tv push                   build the tvOS project in this directory, install, launch
  yaver tv push --project <path>  explicit .xcodeproj / .xcworkspace
  yaver tv push --scheme <name>   pick a scheme (default: the project's name)
  yaver tv push --device <udid>   pick a TV when several are paired
  yaver tv push --no-launch       install without launching

Works with any tvOS Xcode project, yours or a third party's — the project is
found by walking up from the current directory.

Pairing is a one-time manual step (Apple gives no way to script it):
  Apple TV: Settings -> Remotes and Devices -> Remote App and Devices
  Xcode:    Window -> Devices and Simulators -> select the TV -> Pair

Run from Terminal.app: codesign needs the login keychain, which a background
process cannot unlock.
`)
}
