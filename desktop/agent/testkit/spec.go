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
	TargetWeb            Target = "web"             // headless / headful Chromium via CDP (chromedp)
	TargetIOSSim         Target = "ios-sim"         // iOS Simulator via simctl + WebDriverAgent (M5)
	TargetAndroidEmu     Target = "android-emu"     // Android Emulator via emulator + UIAutomator2 (M5)
	TargetAndroidRedroid Target = "android-redroid" // Android-in-Docker via the Studio redroid surface (no adb/AVD, no KVM)
	TargetDevice         Target = "device"          // Physical device (M5)
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

	// Redroid configures the android-redroid target (Android-in-Docker via the
	// Studio capture surface — no adb, no emulator, no KVM). Only read when
	// Target == android-redroid. Optional: dirs default under ~/.yaver on a
	// local farm box.
	Redroid *RedroidSpec `yaml:"redroid,omitempty"`

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

	// Capture turns on instrumentation streams (console errors,
	// network requests, performance metrics, accessibility audit).
	// The runner wires these into chromedp's CDP subscriptions and
	// writes the results as JSON + HAR + axe reports under the spec's
	// artifacts dir.
	Capture CaptureConfig `yaml:"capture,omitempty"`

	// Include is a list of paths to other *.test.yaml files whose
	// `steps:` blocks are inlined at load time. Lets the solo dev
	// extract "login as test user" once and reuse it across every
	// spec without copy-pasting.
	Include []string `yaml:"include,omitempty"`

	// NetworkProfile applies a CDP Network.emulateNetworkConditions
	// preset for the whole spec. Useful for catching PWA / offline
	// UX regressions without wiring a separate Playwright config.
	// Supported values:
	//   "fast-3g"  — 1.6 Mbps down / 768 Kbps up / 150ms RTT
	//   "slow-3g"  — 500 Kbps down / 500 Kbps up / 400ms RTT
	//   "2g"       — 250 Kbps down / 250 Kbps up / 800ms RTT
	//   "offline"  — full network blackout (for service worker tests)
	// Empty / "online" = no emulation (default).
	NetworkProfile string `yaml:"network_profile,omitempty"`

	// Path is the absolute path of the spec file. Set by LoadSpec, not the
	// user.
	Path string `yaml:"-"`
}

// Viewport sets the browser window size for web targets.
type Viewport struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

// RedroidSpec parameterizes the android-redroid target. The surface runs on a
// Docker host reached through the Studio runner seam: the local farm box
// (default) or an on-prem host (ssh_host). Set `base` to restore a warm Yaver
// Base Image instead of cold-booting — the fast path.
type RedroidSpec struct {
	Image       string `yaml:"image,omitempty"`        // redroid image; default redroid 13
	HostWorkDir string `yaml:"host_workdir,omitempty"` // /data bind-mount on the surface host (ssh: required)
	SSHHost     string `yaml:"ssh_host,omitempty"`     // on-prem host; empty = local farm box
	SSHOpts     string `yaml:"ssh_opts,omitempty"`     // extra ssh/scp options
	Container   string `yaml:"container,omitempty"`    // container name; default yaver-qa
	Base        string `yaml:"base,omitempty"`         // restore this Yaver Base Image version instead of cold boot
	SnapshotDir string `yaml:"snapshot_dir,omitempty"` // base snapshot store (used with base)
	Package     string `yaml:"package,omitempty"`      // app package id to launch; default from url
	Activity    string `yaml:"activity,omitempty"`     // optional explicit launch activity
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

	// A11y runs an axe-core audit against the current page. Fails
	// the step if any violations at or above `min_impact` are found.
	// The full violation list is written to the spec's artifact dir
	// so the dev can scroll it on their phone.
	A11y *A11yStep `yaml:"a11y,omitempty"`

	// SaveHAR dumps the network capture accumulated so far as a
	// HAR 1.2 file under <spec>/.yaver-test-results/<name>.har.
	// Requires capture.network to be on.
	SaveHAR string `yaml:"save_har,omitempty"`

	// Eval runs raw JavaScript in the page context. Result is logged.
	Eval string `yaml:"eval,omitempty"`

	// Include is a path to another *.test.yaml whose setup+steps are
	// inlined at this position instead of at the top of setup. Lets
	// the dev drop a "log in as admin" macro in the middle of a flow:
	//
	//	steps:
	//	  - goto: /
	//	  - click: 'a.dashboard'
	//	  - include: macros/admin-login.test.yaml
	//	  - click: 'button.delete-user'
	//
	// When set, the other step fields on this Step are ignored — it
	// is purely a position marker. Resolved at LoadSpec time; the
	// runner never sees the include step itself.
	Include string `yaml:"include,omitempty"`
}

// A11yStep controls an accessibility audit step.
type A11yStep struct {
	// MinImpact is the minimum axe-core impact level to treat as a
	// failure: "minor" | "moderate" | "serious" | "critical".
	// Default "serious" so solo devs don't get flooded by minor
	// nits on the first run.
	MinImpact string `yaml:"min_impact,omitempty"`
	// Tags is an optional list of axe-core rule tags to include
	// (wcag2a, wcag21aa, best-practice, etc). Empty = axe defaults.
	Tags []string `yaml:"tags,omitempty"`
}

// FillStep is the body of a `fill` step.
type FillStep struct {
	Selector string `yaml:"selector"`
	Text     string `yaml:"text"`
}

// LoadSpec parses a single spec file from disk.
func LoadSpec(path string) (*Spec, error) {
	return loadSpecDepth(path, 0)
}

// loadSpecDepth is the internal recursive loader. Depth-limited so a
// malicious or accidentally-cyclic `include:` can't OOM the agent.
func loadSpecDepth(path string, depth int) (*Spec, error) {
	if depth > 8 {
		return nil, fmt.Errorf("include depth limit reached for %q — check for cycles", path)
	}
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
	s.expandEnv()
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

	// Expand spec-level include: directives by inlining each
	// referenced file's Setup + Steps at the front of this spec's
	// own setup. The included spec's target/url/etc are ignored —
	// includes are step macros, not full specs. Solo-dev flow:
	//
	//   yaver-tests/macros/login.test.yaml  (setup + steps only)
	//   yaver-tests/checkout.test.yaml      (include: ["macros/login.test.yaml"])
	baseDir := filepath.Dir(abs)
	if len(s.Include) > 0 {
		var preSetup []Step
		for _, inc := range s.Include {
			body, err := loadIncludeBody(inc, baseDir, depth)
			if err != nil {
				return nil, err
			}
			preSetup = append(preSetup, body...)
		}
		s.Setup = append(preSetup, s.Setup...)
	}

	// Expand step-level `- include: path` markers inside setup /
	// steps / teardown. This is the positional variant — the macro
	// fires at the exact point the dev drops the marker instead of
	// being hoisted to the top of setup. Useful for long specs
	// where the "log in as admin" macro should run mid-flow.
	expanded, err := expandStepIncludes(s.Setup, baseDir, depth)
	if err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}
	s.Setup = expanded
	expanded, err = expandStepIncludes(s.Steps, baseDir, depth)
	if err != nil {
		return nil, fmt.Errorf("steps: %w", err)
	}
	s.Steps = expanded
	expanded, err = expandStepIncludes(s.Teardown, baseDir, depth)
	if err != nil {
		return nil, fmt.Errorf("teardown: %w", err)
	}
	s.Teardown = expanded

	return &s, nil
}

func (s *Spec) expandEnv() {
	s.Name = os.ExpandEnv(s.Name)
	s.URL = os.ExpandEnv(s.URL)
	s.App = os.ExpandEnv(s.App)
	for i := range s.Include {
		s.Include[i] = os.ExpandEnv(s.Include[i])
	}
	for i := range s.Setup {
		expandEnvStep(&s.Setup[i])
	}
	for i := range s.Steps {
		expandEnvStep(&s.Steps[i])
	}
	for i := range s.Teardown {
		expandEnvStep(&s.Teardown[i])
	}
}

func expandEnvStep(step *Step) {
	step.Goto = os.ExpandEnv(step.Goto)
	step.Click = os.ExpandEnv(step.Click)
	step.WaitFor = os.ExpandEnv(step.WaitFor)
	step.WaitForURL = os.ExpandEnv(step.WaitForURL)
	step.AssertVisible = os.ExpandEnv(step.AssertVisible)
	step.AssertText = os.ExpandEnv(step.AssertText)
	step.AssertTitle = os.ExpandEnv(step.AssertTitle)
	step.AssertURL = os.ExpandEnv(step.AssertURL)
	step.Snapshot = os.ExpandEnv(step.Snapshot)
	step.Inspect = os.ExpandEnv(step.Inspect)
	step.SaveHAR = os.ExpandEnv(step.SaveHAR)
	step.Eval = os.ExpandEnv(step.Eval)
	step.Include = os.ExpandEnv(step.Include)
	if step.Fill != nil {
		step.Fill.Selector = os.ExpandEnv(step.Fill.Selector)
		step.Fill.Text = os.ExpandEnv(step.Fill.Text)
	}
	if step.A11y != nil {
		step.A11y.MinImpact = os.ExpandEnv(step.A11y.MinImpact)
		for i := range step.A11y.Tags {
			step.A11y.Tags[i] = os.ExpandEnv(step.A11y.Tags[i])
		}
	}
}

// loadIncludeBody resolves a single include path (relative to baseDir)
// and returns the concatenation of its Setup + Steps. Used by both
// spec-level and step-level include expansion.
func loadIncludeBody(inc, baseDir string, depth int) ([]Step, error) {
	incPath := inc
	if !filepath.IsAbs(incPath) {
		incPath = filepath.Join(baseDir, incPath)
	}
	incSpec, err := loadSpecDepth(incPath, depth+1)
	if err != nil {
		return nil, fmt.Errorf("include %q: %w", inc, err)
	}
	var body []Step
	body = append(body, incSpec.Setup...)
	body = append(body, incSpec.Steps...)
	return body, nil
}

// expandStepIncludes walks a step list and replaces any step whose
// Include field is set with the body of the referenced macro. Non-
// include steps are passed through unchanged. The macro is loaded
// through loadSpecDepth so the global cycle / depth guard still
// applies.
func expandStepIncludes(steps []Step, baseDir string, depth int) ([]Step, error) {
	if len(steps) == 0 {
		return steps, nil
	}
	out := make([]Step, 0, len(steps))
	for _, step := range steps {
		if step.Include == "" {
			out = append(out, step)
			continue
		}
		body, err := loadIncludeBody(step.Include, baseDir, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, body...)
	}
	return out, nil
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
	if s.Target != TargetWeb && s.Target != TargetIOSSim && s.Target != TargetAndroidEmu &&
		s.Target != TargetAndroidRedroid && s.Target != TargetDevice {
		return fmt.Errorf("unknown target %q (supported: web, ios-sim, android-emu, android-redroid, device)", s.Target)
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
