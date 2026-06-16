package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// driver_playwright.go — runs a web spec via Playwright (node sidecar) instead
// of chromedp, for teams that want Playwright-specific features (its selector
// engine, trace viewer, codegen). Same *.test.yaml spec, target: web-playwright.
//
// Implementation: translate the spec to a self-contained Node script that uses
// `playwright` (chromium), run it, and parse a JSON result line per step. The
// driver degrades gracefully: if node or the playwright package isn't present,
// it fails the spec with an actionable message rather than crashing the suite.
// chromedp remains the zero-dependency default (target: web).

func runPlaywrightSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result) {
	node, err := exec.LookPath("node")
	if err != nil {
		res.Err = fmt.Errorf("target web-playwright needs Node.js on PATH: %w", err)
		return
	}
	artifactDir := artifactDirFor(spec, opts)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		res.Err = fmt.Errorf("mkdir artifacts: %w", err)
		return
	}

	script, steps := buildPlaywrightScript(spec, artifactDir)
	scriptPath := filepath.Join(artifactDir, "playwright-run.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		res.Err = fmt.Errorf("write playwright script: %w", err)
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, node, scriptPath)
	out, runErr := cmd.CombinedOutput()

	// Each step prints a line: @@STEP {json}. Parse them into StepResults.
	byIdx := map[int]*pwStepLine{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@@STEP ") {
			continue
		}
		var sl pwStepLine
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "@@STEP ")), &sl) == nil {
			s := sl
			byIdx[sl.Index] = &s
		}
	}
	for i, label := range steps {
		sr := StepResult{Index: i + 1, Description: label, Phase: "step", StartedAt: time.Now()}
		if sl := byIdx[i+1]; sl != nil {
			if sl.Screenshot != "" {
				sr.ScreenshotPath = sl.Screenshot
			}
			if !sl.OK {
				sr.Err = fmt.Errorf("%s", strings.TrimSpace(sl.Error))
			}
		}
		res.Steps = append(res.Steps, sr)
	}
	if runErr != nil && len(byIdx) == 0 {
		// node failed before any step (most likely playwright not installed)
		res.Err = fmt.Errorf("playwright run failed (is the 'playwright' npm package installed? `npx playwright install chromium`): %v: %s",
			runErr, lastLines(string(out), 6))
	}
}

type pwStepLine struct {
	Index      int    `json:"i"`
	OK         bool   `json:"ok"`
	Error      string `json:"err"`
	Screenshot string `json:"shot"`
}

// buildPlaywrightScript renders the spec into a Node ESM script and returns it
// plus the human labels (parallel to the emitted @@STEP indices).
func buildPlaywrightScript(spec *Spec, artifactDir string) (string, []string) {
	var b strings.Builder
	var labels []string
	b.WriteString("import { chromium } from 'playwright';\n")
	b.WriteString("const base = " + jsStr(spec.URL) + ";\n")
	b.WriteString("const shotDir = " + jsStr(artifactDir) + ";\n")
	w := 1280
	h := 800
	if spec.Viewport != nil {
		w, h = spec.Viewport.Width, spec.Viewport.Height
	}
	b.WriteString(fmt.Sprintf("const browser = await chromium.launch({ headless: true });\n"))
	b.WriteString(fmt.Sprintf("const ctx = await browser.newContext({ viewport: { width: %d, height: %d } });\n", w, h))
	for _, c := range spec.Cookies {
		path := c.Path
		if path == "" {
			path = "/"
		}
		ck, _ := json.Marshal(map[string]any{
			"name": c.Name, "value": c.Value, "domain": c.Domain, "path": path,
			"secure": c.Secure, "httpOnly": c.HTTPOnly,
		})
		b.WriteString("await ctx.addCookies([" + string(ck) + "]);\n")
	}
	b.WriteString("const page = await ctx.newPage();\n")
	b.WriteString("function urlOf(p){ return /^https?:\\/\\//.test(p) ? p : (base.replace(/\\/$/,'') + p); }\n")
	b.WriteString("async function step(i, label, fn){ try { await fn(); let shot=shotDir+'/pw-step-'+i+'.png'; await page.screenshot({path:shot}).catch(()=>{}); console.log('@@STEP '+JSON.stringify({i, ok:true, shot})); } catch(e){ let shot=shotDir+'/pw-step-'+i+'-fail.png'; await page.screenshot({path:shot}).catch(()=>{}); console.log('@@STEP '+JSON.stringify({i, ok:false, err:String(e&&e.message||e), shot})); throw e; } }\n")

	idx := 0
	emit := func(label, body string) {
		idx++
		labels = append(labels, label)
		b.WriteString(fmt.Sprintf("await step(%d, %s, async () => { %s });\n", idx, jsStr(label), body))
	}
	for _, st := range spec.Steps {
		switch {
		case st.Goto != "":
			emit("goto "+st.Goto, "await page.goto(urlOf("+jsStr(st.Goto)+"), {waitUntil:'networkidle'});")
		case st.Click != "":
			emit("click "+st.Click, "await page.click("+jsStr(st.Click)+");")
		case st.Fill != nil:
			emit("fill "+st.Fill.Selector, "await page.fill("+jsStr(st.Fill.Selector)+", "+jsStr(st.Fill.Text)+");")
		case st.WaitFor != "":
			emit("wait_for "+st.WaitFor, "await page.waitForSelector("+jsStr(st.WaitFor)+");")
		case st.WaitForURL != "":
			emit("wait_for_url "+st.WaitForURL, "await page.waitForURL(u=>u.href.includes("+jsStr(st.WaitForURL)+"));")
		case st.SleepMS > 0:
			emit(fmt.Sprintf("sleep %dms", st.SleepMS), fmt.Sprintf("await page.waitForTimeout(%d);", st.SleepMS))
		case st.AssertVisible != "":
			emit("assert.visible "+st.AssertVisible, "await page.waitForSelector("+jsStr(st.AssertVisible)+", {state:'visible'});")
		case st.AssertText != "":
			emit("assert.text "+st.AssertText, "{ const body = await page.textContent('body'); if(!body || !body.includes("+jsStr(st.AssertText)+")) throw new Error('text not found: '+"+jsStr(st.AssertText)+"); }")
		case st.AssertTitle != "":
			emit("assert.title "+st.AssertTitle, "{ const t = await page.title(); if(!t.includes("+jsStr(st.AssertTitle)+")) throw new Error('title mismatch: '+t); }")
		case st.AssertURL != "":
			emit("assert.url "+st.AssertURL, "{ if(!page.url().includes("+jsStr(st.AssertURL)+")) throw new Error('url mismatch: '+page.url()); }")
		case st.Screenshot:
			emit("screenshot", "/* screenshot taken by step() */")
		case st.Eval != "":
			emit("eval", "await page.evaluate(()=>{ "+st.Eval+" });")
		}
	}
	b.WriteString("await browser.close();\n")
	return b.String(), labels
}

func jsStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
