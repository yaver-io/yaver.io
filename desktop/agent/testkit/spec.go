// Package testkit is the embedded test runner for yaver-test-sdk.
//
// A spec is a YAML file describing a flow against a target (web today,
// ios-sim/android-emu/device later). The runner ships inside the yaver
// Go binary — no external Playwright/Selenium/ChromeDriver dependency.
//
// The point of yaver-test-sdk is to make CI for a solo developer cost
// $0/month by running every test on the developer's own machine. So:
//
//   - Specs live in the user's repo at `yaver-tests/**/*.test.yaml`,
//     versioned in git.
//   - The runner is a single Go binary the user already has installed.
//   - No telemetry. No "phone home." No Convex calls.
//   - Artifacts (screenshots, traces) land on local disk only. The
//     mobile app may pull them later via the existing P2P channel; they
//     never touch a central server.
//
// Supported host platforms: macOS and Linux only. Windows is explicitly
// out of scope for the local-CI use case — the persona is a solo
// full-stack mobile dev who is on a Mac for iOS work or on Linux for
// everything else.
package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Target is the kind of system the spec drives.
type Target string

const (
	TargetWeb        Target = "web"         // headless / headful Chromium via CDP (chromedp)
	TargetIOSSim     Target = "ios-sim"     // iOS Simulator via simctl + WebDriverAgent (M5)
	TargetAndroidEmu Target = "android-emu" // Android Emulator via emulator + UIAutomator2 (M5)
	TargetDevice     Target = "device"      // Physical device (M5)
)

// Spec is one yaver-tests/*.test.yaml file.
type Spec struct {
	// Name is a human label for the test (also used in reports).
	Name string `yaml:"name"`

	// Target picks the driver. Defaults to TargetWeb.
	Target Target `yaml:"target,omitempty"`

	// URL is the page to open for web targets.
	URL string `yaml:"url,omitempty"`

	// App is the path to a built .app/.apk for mobile targets (M5+).
	App string `yaml:"app,omitempty"`

	// Viewport sets the browser viewport for web targets. Optional.
	Viewport *Viewport `yaml:"viewport,omitempty"`

	// Headful runs the browser visibly so the dev can watch it. Default headless.
	Headful bool `yaml:"headful,omitempty"`

	// Timeout is the per-step default timeout. Defaults to 7s.
	TimeoutMS int `yaml:"timeout_ms,omitempty"`

	// Setup runs once before Steps. Failure in setup fails the whole spec.
	Setup []Step `yaml:"setup,omitempty"`

	// Steps is the test body.
	Steps []Step `yaml:"steps"`

	// Teardown runs once after Steps regardless of pass/fail.
	Teardown []Step `yaml:"teardown,omitempty"`

	// Artifacts controls what gets captured on failure.
	Artifacts ArtifactsConfig `yaml:"artifacts,omitempty"`

	// Path is the absolute path of the spec file. Set by LoadSpec, not the
	// user.
	Path string `yaml:"-"`
}

// Viewport sets the browser window size for web targets.
type Viewport struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

// ArtifactsConfig controls failure capture. By default we screenshot on
// failure; trace and video are opt-in.
type ArtifactsConfig struct {
	// On is when to capture: "always", "failure" (default), "never".
	On string `yaml:"on,omitempty"`

	// Screenshot captures a PNG. Default true.
	Screenshot *bool `yaml:"screenshot,omitempty"`

	// Trace captures a CDP trace zip (M6).
	Trace bool `yaml:"trace,omitempty"`

	// Video captures the run as mp4 (M6).
	Video bool `yaml:"video,omitempty"`
}

// Step is a single action in the spec. Exactly one of the action fields
// (Goto / Click / Fill / WaitFor / Assert / etc.) is set per step. The
// YAML uses the action name as a key directly so a spec reads cleanly:
//
//	steps:
//	  - goto: /auth
//	  - click: 'button:has-text("Sign In")'
//	  - assert.visible: 'text=Welcome'
//
// We accept either form via custom unmarshalling below.
type Step struct {
	// Goto navigates to a URL or path (path joined with Spec.URL).
	Goto string `yaml:"goto,omitempty"`

	// Click clicks the first element matching the CSS selector.
	Click string `yaml:"click,omitempty"`

	// Fill is { selector, text } — fills an input.
	Fill *FillStep `yaml:"fill,omitempty"`

	// WaitFor waits until a CSS selector is visible.
	WaitFor string `yaml:"wait_for,omitempty"`

	// WaitForURL waits until the page URL matches the substring.
	WaitForURL string `yaml:"wait_for_url,omitempty"`

	// Sleep pauses for N milliseconds. Use sparingly.
	SleepMS int `yaml:"sleep_ms,omitempty"`

	// AssertVisible asserts the selector is visible on the page.
	AssertVisible string `yaml:"assert.visible,omitempty"`

	// AssertText asserts the page contains the given substring.
	AssertText string `yaml:"assert.text,omitempty"`

	// AssertTitle asserts the page title contains the given substring.
	AssertTitle string `yaml:"assert.title,omitempty"`

	// AssertURL asserts the current URL contains the given substring.
	AssertURL string `yaml:"assert.url,omitempty"`

	// Screenshot saves a PNG named with the current step index.
	Screenshot bool `yaml:"screenshot,omitempty"`

	// Snapshot captures a visual baseline (or compares against one).
	// Value is the snapshot's name (used as the PNG filename under
	// `<spec dir>/snapshots/<name>.png`). First run writes the
	// baseline, subsequent runs diff against it.
	Snapshot string `yaml:"snapshot,omitempty"`

	// Inspect runs an LLM visual inspection on the current screenshot.
	// The dev's own provider (Mistral / OpenAI / Anthropic / Ollama)
	// is used; the API key never leaves the agent. Empty value uses
	// the default question; a string value overrides it.
	Inspect string `yaml:"inspect,omitempty"`

	// Eval runs raw JavaScript in the page context. Result is logged.
	Eval string `yaml:"eval,omitempty"`
}

// FillStep is the body of a `fill` step.
type FillStep struct {
	Selector string `yaml:"selector"`
	Text     string `yaml:"text"`
}

// LoadSpec parses a single spec file from disk.
func LoadSpec(path string) (*Spec, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", abs, err)
	}
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %q: %w", abs, err)
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	if s.Target == "" {
		s.Target = TargetWeb
	}
	if s.TimeoutMS == 0 {
		s.TimeoutMS = 7000
	}
	if s.Artifacts.On == "" {
		s.Artifacts.On = "failure"
	}
	if s.Artifacts.Screenshot == nil {
		yes := true
		s.Artifacts.Screenshot = &yes
	}
	s.Path = abs
	return &s, nil
}

// DiscoverSpecs walks `root` looking for *.test.yaml or *.test.yml files.
// Files are returned in lexical order so runs are deterministic.
func DiscoverSpecs(root string) ([]*Spec, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var paths []string
	err = filepath.Walk(abs, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// If the root doesn't exist, return a clear error from the
			// caller side. For child errors, skip silently.
			if p == abs {
				return walkErr
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".test.yaml") || strings.HasSuffix(name, ".test.yml") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Stable order for deterministic runs.
	sortPaths(paths)
	specs := make([]*Spec, 0, len(paths))
	for _, p := range paths {
		s, err := LoadSpec(p)
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	return specs, nil
}

// sortPaths sorts the slice in place. Pulled out so we don't import
// `sort` at the top of the file just for one call (and so the test file
// can stub it if needed).
func sortPaths(paths []string) {
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && paths[j-1] > paths[j]; j-- {
			paths[j-1], paths[j] = paths[j], paths[j-1]
		}
	}
}

// Validate returns an error if the spec is malformed.
func (s *Spec) Validate() error {
	if s.Target != TargetWeb && s.Target != TargetIOSSim && s.Target != TargetAndroidEmu && s.Target != TargetDevice {
		return fmt.Errorf("unknown target %q (supported today: web)", s.Target)
	}
	if s.Target == TargetWeb && s.URL == "" {
		// We allow URL-less specs if every Goto is absolute, but flag the
		// common mistake.
		for _, st := range s.Steps {
			if st.Goto != "" && !strings.HasPrefix(st.Goto, "http://") && !strings.HasPrefix(st.Goto, "https://") {
				return fmt.Errorf("spec %q: goto %q is a path but no top-level url is set", s.Name, st.Goto)
			}
		}
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("spec %q: no steps", s.Name)
	}
	return nil
}
