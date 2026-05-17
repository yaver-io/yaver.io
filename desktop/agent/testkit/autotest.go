package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AutoTestRequest is the phase-1 autonomous web test entry point. It
// deliberately stays local-only: plans/results/artifacts are written
// under .yaver/ and never leave the machine unless a caller reads them
// over the authenticated P2P/relay path.
type AutoTestRequest struct {
	RunID         string `json:"-"`
	WorkDir       string `json:"workDir,omitempty"`
	Scope         string `json:"scope,omitempty"`
	Viewport      string `json:"viewport,omitempty"`
	Driver        string `json:"driver,omitempty"`
	Stream        bool   `json:"stream,omitempty"`
	Propose       bool   `json:"propose,omitempty"`
	MaxFlows      int    `json:"maxFlows,omitempty"`
	MaxWallClockS int    `json:"maxWallClockSec,omitempty"`
	ACPowerOnly   bool   `json:"acPowerOnly,omitempty"`
}

type AutoTestEvent struct {
	RunID         string    `json:"runId"`
	Phase         string    `json:"phase"`
	Flow          string    `json:"flow,omitempty"`
	Progress      int       `json:"progress,omitempty"`
	Total         int       `json:"total,omitempty"`
	BugsFound     int       `json:"bugsFound"`
	Proposed      int       `json:"proposed"`
	NativeSkipped int       `json:"nativeSkipped"`
	Message       string    `json:"message,omitempty"`
	At            time.Time `json:"at"`
}

type AutoTestFlowResult struct {
	Name          string       `json:"name"`
	SpecPath      string       `json:"specPath,omitempty"`
	URL           string       `json:"url,omitempty"`
	Passed        bool         `json:"passed"`
	DurationMS    int64        `json:"durationMs"`
	Error         string       `json:"error,omitempty"`
	Screenshot    string       `json:"screenshot,omitempty"`
	Console       []ConsoleMsg `json:"console,omitempty"`
	Network       []NetEvent   `json:"network,omitempty"`
	Interactables int          `json:"interactables"`
	NativeSkipped bool         `json:"nativeSkipped,omitempty"`
}

type AutoTestResult struct {
	RunID         string               `json:"runId"`
	WorkDir       string               `json:"workDir"`
	Scope         string               `json:"scope"`
	Viewport      string               `json:"viewport"`
	Driver        string               `json:"driver"`
	StartedAt     time.Time            `json:"startedAt"`
	FinishedAt    time.Time            `json:"finishedAt"`
	Passed        bool                 `json:"passed"`
	BugsFound     int                  `json:"bugsFound"`
	Proposed      int                  `json:"proposed"`
	NativeSkipped int                  `json:"nativeSkipped"`
	ResultsDir    string               `json:"resultsDir"`
	Flows         []AutoTestFlowResult `json:"flows"`
}

type AutoTestViewport struct {
	ID     string  `json:"id"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	DPR    float64 `json:"dpr"`
}

var AutoTestViewports = []AutoTestViewport{
	{ID: "iphone15", Width: 393, Height: 852, DPR: 3},
	{ID: "pixel7", Width: 412, Height: 915, DPR: 2.6},
	{ID: "ipad11", Width: 834, Height: 1194, DPR: 2},
	{ID: "ipad11-landscape", Width: 1194, Height: 834, DPR: 2},
}

type autotestConfig struct {
	Driver   string `json:"driver"`
	Viewport string `json:"viewport"`
	MaxFlows int    `json:"maxFlows"`
}

// RunAutoTest executes the phase-1 DISCOVER -> DRIVE -> OBSERVE ->
// CODIFY -> REPORT path against web specs. Native specs are counted
// and reported as skipped for the later simulator/device pass.
func RunAutoTest(ctx context.Context, req AutoTestRequest, emit func(AutoTestEvent)) (*AutoTestResult, error) {
	if req.MaxWallClockS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.MaxWallClockS)*time.Second)
		defer cancel()
	}
	workDir := req.WorkDir
	if workDir == "" {
		workDir = "."
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	cfg := readAutoTestConfig(abs)
	if req.Driver == "" {
		req.Driver = cfg.Driver
	}
	if req.Driver == "" {
		req.Driver = "cdp"
	}
	if req.Viewport == "" {
		req.Viewport = cfg.Viewport
	}
	if req.Viewport == "" {
		req.Viewport = "iphone15"
	}
	if req.Scope == "" {
		req.Scope = "full"
	}
	if req.MaxFlows <= 0 {
		req.MaxFlows = cfg.MaxFlows
	}
	vp := ResolveAutoTestViewport(req.Viewport)
	runID := req.RunID
	if runID == "" {
		runID = FormatTimestamp(time.Now())
	}
	resultsDir := filepath.Join(abs, ".yaver", "results", "runs", runID)
	if err := os.MkdirAll(filepath.Join(resultsDir, "screenshots"), 0o755); err != nil {
		return nil, err
	}
	res := &AutoTestResult{
		RunID:      runID,
		WorkDir:    abs,
		Scope:      req.Scope,
		Viewport:   vp.ID,
		Driver:     req.Driver,
		StartedAt:  time.Now().UTC(),
		ResultsDir: resultsDir,
		Passed:     true,
	}
	defer func() {
		res.FinishedAt = time.Now().UTC()
		_ = writeAutoTestResult(res)
	}()

	emitAutoTest(emit, res, "DISCOVER", "", 0, 0, "discovering yaver-tests specs")
	specs, err := DiscoverSpecs(filepath.Join(abs, "yaver-tests"))
	if err != nil {
		res.Passed = false
		return res, err
	}
	specs = filterAutoTestSpecs(specs, req.Scope)
	if req.MaxFlows > 0 && len(specs) > req.MaxFlows {
		specs = specs[:req.MaxFlows]
	}
	if len(specs) == 0 {
		res.Passed = false
		return res, fmt.Errorf("no autotest specs found for scope %q", req.Scope)
	}
	_ = writeAutoTestPlan(abs, specs, req)

	emitAutoTest(emit, res, "SERVE", "", 0, len(specs), "using project dev server URLs from specs")
	for i, sp := range specs {
		if ctx.Err() != nil {
			res.Passed = false
			return res, ctx.Err()
		}
		if sp.Target != TargetWeb {
			res.NativeSkipped++
			res.Flows = append(res.Flows, AutoTestFlowResult{
				Name:          sp.Name,
				SpecPath:      sp.Path,
				NativeSkipped: true,
				Passed:        true,
			})
			emitAutoTest(emit, res, "OBSERVE", sp.Name, i+1, len(specs), "native flow queued for deep pass")
			continue
		}
		flow := runAutoTestWebFlow(ctx, sp, req.Driver, vp, resultsDir)
		res.Flows = append(res.Flows, flow)
		if !flow.Passed {
			res.Passed = false
			res.BugsFound++
			if req.Propose {
				res.Proposed++
			}
		}
		emitAutoTest(emit, res, "OBSERVE", sp.Name, i+1, len(specs), flowMessage(flow))
	}
	if req.Propose && res.BugsFound > 0 {
		emitAutoTest(emit, res, "PROPOSE", "", len(specs), len(specs), "phase-1 proposal recorded; approval remains user-gated")
	}
	emitAutoTest(emit, res, "CODIFY", "", len(specs), len(specs), "wrote .yaver/tests plan and local result")
	emitAutoTest(emit, res, "REPORT", "", len(specs), len(specs), "autotest complete")
	return res, nil
}

func runAutoTestWebFlow(ctx context.Context, sp *Spec, driver string, vp AutoTestViewport, resultsDir string) AutoTestFlowResult {
	start := time.Now()
	out := AutoTestFlowResult{Name: sp.Name, SpecPath: sp.Path, URL: firstSpecURL(sp), Passed: true}
	if out.URL == "" {
		out.Passed = false
		out.Error = "web spec has no url/goto"
		out.DurationMS = time.Since(start).Milliseconds()
		return out
	}
	d, err := NewWebDriver(driver, ChromeOpts{
		URL:       out.URL,
		ViewportW: vp.Width,
		ViewportH: vp.Height,
		DPR:       vp.DPR,
	})
	if err != nil {
		out.Passed = false
		out.Error = err.Error()
		out.DurationMS = time.Since(start).Milliseconds()
		return out
	}
	defer d.Close()
	if err := d.Launch(ctx); err != nil {
		out.Passed = false
		out.Error = err.Error()
		out.DurationMS = time.Since(start).Milliseconds()
		return out
	}
	if err := d.Navigate(ctx, out.URL); err != nil {
		out.Passed = false
		out.Error = err.Error()
	} else if snap, err := d.Snapshot(ctx); err != nil {
		out.Passed = false
		out.Error = err.Error()
	} else {
		out.Interactables = len(snap.Interactables)
	}
	if png, err := d.Screenshot(ctx); err == nil && len(png) > 0 {
		name := safeResultName(sp.Name) + ".png"
		p := filepath.Join(resultsDir, "screenshots", name)
		if werr := os.WriteFile(p, png, 0o644); werr == nil {
			out.Screenshot = p
		}
	}
	out.Console = d.Console()
	out.Network = d.Network()
	out.DurationMS = time.Since(start).Milliseconds()
	return out
}

func firstSpecURL(sp *Spec) string {
	base := strings.TrimSpace(sp.URL)
	for _, st := range append(sp.Setup, sp.Steps...) {
		if strings.TrimSpace(st.Goto) == "" {
			continue
		}
		g := strings.TrimSpace(st.Goto)
		if strings.HasPrefix(g, "http://") || strings.HasPrefix(g, "https://") {
			return g
		}
		if base == "" {
			return g
		}
		u, err := url.Parse(base)
		if err != nil {
			return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(g, "/")
		}
		rel, _ := url.Parse(g)
		return u.ResolveReference(rel).String()
	}
	return base
}

func ResolveAutoTestViewport(id string) AutoTestViewport {
	for _, vp := range AutoTestViewports {
		if vp.ID == id {
			return vp
		}
	}
	return AutoTestViewports[0]
}

func readAutoTestConfig(workDir string) autotestConfig {
	var cfg autotestConfig
	data, err := os.ReadFile(filepath.Join(workDir, ".yaver", "autotest.json"))
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	return cfg
}

func writeAutoTestPlan(workDir string, specs []*Spec, req AutoTestRequest) error {
	dir := filepath.Join(workDir, ".yaver", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type planSpec struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Target Target `json:"target"`
		URL    string `json:"url,omitempty"`
	}
	plan := struct {
		GeneratedAt time.Time  `json:"generatedAt"`
		Scope       string     `json:"scope"`
		Viewport    string     `json:"viewport"`
		Driver      string     `json:"driver"`
		Specs       []planSpec `json:"specs"`
	}{
		GeneratedAt: time.Now().UTC(),
		Scope:       req.Scope,
		Viewport:    req.Viewport,
		Driver:      req.Driver,
	}
	for _, sp := range specs {
		plan.Specs = append(plan.Specs, planSpec{Name: sp.Name, Path: sp.Path, Target: sp.Target, URL: sp.URL})
	}
	data, _ := json.MarshalIndent(plan, "", "  ")
	return os.WriteFile(filepath.Join(dir, "plan.json"), append(data, '\n'), 0o644)
}

func writeAutoTestResult(res *AutoTestResult) error {
	if res == nil || res.ResultsDir == "" {
		return nil
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(res.ResultsDir, "results.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# Auto Test %s\n\n", res.RunID)
	fmt.Fprintf(&md, "- Passed: %v\n- Bugs found: %d\n- Native skipped: %d\n\n", res.Passed, res.BugsFound, res.NativeSkipped)
	for _, f := range res.Flows {
		status := "PASS"
		if !f.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&md, "## %s %s\n\n", status, f.Name)
		if f.Error != "" {
			fmt.Fprintf(&md, "%s\n\n", f.Error)
		}
	}
	return os.WriteFile(filepath.Join(res.ResultsDir, "results.md"), []byte(md.String()), 0o644)
}

func filterAutoTestSpecs(specs []*Spec, scope string) []*Spec {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == "full" || scope == "changed" {
		return specs
	}
	if strings.HasPrefix(scope, "screen:") {
		needle := strings.ToLower(strings.TrimPrefix(scope, "screen:"))
		var out []*Spec
		for _, sp := range specs {
			if strings.Contains(strings.ToLower(sp.Name), needle) || strings.Contains(strings.ToLower(sp.Path), needle) {
				out = append(out, sp)
			}
		}
		return out
	}
	return specs
}

func emitAutoTest(emit func(AutoTestEvent), res *AutoTestResult, phase, flow string, progress, total int, msg string) {
	if emit == nil || res == nil {
		return
	}
	emit(AutoTestEvent{
		RunID:         res.RunID,
		Phase:         phase,
		Flow:          flow,
		Progress:      progress,
		Total:         total,
		BugsFound:     res.BugsFound,
		Proposed:      res.Proposed,
		NativeSkipped: res.NativeSkipped,
		Message:       msg,
		At:            time.Now().UTC(),
	})
}

func flowMessage(flow AutoTestFlowResult) string {
	if flow.NativeSkipped {
		return "native flow queued for deep pass"
	}
	if flow.Passed {
		return "flow passed"
	}
	return flow.Error
}

func safeResultName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "flow"
	}
	return out
}
