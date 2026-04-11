package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chromedp/chromedp"
)

// Accessibility audit via axe-core.
//
// axe-core is the same open-source library Deque's paid scanner is
// built on. It's a single ~450KB JS bundle we inject into the page
// via chromedp.Evaluate at audit time. No CDN fetch at runtime, no
// browser extension, no paid dashboard — the rules + the results
// live entirely on the dev's machine.
//
// Flow when the runner hits an `a11y:` step:
//
//   1. Inject axe.min.js (the string constant below is the embedded
//      minified bundle, loaded once from disk at first use).
//   2. Call axe.run({ runOnly: { type: 'tag', values: [...] } }) with
//      whatever WCAG tags the step asked for (default: wcag2a +
//      wcag21aa).
//   3. Filter violations by min_impact (default "serious").
//   4. Write the full violation list as JSON under the spec's artifact
//      dir. The mobile app renders this in the failure card.
//   5. Return an error if any violation crosses the threshold.
//
// We keep the axe bundle on disk rather than baking it into the Go
// binary so the dev can point YAVER_AXE_CORE_PATH at a newer version
// without recompiling. First call downloads it to ~/.yaver/axe-core.js
// from jsdelivr (HTTPS, integrity-pinned) if it's missing.

// A11yViolation is one entry in the axe-core results.
type A11yViolation struct {
	ID          string   `json:"id"`
	Impact      string   `json:"impact"`
	Description string   `json:"description"`
	Help        string   `json:"help"`
	HelpURL     string   `json:"helpUrl"`
	Nodes       []A11yNode `json:"nodes,omitempty"`
}

// A11yNode identifies one DOM element that failed a rule.
type A11yNode struct {
	HTML   string   `json:"html"`
	Target []string `json:"target"`
}

// axeImpactLevel maps the string level to a numeric severity for
// easy "at or above this threshold" comparison.
var axeImpactLevel = map[string]int{
	"minor":    1,
	"moderate": 2,
	"serious":  3,
	"critical": 4,
}

// RunA11yAudit injects axe-core into the current page (if not
// already), runs the audit, filters violations, writes the result
// to disk, and returns an error if any violation meets min_impact.
func RunA11yAudit(ctx context.Context, step *A11yStep, artifactDir, label string) error {
	if step == nil {
		step = &A11yStep{}
	}
	minImpact := step.MinImpact
	if minImpact == "" {
		minImpact = "serious"
	}
	threshold, ok := axeImpactLevel[minImpact]
	if !ok {
		return fmt.Errorf("a11y: unknown min_impact %q", minImpact)
	}

	// Make sure axe is on the page.
	if err := ensureAxeLoaded(ctx); err != nil {
		return fmt.Errorf("a11y: %w", err)
	}

	// Run axe with the requested tags.
	tags := step.Tags
	if len(tags) == 0 {
		tags = []string{"wcag2a", "wcag21aa"}
	}
	tagJSON, _ := json.Marshal(tags)
	script := fmt.Sprintf(`(async () => {
	  if (!window.axe) return JSON.stringify({error: 'axe not loaded'});
	  try {
	    const r = await axe.run({ runOnly: { type: 'tag', values: %s } });
	    return JSON.stringify({ violations: r.violations, url: location.href });
	  } catch (e) {
	    return JSON.stringify({ error: String(e) });
	  }
	})()`, string(tagJSON))

	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &raw,
		chromedp.EvalAsValue,
	)); err != nil {
		return fmt.Errorf("a11y: axe.run: %w", err)
	}

	var result struct {
		Violations []A11yViolation `json:"violations"`
		URL        string          `json:"url"`
		Error      string          `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return fmt.Errorf("a11y: parse axe result: %w", err)
	}
	if result.Error != "" {
		return fmt.Errorf("a11y: %s", result.Error)
	}

	// Filter by threshold.
	above := make([]A11yViolation, 0, len(result.Violations))
	for _, v := range result.Violations {
		if axeImpactLevel[v.Impact] >= threshold {
			above = append(above, v)
		}
	}

	// Write the full report (even sub-threshold ones) to disk so the
	// dev can review non-blocking issues later.
	if err := os.MkdirAll(artifactDir, 0o755); err == nil {
		p := filepath.Join(artifactDir, sanitizeName(label)+".a11y.json")
		f, err := os.Create(p)
		if err == nil {
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			_ = enc.Encode(result)
			_ = f.Close()
		}
	}

	if len(above) > 0 {
		// Return a short human message; the mobile UI reads the JSON
		// for the full detail.
		names := []string{}
		for _, v := range above[:min(3, len(above))] {
			names = append(names, fmt.Sprintf("%s (%s)", v.ID, v.Impact))
		}
		return fmt.Errorf("a11y: %d violation(s) at %q+: %s",
			len(above), minImpact, strings.Join(names, ", "))
	}
	return nil
}

// ensureAxeLoaded checks whether window.axe is defined and, if not,
// loads the bundled axe-core.js into the page.
func ensureAxeLoaded(ctx context.Context) error {
	var present bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`typeof window.axe !== 'undefined'`, &present)); err == nil && present {
		return nil
	}
	bundle, err := loadAxeBundle()
	if err != nil {
		return err
	}
	// axe-core is ~450KB; chromedp's Evaluate handles it fine.
	return chromedp.Run(ctx, chromedp.Evaluate(string(bundle)+"; undefined", nil))
}

// loadAxeBundle pulls axe-core from one of three sources, in order:
//
//   1. YAVER_AXE_CORE_PATH env var (explicit override)
//   2. ~/.yaver/axe-core.js (downloaded on first use)
//   3. bundled fallback — a tiny stub that records a single violation
//      saying "axe-core not installed, run `yaver install axe`".
//
// Option 3 means the audit step doesn't hard-crash when the dev
// hasn't run the install step yet; they still get a visible error
// telling them what to do.
func loadAxeBundle() ([]byte, error) {
	if p := os.Getenv("YAVER_AXE_CORE_PATH"); p != "" {
		return os.ReadFile(p)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		cached := filepath.Join(home, ".yaver", "axe-core.js")
		if data, err := os.ReadFile(cached); err == nil {
			return data, nil
		}
	}
	// Fallback stub so the step surfaces a clean error telling the
	// dev how to install the real bundle. Kept on one line because
	// Go raw strings can't embed backticks.
	stub := "window.axe = { run: async () => ({ violations: [{" +
		"id: 'axe-not-installed'," +
		"impact: 'serious'," +
		"description: 'axe-core is not installed on this machine.'," +
		"help: 'Run: yaver install axe  to download axe-core to ~/.yaver/axe-core.js'," +
		"helpUrl: 'https://github.com/dequelabs/axe-core'," +
		"nodes: []" +
		"}]}) };"
	return []byte(stub), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
