package main

// autoinit_cmd.go — `yaver autoinit <project>` produces a project
// "init.md" that captures what every subsequent yaver session
// (autodev, autoideas, autotest, manual handoff) needs to know
// about the codebase up-front: what the project is, the stack,
// the directory map, conventions, dependencies, build / test /
// deploy commands, and a running log of what's been built lately.
//
// The point is performance + continuity:
//   - perf:       runners stop re-reading the whole project every
//                 kick; init.md goes into their prompt as cached context.
//   - continuity: the "what was developed so far" section is
//                 appended to after each autodev / autoideas run, so
//                 the next session sees what the previous one shipped.
//
// init.md lives at the project root so it's visible in `git status`
// and reviewable. Auto-managed sections are bracketed with HTML
// comment markers so a human can hand-edit the prose between them
// without losing their work to the next regen.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	autoinitFile         = "init.md"
	autoinitGenStart     = "<!-- yaver:autoinit:generated:start -->"
	autoinitGenEnd       = "<!-- yaver:autoinit:generated:end -->"
	autoinitHistoryStart = "<!-- yaver:autoinit:history:start -->"
	autoinitHistoryEnd   = "<!-- yaver:autoinit:history:end -->"
)

func runAutoInit(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			printAutoInitHelp()
			return
		case "status":
			runAutoInitStatus(args[1:])
			return
		}
	}

	fs := flag.NewFlagSet("autoinit", flag.ExitOnError)
	prompt := fs.String("prompt", "", "Optional extra context to bias the description (e.g. 'mobile-first tipsters app')")
	engine := fs.String("engine", "", "claude|hybrid (default: claude)")
	runner := fs.String("runner", "", "Override the generator runner (e.g. claude:opus, codex, opencode, ollama:qwen2.5-coder:14b)")
	hybrid := fs.Bool("hybrid", false, "Shortcut for --engine hybrid")
	output := fs.String("output", autoinitFile, "Output file inside the project (default init.md)")
	force := fs.Bool("force", false, "Regenerate the entire generated section even if init.md exists")
	showPlan := fs.Bool("plan", false, "Print plan and exit (dry-run)")
	to := fs.String("to", "", "Run on a remote yaver agent (device id / hostname). Routes via P2P or relay.")
	fs.Usage = printAutoInitHelp
	positional, flagArgs := splitAutodevArgs(args)
	_ = fs.Parse(flagArgs)
	if *hybrid {
		*engine = "hybrid"
	}

	wd, _ := os.Getwd()
	project := ""
	if len(positional) > 0 {
		project = positional[0]
	}
	if project == "" {
		project = filepath.Base(wd)
	}
	outPath := *output
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(wd, outPath)
	}

	if strings.TrimSpace(*to) != "" {
		body := map[string]interface{}{
			"project":  project,
			"work_dir": wd,
			"prompt":   *prompt,
			"engine":   *engine,
			"runner":   *runner,
			"output":   *output,
			"force":    *force,
		}
		out := remoteYaverPOST(*to, "/autoinit/start", body)
		fmt.Printf("autoinit: started on %s — loop=%v stream=%v output=%v\n",
			*to, out["loop_name"], out["stream_name"], out["output"])
		return
	}

	if *showPlan {
		fmt.Println("yaver autoinit plan")
		fmt.Println("---------------")
		fmt.Printf("  project: %s\n", project)
		fmt.Printf("  output:  %s\n", outPath)
		fmt.Printf("  engine:  %s\n", defaultStr(*engine, "claude"))
		fmt.Printf("  runner:  %s\n", defaultStr(*runner, "auto"))
		fmt.Printf("  force:   %v\n", *force)
		return
	}

	// Detach + tail. Same vibe as autodev/autoideas — the user can
	// Ctrl-C the parent and reattach the stream later.
	loopName := project + "-autoinit"
	streamName := "autodev:" + loopName
	if !autodevDetachActive() {
		_, sn := spawnDetachedAutodev("autoinit", args, loopName)
		if sn != "" {
			tailDetachedAutodev(sn)
			return
		}
		fmt.Fprintln(os.Stderr, "[autoinit] detach failed — running in foreground")
	}

	stopStream := teeStdoutToStream(streamName)
	defer stopStream()

	fmt.Printf("autoinit: generating %s for %s…\n", outPath, project)
	AutodevPublishYaverSay(fmt.Sprintf("Generate init.md for project %s", project))

	if err := autoinitGenerate(*engine, *runner, *prompt, project, outPath, wd, *force); err != nil {
		fmt.Fprintf(os.Stderr, "autoinit: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("autoinit: wrote %s\n", outPath)
}

// autoinitGenerate asks whichever AI runner the user has configured
// (claude / codex / aider / ollama, picked by RunAIGenerator) to
// read the project and emit a structured init.md body. We bracket
// the AI-written portion with markers so the next regen can replace
// it without touching the human-written prose between them.
func autoinitGenerate(engine, runner, extraPrompt, project, outPath, wd string, force bool) error {
	existing := ""
	if data, err := os.ReadFile(outPath); err == nil {
		existing = string(data)
	}
	if existing != "" && !force && strings.Contains(existing, autoinitGenStart) {
		fmt.Fprintf(os.Stderr, "[autoinit] %s exists with generated section — pass --force to regenerate fully\n", outPath)
	}

	prompt := fmt.Sprintf(`You are bootstrapping a project "init.md" for an autonomous coding agent (yaver).

Project root: %s
Project name: %s
%s
Read the project — package.json / go.mod / Cargo.toml / Podfile / app.json, top-level README, src/ + app/ directory shape, .github/workflows, scripts/, recent git log — and write a markdown document that captures the cached project context. Output ONLY the markdown body, no commentary, no code fences.

Required sections (use these exact H2 headings):

## What is this
1-2 sentences plain-English description of the product.

## Tech stack
Bullet list: language(s), framework(s), backend, database, deployment targets. One bullet per item, terse.

## Layout
Tree-style listing of the most important directories with one-line purpose each. Skip node_modules, build, dist, .git.

## Build / test / deploy
Commands that work in this repo. One line each, with a 2-3 word label. Pull from package.json scripts, Makefile, or scripts/ if present.

## Conventions
Bullet list of project conventions an autonomous agent must respect (commit message style, branch policy, file naming, language style guide, "never edit foo.gen.ts" type rules). Pull from CLAUDE.md / AGENTS.md / .cursorrules if present; otherwise infer from git log + lint configs.

## Recent direction
3-5 bullets summarising what's been built / changed in the last ~20 commits.

Output the markdown only.`,
		wd, project,
		func() string {
			if strings.TrimSpace(extraPrompt) != "" {
				return "Extra context: " + strings.TrimSpace(extraPrompt) + "\n"
			}
			return ""
		}())

	body, err := RunAIGenerator(AIGeneratorSpec{
		Engine:  engine,
		Runner:  runner,
		WorkDir: wd,
		Prompt:  prompt,
		Timeout: 10 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("autoinit generator: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("autoinit: empty body from AI runner")
	}

	// Compose final init.md: preserve human-edited prose, replace the
	// generated section, keep / start the history section. If the
	// existing file has no markers (first run), wrap everything fresh.
	final := autoinitMerge(existing, body)
	if err := os.WriteFile(outPath, []byte(final), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

// autoinitMerge keeps the human-written prose around the marker
// block while replacing the generated section.
func autoinitMerge(existing, generated string) string {
	header := "# Project init — for yaver autodev / autoideas / autotest sessions\n\n" +
		"Auto-generated context for autonomous yaver runs. The block\n" +
		"between `<!-- yaver:autoinit:generated:* -->` markers is\n" +
		"regenerated by `yaver autoinit`; everything outside it is\n" +
		"yours to hand-edit and stays put.\n\n"

	historyBlock := autoinitHistoryStart + "\n" +
		"## What's been built recently (auto-appended after each yaver run)\n\n" +
		"_(populated automatically — most recent first)_\n" +
		autoinitHistoryEnd + "\n"

	wrapped := autoinitGenStart + "\n" + strings.TrimSpace(generated) + "\n" + autoinitGenEnd

	if existing == "" {
		return header + wrapped + "\n\n" + historyBlock
	}
	// Replace existing generated section if present, else append.
	if strings.Contains(existing, autoinitGenStart) && strings.Contains(existing, autoinitGenEnd) {
		out := replaceBetween(existing, autoinitGenStart, autoinitGenEnd, wrapped)
		// Ensure history block also exists.
		if !strings.Contains(out, autoinitHistoryStart) {
			out += "\n\n" + historyBlock
		}
		return out
	}
	// First-time autoinit on a manually-curated file: append.
	out := strings.TrimRight(existing, "\n") + "\n\n" + wrapped
	if !strings.Contains(out, autoinitHistoryStart) {
		out += "\n\n" + historyBlock
	}
	return out
}

func replaceBetween(s, startTag, endTag, replacement string) string {
	startIdx := strings.Index(s, startTag)
	endIdx := strings.Index(s, endTag)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return s
	}
	endIdx += len(endTag)
	return s[:startIdx] + replacement + s[endIdx:]
}

// autoinitAppendHistory pushes a one-line entry to init.md's history
// block. Called by autodev / autoideas after each successful run so
// "what's been built recently" stays current.
func autoinitAppendHistory(workDir, line string) {
	path := filepath.Join(workDir, autoinitFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return // no init.md → nothing to append to
	}
	content := string(data)
	if !strings.Contains(content, autoinitHistoryStart) || !strings.Contains(content, autoinitHistoryEnd) {
		return
	}
	startIdx := strings.Index(content, autoinitHistoryStart) + len(autoinitHistoryStart)
	endIdx := strings.Index(content, autoinitHistoryEnd)
	if endIdx <= startIdx {
		return
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	entry := fmt.Sprintf("\n- %s — %s", stamp, strings.TrimSpace(line))
	// Insert right after the start marker so latest is on top.
	updated := content[:startIdx] + entry + content[startIdx:endIdx] + content[endIdx:]
	_ = os.WriteFile(path, []byte(updated), 0o644)
}

// autoinitContextBlock returns the cached project context that
// runners (autodev / autoideas / autotest) prepend to their prompt.
// Reads init.md, CLAUDE.md, and remained.md from workDir. Each is
// best-effort — missing files just contribute nothing.
func autoinitContextBlock(workDir string) string {
	var sb strings.Builder
	read := func(label, name string) {
		path := filepath.Join(workDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			return
		}
		// Cap each section at 8 KB so we don't blow the runner's
		// prompt window with a massive CLAUDE.md.
		if len(body) > 8*1024 {
			body = body[:8*1024] + "\n…(truncated)"
		}
		sb.WriteString("\n=== " + label + " (" + name + ") ===\n")
		sb.WriteString(body)
		sb.WriteString("\n")
	}
	read("Project init", autoinitFile)
	read("Project conventions", "CLAUDE.md")
	read("Remaining work", "remained.md")
	if sb.Len() == 0 {
		return ""
	}
	return "\n--- CACHED PROJECT CONTEXT (do not re-read these files unless you need to verify a detail) ---\n" + sb.String() + "--- END CACHED CONTEXT ---\n"
}

// autoinitStatus is what /autoinit/status returns and what the
// CLI's `yaver autoinit status` prints.
type AutoInitStatus struct {
	Done       bool   `json:"done"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	HasGenSec  bool   `json:"has_generated_section"`
	HasHistory bool   `json:"has_history_section"`
}

func computeAutoInitStatus(workDir string) AutoInitStatus {
	path := filepath.Join(workDir, autoinitFile)
	st, err := os.Stat(path)
	if err != nil {
		return AutoInitStatus{Done: false, Path: path}
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	return AutoInitStatus{
		Done:       true,
		Path:       path,
		Bytes:      st.Size(),
		UpdatedAt:  st.ModTime().UTC().Format(time.RFC3339),
		HasGenSec:  strings.Contains(body, autoinitGenStart),
		HasHistory: strings.Contains(body, autoinitHistoryStart),
	}
}

func runAutoInitStatus(_ []string) {
	wd, _ := os.Getwd()
	st := computeAutoInitStatus(wd)
	if !st.Done {
		fmt.Println("autoinit: not done — run `yaver autoinit` to generate init.md")
		return
	}
	fmt.Printf("autoinit: %s (%d bytes, updated %s)\n", st.Path, st.Bytes, st.UpdatedAt)
	if st.HasGenSec {
		fmt.Println("  - has generated section ✓")
	}
	if st.HasHistory {
		fmt.Println("  - has history section ✓")
	}
}

func printAutoInitHelp() {
	fmt.Println(`yaver autoinit — bootstrap a project init.md for autonomous yaver sessions

Usage:
  yaver autoinit <project> [flags]
  yaver autoinit status              — quick "is init done?" check
  yaver autoinit help                — this help

Why:
  autodev / autoideas / autotest re-read the project on every kick.
  init.md is a cached project description (stack, layout, conventions,
  recent direction) that runners read instead, drastically cutting
  the per-kick token + wall-clock cost. After each yaver run the
  history section gets a fresh "what was built" entry so the next
  session knows where the previous one left off.

Flags:
  --prompt "..."   extra context to bias the description
  --engine claude|hybrid
  --runner SPEC    generator runner override (claude:opus|codex|opencode|ollama:MODEL)
  --hybrid         shortcut for --engine hybrid
  --output PATH    default init.md inside the project
  --force          regenerate the AI section even if it already exists
  --plan           print plan and exit (dry run)

Markers in init.md (do not delete):
  <!-- yaver:autoinit:generated:start --> ... <!-- yaver:autoinit:generated:end -->
       AI-written project context, regenerated by --force.
  <!-- yaver:autoinit:history:start --> ... <!-- yaver:autoinit:history:end -->
       Auto-appended one-line entries after each yaver run.
  Everything outside the markers is yours — hand-editable, preserved.`)
}

// io.Discard alias to satisfy a tiny check elsewhere; keeps imports tidy.
var _ = io.Discard
