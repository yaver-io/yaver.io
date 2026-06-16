package main

// droid_interactive.go — generic interactive / human-in-the-loop control of a
// paired Android device over adb.
//
// Mirrors browser_interactive.go but for a physical (or emulated) Android phone:
// it streams the screen as PNG frames and relays tap/text/key/swipe input so a
// human can do something automation can't — e.g. enter an SMS OTP during login
// — after which automation drives the same device.
//
// This is GENERIC — it has no knowledge of any particular app. It just exposes
// raw adb screen/input primitives plus a couple of read/launch conveniences.
//
// It reuses the SAME adb invocation path as the rest of the agent: shelling out
// to the `adb` binary on PATH via os/exec, with an optional `-s <serial>` to
// target a specific device (see remote_runtime_dims.go, mcp_wire_tools.go,
// native_build.go). There is no second adb path.

import (
	"context"
	"encoding/xml"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// adbArgs prepends `-s <serial>` when a serial is given, so every helper targets
// the same device the caller asked for (and the default device otherwise).
func adbArgs(serial string, rest ...string) []string {
	args := []string{}
	if strings.TrimSpace(serial) != "" {
		args = append(args, "-s", serial)
	}
	return append(args, rest...)
}

// runAdb runs `adb [-s serial] <rest...>` with a timeout and returns stdout.
func runAdb(serial string, timeout time.Duration, rest ...string) ([]byte, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, "adb", adbArgs(serial, rest...)...).Output()
}

// droidPickDevice returns the first `device`-state serial from `adb devices`,
// or "" when no usable device is attached. Lines like
// "emulator-5554\tdevice" → serial; "...\tunauthorized|offline" are skipped.
func droidPickDevice() string {
	out, err := runAdb("", 8*time.Second, "devices")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			return fields[0]
		}
	}
	return ""
}

// droidFrame captures the current screen as PNG bytes via
// `adb -s <serial> exec-out screencap -p`.
func droidFrame(serial string) ([]byte, error) {
	// screencap can take a moment on slow devices; give it a generous timeout.
	out, err := runAdb(serial, 25*time.Second, "exec-out", "screencap", "-p")
	if err != nil {
		return nil, fmt.Errorf("droid frame: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("droid frame: empty screencap (device locked or no display?)")
	}
	return out, nil
}

// droidSize parses `adb -s <serial> shell wm size`
// ("Physical size: 1080x2400", optionally "Override size: ...") and returns the
// effective width/height. Returns (0,0) on failure.
func droidSize(serial string) (w, h int) {
	out, err := runAdb(serial, 8*time.Second, "shell", "wm", "size")
	if err != nil {
		return 0, 0
	}
	var override bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(line, "Override size:"):
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Override size:"))
			override = true
		case strings.HasPrefix(line, "Physical size:") && !override:
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Physical size:"))
		default:
			continue
		}
		parts := strings.SplitN(rest, "x", 2)
		if len(parts) != 2 {
			continue
		}
		pw, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		ph, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if e1 == nil && e2 == nil && pw > 0 && ph > 0 {
			w, h = pw, ph
			if override {
				break
			}
		}
	}
	return w, h
}

// droidTap dispatches `adb shell input tap <x> <y>`.
func droidTap(serial string, x, y int) error {
	_, err := runAdb(serial, 8*time.Second, "shell", "input", "tap",
		strconv.Itoa(x), strconv.Itoa(y))
	if err != nil {
		return fmt.Errorf("droid tap: %w", err)
	}
	return nil
}

// droidText types text via `adb shell input text <text>`. Spaces are encoded as
// %s, which `input text` decodes back to spaces (a raw space would split the
// argument). Other shell metacharacters are escaped so they reach the device.
func droidText(serial, text string) error {
	encoded := strings.ReplaceAll(text, " ", "%s")
	_, err := runAdb(serial, 8*time.Second, "shell", "input", "text", encoded)
	if err != nil {
		return fmt.Errorf("droid text: %w", err)
	}
	return nil
}

// droidKey dispatches `adb shell input keyevent <keycode>` (e.g. 66=ENTER,
// 67=DEL, 4=BACK, 3=HOME).
func droidKey(serial string, keycode int) error {
	_, err := runAdb(serial, 8*time.Second, "shell", "input", "keyevent",
		strconv.Itoa(keycode))
	if err != nil {
		return fmt.Errorf("droid key: %w", err)
	}
	return nil
}

// droidSwipe dispatches `adb shell input swipe <x1> <y1> <x2> <y2> <dur>`.
// dur is the swipe duration in milliseconds (defaults to 300 when <= 0).
func droidSwipe(serial string, x1, y1, x2, y2, dur int) error {
	if dur <= 0 {
		dur = 300
	}
	_, err := runAdb(serial, 10*time.Second, "shell", "input", "swipe",
		strconv.Itoa(x1), strconv.Itoa(y1), strconv.Itoa(x2), strconv.Itoa(y2),
		strconv.Itoa(dur))
	if err != nil {
		return fmt.Errorf("droid swipe: %w", err)
	}
	return nil
}

var droidTextAttrRe = regexp.MustCompile(`text="([^"]*)"`)

type droidUINode struct {
	Text        string `json:"text,omitempty"`
	Description string `json:"description,omitempty"`
	ResourceID  string `json:"resourceId,omitempty"`
	Class       string `json:"class,omitempty"`
	Package     string `json:"package,omitempty"`
	Bounds      string `json:"bounds,omitempty"`
	X           int    `json:"x,omitempty"`
	Y           int    `json:"y,omitempty"`
	W           int    `json:"w,omitempty"`
	H           int    `json:"h,omitempty"`
	Clickable   bool   `json:"clickable,omitempty"`
	Enabled     bool   `json:"enabled,omitempty"`
	Focusable   bool   `json:"focusable,omitempty"`
	Password    bool   `json:"password,omitempty"`
}

type droidXMLNode struct {
	XMLName     xml.Name       `xml:"node"`
	Text        string         `xml:"text,attr"`
	Description string         `xml:"content-desc,attr"`
	ResourceID  string         `xml:"resource-id,attr"`
	Class       string         `xml:"class,attr"`
	Package     string         `xml:"package,attr"`
	Bounds      string         `xml:"bounds,attr"`
	Clickable   string         `xml:"clickable,attr"`
	Enabled     string         `xml:"enabled,attr"`
	Focusable   string         `xml:"focusable,attr"`
	Password    string         `xml:"password,attr"`
	Children    []droidXMLNode `xml:"node"`
}

type droidXMLHierarchy struct {
	Children []droidXMLNode `xml:"node"`
}

func droidUIDumpXML(serial string) ([]byte, error) {
	if _, err := runAdb(serial, 20*time.Second, "shell", "uiautomator", "dump", "/sdcard/ui.xml"); err != nil {
		return nil, fmt.Errorf("droid ui dump: %w", err)
	}
	out, err := runAdb(serial, 10*time.Second, "shell", "cat", "/sdcard/ui.xml")
	if err != nil {
		return nil, fmt.Errorf("droid ui read: %w", err)
	}
	return out, nil
}

// droidUITexts dumps the current view hierarchy via uiautomator and returns the
// non-empty text="..." values — handy for reading on-screen labels / fields
// (e.g. to confirm a login form is showing). Best-effort: uiautomator can fail
// on protected screens, in which case an error is returned.
func droidUITexts(serial string) ([]string, error) {
	// Dump to a known path, then read it back. Two calls because `adb exec-out
	// uiautomator dump /dev/tty` is unreliable across devices.
	out, err := droidUIDumpXML(serial)
	if err != nil {
		return nil, err
	}
	var texts []string
	seen := map[string]bool{}
	for _, m := range droidTextAttrRe.FindAllStringSubmatch(string(out), -1) {
		v := strings.TrimSpace(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		texts = append(texts, v)
	}
	return texts, nil
}

func droidUIElements(serial string, limit int) ([]droidUINode, error) {
	if limit <= 0 || limit > 250 {
		limit = 120
	}
	out, err := droidUIDumpXML(serial)
	if err != nil {
		return nil, err
	}
	var h droidXMLHierarchy
	if err := xml.Unmarshal(out, &h); err != nil {
		return nil, fmt.Errorf("droid ui parse: %w", err)
	}
	nodes := make([]droidUINode, 0, limit)
	var walk func([]droidXMLNode)
	walk = func(in []droidXMLNode) {
		for _, n := range in {
			if len(nodes) >= limit {
				return
			}
			label := strings.TrimSpace(firstNonEmpty(n.Text, n.Description, n.ResourceID))
			clickable := strings.EqualFold(n.Clickable, "true")
			focusable := strings.EqualFold(n.Focusable, "true")
			password := strings.EqualFold(n.Password, "true")
			x, y, ww, hh := droidParseBounds(n.Bounds)
			if label != "" || clickable || focusable {
				nodes = append(nodes, droidUINode{
					Text:        strings.TrimSpace(n.Text),
					Description: strings.TrimSpace(n.Description),
					ResourceID:  strings.TrimSpace(n.ResourceID),
					Class:       strings.TrimSpace(n.Class),
					Package:     strings.TrimSpace(n.Package),
					Bounds:      strings.TrimSpace(n.Bounds),
					X:           x,
					Y:           y,
					W:           ww,
					H:           hh,
					Clickable:   clickable,
					Enabled:     strings.EqualFold(n.Enabled, "true"),
					Focusable:   focusable,
					Password:    password,
				})
			}
			walk(n.Children)
		}
	}
	walk(h.Children)
	return nodes, nil
}

func droidParseBounds(bounds string) (x, y, w, h int) {
	var x1, y1, x2, y2 int
	if _, err := fmt.Sscanf(bounds, "[%d,%d][%d,%d]", &x1, &y1, &x2, &y2); err != nil {
		return 0, 0, 0, 0
	}
	if x2 < x1 || y2 < y1 {
		return 0, 0, 0, 0
	}
	return x1 + (x2-x1)/2, y1 + (y2-y1)/2, x2 - x1, y2 - y1
}

// droidFocus returns the currently focused/resumed activity (package/activity)
// via `adb shell dumpsys activity activities | grep mResumedActivity`.
// Returns "" when it can't be determined.
func droidFocus(serial string) string {
	out, err := runAdb(serial, 10*time.Second, "shell", "dumpsys", "activity", "activities")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "mResumedActivity") {
			continue
		}
		// e.g. "mResumedActivity: ActivityRecord{... u0 com.app/.MainActivity t123}"
		for _, tok := range strings.Fields(line) {
			if strings.Contains(tok, "/") {
				return strings.TrimRight(tok, "}")
			}
		}
	}
	return ""
}

// droidLaunchPackage finds an installed package whose id contains pkgSubstr and
// launches it via monkey's LAUNCHER intent. Returns the resolved package name.
func droidLaunchPackage(serial, pkgSubstr string) (string, error) {
	pkgSubstr = strings.TrimSpace(pkgSubstr)
	if pkgSubstr == "" {
		return "", fmt.Errorf("droid launch: package substring is required")
	}
	out, err := runAdb(serial, 12*time.Second, "shell", "pm", "list", "packages")
	if err != nil {
		return "", fmt.Errorf("droid list packages: %w", err)
	}
	var pkg string
	var exact string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		name := strings.TrimPrefix(line, "package:")
		if name == "" || !strings.Contains(name, pkgSubstr) {
			continue
		}
		if name == pkgSubstr {
			exact = name
			break
		}
		if pkg == "" {
			pkg = name
		}
	}
	if exact != "" {
		pkg = exact
	}
	if pkg == "" {
		return "", fmt.Errorf("droid launch: no installed package matches %q", pkgSubstr)
	}
	if _, err := runAdb(serial, 12*time.Second, "shell", "monkey", "-p", pkg,
		"-c", "android.intent.category.LAUNCHER", "1"); err != nil {
		return pkg, fmt.Errorf("droid launch %q: %w", pkg, err)
	}
	return pkg, nil
}

func droidInstalledPackages(serial, filter string, limit int) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	out, err := runAdb(serial, 15*time.Second, "shell", "pm", "list", "packages")
	if err != nil {
		return nil, fmt.Errorf("droid list packages: %w", err)
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	var packages []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "package:"))
		if name == "" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
			continue
		}
		packages = append(packages, name)
		if len(packages) >= limit {
			break
		}
	}
	return packages, nil
}
