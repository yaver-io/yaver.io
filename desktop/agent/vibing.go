package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const vibingRuntimeDeployPrefix = "__YAVER_RUNTIME_DEPLOY__:"

type vibingRuntimeDeployAction struct {
	Slug    string   `json:"slug"`
	Targets []string `json:"targets,omitempty"`
	DryRun  bool     `json:"dryRun,omitempty"`
	Run     bool     `json:"run,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Project string   `json:"project,omitempty"`
	Path    string   `json:"path,omitempty"`
}

// VibingSuggestion is a structured suggestion from the AI for the Vibing widget.
type VibingSuggestion struct {
	ID        string `json:"id"`
	Icon      string `json:"icon"`
	Label     string `json:"label"`
	Desc      string `json:"desc"`
	Category  string `json:"category"`  // "feature", "bugfix", "test", "deploy", "refactor", "docs"
	Prompt    string `json:"prompt"`    // full prompt to send to the agent
	Priority  int    `json:"priority"`  // 1=high, 2=medium, 3=low
	Reasoning string `json:"reasoning"` // why this idea, use cases, why it's exciting — shown on card tap
}

// VibingState holds the state of a vibing session for a project.
type VibingState struct {
	Project      string             `json:"project"`
	Path         string             `json:"path"`
	Framework    string             `json:"framework,omitempty"`
	Suggestions  []VibingSuggestion `json:"suggestions"`
	QuickActions []VibingSuggestion `json:"quickActions"` // always-available actions
	History      []string           `json:"history"`      // recent task titles
	GeneratedAt  string             `json:"generatedAt"`
}

// generateQuickActions returns always-available actions for any project.
func generateQuickActions(projectPath, projectName, framework string) []VibingSuggestion {
	actions := []VibingSuggestion{
		{ID: "tests", Icon: "\U0001F9EA", Label: "Run Tests", Desc: "Run test suite", Category: "test",
			Prompt: fmt.Sprintf("Run all tests for %s. Report which pass and which fail. Fix any failures.", projectName), Priority: 1},
		{ID: "bugfix", Icon: "\U0001F41B", Label: "Bug Analysis", Desc: "Scan for bugs", Category: "bugfix",
			Prompt: fmt.Sprintf("Analyze %s for common bugs: null checks, error handling, race conditions, security issues. List what you find and fix the critical ones.", projectName), Priority: 2},
		{ID: "refactor", Icon: "\u2728", Label: "Clean Up", Desc: "Refactor code", Category: "refactor",
			Prompt: fmt.Sprintf("Do a code quality pass on %s: remove dead code, simplify complex functions, improve naming. Don't change behavior.", projectName), Priority: 3},
		{ID: "docs", Icon: "\U0001F4DD", Label: "Write Docs", Desc: "Update README", Category: "docs",
			Prompt: fmt.Sprintf("Update the README.md for %s. Add setup instructions, usage examples, and architecture overview based on the actual code.", projectName), Priority: 3},
		{ID: "add-tests", Icon: "\u2705", Label: "Add Tests", Desc: "Create unit tests", Category: "test",
			Prompt: fmt.Sprintf("Add unit tests for %s. Create a test file for each major module. Use the project's testing framework.", projectName), Priority: 3},
		{ID: "deps", Icon: "\U0001F4E6", Label: "Update Deps", Desc: "Check outdated packages", Category: "refactor",
			Prompt: fmt.Sprintf("Check %s for outdated dependencies. Update safe ones (patch/minor). Don't update major versions without checking.", projectName), Priority: 3},
	}

	if slug := vibingPhoneProjectSlug(projectPath); slug != "" {
		deployActions := []VibingSuggestion{
			{
				ID: "phone-convex-cloud", Icon: "\U0001F300", Label: "Sandbox → Convex", Desc: "Promote this phone sandbox to Convex Cloud", Category: "deploy",
				Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
					Slug:    slug,
					Targets: []string{"convex-cloud"},
					Run:     true,
					Project: projectName,
					Path:    projectPath,
				}),
				Priority: 1,
			},
			{
				ID: "phone-workers-cloud", Icon: "\U0001F7E7", Label: "Sandbox → Workers", Desc: "Promote this phone sandbox to Cloudflare Workers", Category: "deploy",
				Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
					Slug:    slug,
					Targets: []string{"cloudflare-workers"},
					Run:     true,
					Project: projectName,
					Path:    projectPath,
				}),
				Priority: 1,
			},
			{
				ID: "phone-cloud-dual", Icon: "\u2601\ufe0f", Label: "Machine + Cloud", Desc: "Push this sandbox to Yaver Cloud and your machine-ready runtime path", Category: "deploy",
				Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
					Slug:    slug,
					Targets: []string{"yaver-cloud"},
					Run:     true,
					Project: projectName,
					Path:    projectPath,
					Prompt:  "Push this phone sandbox to Yaver Cloud now. If I also have a selected dev machine flow available, use that too.",
				}),
				Priority: 1,
			},
		}
		if selfHostedRuntimeBaseURL() != "" {
			deployActions = append([]VibingSuggestion{
				{
					ID: "phone-self-hosted", Icon: "\U0001F5A5", Label: "Sandbox → Self-Hosted", Desc: "Push this phone sandbox to your configured self-hosted Yaver runtime", Category: "deploy",
					Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
						Slug:    slug,
						Targets: []string{"self-hosted"},
						Run:     true,
						Project: projectName,
						Path:    projectPath,
					}),
					Priority: 1,
				},
				{
					ID: "phone-self-hosted-cloud", Icon: "\u2601\ufe0f", Label: "Self-Hosted + Cloud", Desc: "Deploy this sandbox to self-hosted and managed cloud together", Category: "deploy",
					Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
						Slug:    slug,
						Targets: []string{"self-hosted", "yaver-cloud"},
						Run:     true,
						Project: projectName,
						Path:    projectPath,
					}),
					Priority: 1,
				},
			}, deployActions...)
		}
		actions = append(deployActions, actions...)
	}

	// Add deploy actions based on framework
	if framework == "expo" || framework == "flutter" || framework == "react-native" {
		actions = append([]VibingSuggestion{
			{ID: "mobile-flush", Icon: "\U0001F4F1", Label: "Push To Phone", Desc: "Use the right Yaver mobile path", Category: "deploy",
				Prompt: fmt.Sprintf("Push %s to the selected phone using Yaver's framework-aware mobile flow. If it is React Native or Expo, use the Hermes first-class path. If it is Flutter, use the second-class LAN-only Flutter flush path. If it is not LAN-eligible, explain why and do not fake a remote push.", projectName), Priority: 1},
			{ID: "testflight", Icon: "\U0001F34E", Label: "Ship to TestFlight", Desc: "Build + upload to TestFlight", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s for iOS, archive, and upload to TestFlight. Auto-increment build number. Report progress.", projectName), Priority: 1},
			{ID: "playstore", Icon: "\U0001F916", Label: "Ship to Play Store", Desc: "Build AAB + upload to internal testing", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s for Android (release AAB) and upload to Google Play internal testing. Auto-increment versionCode.", projectName), Priority: 1},
		}, actions...)
	}
	if framework == "swift" || framework == "kotlin" {
		actions = append([]VibingSuggestion{
			{ID: "mobile-flush", Icon: "\U0001F4F1", Label: "Push To Phone", Desc: "Use the second-class native mobile flow", Category: "deploy",
				Prompt: fmt.Sprintf("Push %s to the selected phone using Yaver's framework-aware native mobile flow. If it is Swift or Kotlin, use the second-class LAN-only build/install path for the selected target phone. If it is not LAN-eligible, explain why and stop.", projectName), Priority: 1},
		}, actions...)
	}
	if framework == "nextjs" || framework == "vite" {
		actions = append([]VibingSuggestion{
			{ID: "cloudflare", Icon: "\U0001F680", Label: "Deploy to Cloudflare", Desc: "Build and deploy to Cloudflare Workers", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s and deploy to Cloudflare Workers. Report the production URL.", projectName), Priority: 1},
			{ID: "vercel", Icon: "\U0001F680", Label: "Deploy to Vercel", Desc: "Build and deploy to production", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s and deploy to Vercel. Report the deploy URL.", projectName), Priority: 1},
		}, actions...)
	}

	return actions
}

func vibingExecutionContext(projectPath, framework string, target DevServerTarget, direct bool) string {
	var lines []string
	lines = append(lines, "Yaver mobile execution context:")
	if framework != "" {
		lines = append(lines, fmt.Sprintf("- Project framework: %s", framework))
	}
	if target.DeviceID != "" || target.DeviceName != "" || target.DeviceClass != "" {
		lines = append(lines, fmt.Sprintf("- Selected target phone: %s (%s, class=%s)",
			firstNonEmpty(target.DeviceName, "unknown"),
			firstNonEmpty(target.DeviceID, "unknown"),
			firstNonEmpty(target.DeviceClass, "unknown"),
		))
	} else {
		lines = append(lines, "- Selected target phone: current/default phone")
	}
	if direct {
		lines = append(lines, "- Current control connection to this machine: direct/LAN-capable")
	} else {
		lines = append(lines, "- Current control connection to this machine: relay/remote")
	}
	lines = append(lines, "- Hermes is the only first-class mobile runtime path and works over LAN, relay, and 4G.")
	lines = append(lines, "- Flutter, Swift, and Kotlin are second-class mobile paths and are LAN-only.")
	lines = append(lines, "- Do not suggest manual CLI flags like `--platform ios` to the user.")
	lines = append(lines, "- Let Yaver route by framework and selected target phone.")
	lines = append(lines, "- If framework is expo/react-native, prefer Hermes/Open in Yaver.")
	lines = append(lines, "- If framework is flutter/swift/kotlin, only use flush/build-install flows when LAN-eligible; otherwise explain the limitation.")
	lines = append(lines, fmt.Sprintf("- Project path: %s", projectPath))
	return strings.Join(lines, "\n")
}

// getRecentGitActivity returns recent commit messages and active files for smart suggestions.
func getRecentGitActivity(projectPath string) (commits []string, activeFiles []string) {
	// Recent commit messages
	if out, err := exec.Command("git", "-C", projectPath, "log", "--oneline", "-15", "--no-merges").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				// Strip hash prefix
				parts := strings.SplitN(line, " ", 2)
				if len(parts) == 2 {
					commits = append(commits, parts[1])
				}
			}
		}
	}
	// Most recently changed files
	if out, err := exec.Command("git", "-C", projectPath, "diff", "--name-only", "HEAD~5", "HEAD").Output(); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f != "" && !strings.Contains(f, "node_modules") && !strings.Contains(f, ".lock") {
				activeFiles = append(activeFiles, f)
			}
		}
	}
	return
}

// generateAISuggestions creates project-specific suggestions by reading the codebase.
// Reads README, package.json, git log, TODOs to propose smart ideas.
func generateAISuggestions(projectPath, projectName string) []VibingSuggestion {
	var suggestions []VibingSuggestion
	phoneSlug := vibingPhoneProjectSlug(projectPath)

	// Read README for context
	readmeContent := ""
	for _, name := range []string{"README.md", "readme.md", "README.txt"} {
		data, err := os.ReadFile(filepath.Join(projectPath, name))
		if err == nil {
			readmeContent = string(data)
			break
		}
	}

	// Git activity — what's been worked on recently
	commits, activeFiles := getRecentGitActivity(projectPath)

	// Smart "What's Next" based on git activity
	if len(commits) > 0 {
		// Build a context string from recent commits
		recentWork := strings.Join(commits, "; ")
		if len(recentWork) > 300 {
			recentWork = recentWork[:300]
		}
		suggestions = append(suggestions, VibingSuggestion{
			ID: "whats-next", Icon: "\U0001F52E", Label: "What's Next?",
			Desc:     fmt.Sprintf("Based on recent work: %s", commits[0]),
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("I've been working on %s. Recent commits: %s\n\nBased on this momentum, what should I build next? Suggest 3 concrete features or improvements that naturally follow from what I've been doing. For each one, give a one-liner and ask which one to start.", projectName, recentWork),
		})
	}

	// Active area suggestion
	if len(activeFiles) > 3 {
		area := filepath.Dir(activeFiles[0])
		suggestions = append(suggestions, VibingSuggestion{
			ID: "active-area", Icon: "\U0001F525", Label: fmt.Sprintf("Hot area: %s", area),
			Desc:     fmt.Sprintf("%d files changed recently", len(activeFiles)),
			Category: "feature", Priority: 2,
			Prompt: fmt.Sprintf("The most active area in %s is around %s (%d files changed recently). Look at this area, understand what's being built, and suggest improvements or the next logical step.", projectName, area, len(activeFiles)),
		})
	}

	// Feature ideas based on git momentum + README context
	if len(commits) > 2 {
		// Recent work pattern → next logical feature
		recentWork := strings.Join(commits[:min(5, len(commits))], "; ")
		suggestions = append(suggestions, VibingSuggestion{
			ID: "next-feature", Icon: "\U0001F4A1", Label: "Continue momentum",
			Desc:     fmt.Sprintf("You've been working on: %s", commits[0]),
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("I've been working on %s. Recent commits: %s\n\nWhat's the most impactful thing to build next? Give me ONE concrete feature that follows from this work. Describe it in 2 sentences and start implementing it.", projectName, recentWork),
		})
	}

	// README-based feature idea
	if readmeContent != "" && len(readmeContent) > 100 {
		truncatedReadme := readmeContent
		if len(truncatedReadme) > 1500 {
			truncatedReadme = truncatedReadme[:1500]
		}
		suggestions = append(suggestions, VibingSuggestion{
			ID: "readme-feature", Icon: "\U0001F52E", Label: "What's missing?",
			Desc:     "AI analyzes your project and finds gaps",
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("Read the codebase and README of %s:\n\n%s\n\nWhat's the most obvious missing feature that users would expect? Describe it and implement it.", projectName, truncatedReadme),
		})
	}

	// Active area — where the action is
	if len(activeFiles) > 2 {
		area := filepath.Dir(activeFiles[0])
		suggestions = append(suggestions, VibingSuggestion{
			ID: "hot-area", Icon: "\U0001F525", Label: fmt.Sprintf("Improve %s", area),
			Desc:     fmt.Sprintf("%d files changed recently in this area", len(activeFiles)),
			Category: "feature", Priority: 2,
			Prompt: fmt.Sprintf("The hottest area in %s is %s (%d files changed recently: %s). Look at what's being built there and make it better — add error handling, improve the UX, or add a missing feature.", projectName, area, len(activeFiles), strings.Join(activeFiles[:min(5, len(activeFiles))], ", ")),
		})
	}

	if phoneSlug != "" {
		suggestions = append(suggestions,
			VibingSuggestion{
				ID:   "phone-runtime-convex-cloud",
				Icon: "\U0001F300", Label: "Ship Sandbox Backend",
				Desc:     "Promote the phone sandbox to Convex Cloud now",
				Category: "deploy", Priority: 1,
				Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
					Slug:    phoneSlug,
					Targets: []string{"convex-cloud"},
					Run:     true,
					Project: projectName,
					Path:    projectPath,
				}),
				Reasoning: "This project is still a phone-first sandbox, so the highest-signal deploy action is moving the backend into a real managed runtime. Convex is the shortest path to keep the schema portable while making the app feel production-capable.",
			},
			VibingSuggestion{
				ID:   "phone-runtime-cloud",
				Icon: "\u2601\ufe0f", Label: "Deploy To Cloud",
				Desc:     "Push the same sandbox to Yaver Cloud immediately",
				Category: "deploy", Priority: 1,
				Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
					Slug:    phoneSlug,
					Targets: []string{"yaver-cloud"},
					Run:     true,
					Project: projectName,
					Path:    projectPath,
				}),
				Reasoning: "Yaver Cloud is the fastest always-on path for a solo developer who still wants the sandbox workflow. It preserves the current app loop while making the backend reachable outside the phone.",
			},
		)
		if selfHostedRuntimeBaseURL() != "" {
			suggestions = append(suggestions,
				VibingSuggestion{
					ID:   "phone-runtime-self-hosted",
					Icon: "\U0001F5A5", Label: "Deploy Self-Hosted",
					Desc:     "Push this sandbox to the configured self-hosted Yaver runtime",
					Category: "deploy", Priority: 1,
					Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
						Slug:    phoneSlug,
						Targets: []string{"self-hosted"},
						Run:     true,
						Project: projectName,
						Path:    projectPath,
					}),
					Reasoning: "A self-hosted Yaver runtime is the lowest-opex path when you already own the hardware or VPS. It keeps the sandbox portable while moving the backend onto infrastructure you control.",
				},
				VibingSuggestion{
					ID:   "phone-runtime-self-hosted-cloud",
					Icon: "\u2601\ufe0f", Label: "Deploy Both",
					Desc:     "Ship the sandbox to self-hosted and managed cloud together",
					Category: "deploy", Priority: 1,
					Prompt: vibingRuntimeDeployPrompt(vibingRuntimeDeployAction{
						Slug:    phoneSlug,
						Targets: []string{"self-hosted", "yaver-cloud"},
						Run:     true,
						Project: projectName,
						Path:    projectPath,
					}),
					Reasoning: "Dual deploy gives the solo developer an owned runtime plus a peace-of-mind managed fallback. That is the exact product posture Yaver is aiming for: self-host most of the time, keep cloud for confidence and reachability.",
				},
			)
		}
	}

	return suggestions
}

// countPatternInDir counts occurrences of a pattern in source files.
func countPatternInDir(dir, pattern string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip non-source files and large files
		ext := filepath.Ext(path)
		sourceExts := map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".go": true, ".py": true, ".rs": true, ".dart": true, ".swift": true, ".kt": true}
		if !sourceExts[ext] {
			return nil
		}
		if info.Size() > 100000 { // skip files > 100KB
			return nil
		}
		// Skip node_modules, .git, etc
		if strings.Contains(path, "node_modules") || strings.Contains(path, ".git/") || strings.Contains(path, "vendor/") {
			return filepath.SkipDir
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		count += strings.Count(string(data), pattern)
		return nil
	})
	return count
}

// vibingCache stores pre-generated vibing states keyed by project path.
var vibingCache = struct {
	mu    sync.RWMutex
	cache map[string]*VibingState
}{cache: make(map[string]*VibingState)}

// PrewarmVibingCache generates vibing suggestions for recently modified projects in background.
// It runs up to 5 iterative passes with the AI agent per project, each building on the previous
// output to produce increasingly deep and useful suggestions.
func PrewarmVibingCache(taskMgr *TaskManager) {
	projects := listDiscoveredProjects()
	if len(projects) == 0 {
		return
	}

	// Sort by most recently modified (check .git/HEAD mtime as proxy)
	type projectMod struct {
		path    string
		modTime int64
	}
	var sorted []projectMod
	for _, p := range projects {
		gitHead := filepath.Join(p.Path, ".git", "HEAD")
		info, err := os.Stat(gitHead)
		if err != nil {
			continue
		}
		sorted = append(sorted, projectMod{path: p.Path, modTime: info.ModTime().Unix()})
	}
	// Sort by most recent first
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].modTime > sorted[i].modTime {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Cache top 3 most recent (fewer projects, deeper analysis per project)
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, pm := range sorted[:limit] {
		projectName := filepath.Base(pm.path)
		info := DetectProjectInfo(pm.path)
		quickActions := generateQuickActions(pm.path, projectName, info.Framework)

		var history []string
		if taskMgr != nil {
			for _, t := range taskMgr.ListTasks() {
				if len(history) >= 10 {
					break
				}
				history = append(history, t.Title)
			}
		}

		state := &VibingState{
			Project:      projectName,
			Path:         pm.path,
			Framework:    info.Framework,
			Suggestions:  nil,
			QuickActions: quickActions,
			History:      history,
			GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		}

		// Store cache immediately (fast response — suggestions come from Deep Shuffle on demand)
		vibingCache.mu.Lock()
		vibingCache.cache[pm.path] = state
		vibingCache.mu.Unlock()
		log.Printf("[vibing-cache] Pre-warmed %s with %d quick actions (suggestions via Deep Shuffle)", projectName, len(quickActions))

		// Run iterative deep analysis in background (pre-warm Deep Shuffle results)
		if taskMgr != nil {
			deepSuggestions := runDeepVibingAnalysis(taskMgr, pm.path, projectName, info)
			if len(deepSuggestions) > 0 {
				vibingCache.mu.Lock()
				cached := vibingCache.cache[pm.path]
				if cached != nil {
					cached.Suggestions = deepSuggestions
					cached.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
				}
				vibingCache.mu.Unlock()
				log.Printf("[vibing-cache] Deep analysis done for %s: %d AI suggestions added", projectName, len(deepSuggestions))
			}
		}
	}
}

// runDeepVibingAnalysis runs 5 iterative steps with the AI agent to produce
// deep, context-rich suggestions. Each step feeds into the next.
//
// Step 1: Analyze project structure and key areas
// Step 2: Identify gaps and missing features
// Step 3: Find bugs and quality issues
// Step 4: Propose next features based on momentum
// Step 5: Synthesize into prioritized action items
func runDeepVibingAnalysis(taskMgr *TaskManager, projectPath, projectName string, info ProjectInfo) []VibingSuggestion {
	commits, activeFiles := getRecentGitActivity(projectPath)
	recentWork := ""
	if len(commits) > 0 {
		recentWork = strings.Join(commits[:min(10, len(commits))], "\n")
	}

	readme := ""
	for _, name := range []string{"README.md", "readme.md"} {
		if data, err := os.ReadFile(filepath.Join(projectPath, name)); err == nil {
			readme = string(data)
			if len(readme) > 3000 {
				readme = readme[:3000]
			}
			break
		}
	}

	activeFilesStr := ""
	if len(activeFiles) > 0 {
		activeFilesStr = strings.Join(activeFiles[:min(15, len(activeFiles))], "\n")
	}

	// Common preamble for all steps
	preamble := fmt.Sprintf("Project: %s\nFramework: %s\nRecent commits:\n%s\nActive files:\n%s\n",
		projectName, info.Framework, recentWork, activeFilesStr)

	// Step prompts — each references output from previous steps
	steps := []struct {
		name   string
		prompt func(prevOutputs []string) string
	}{
		{
			name: "structure",
			prompt: func(_ []string) string {
				return fmt.Sprintf(`%s
README excerpt:
%s

Analyze this project. List:
1. The 5 most important modules/areas and what they do (1 line each)
2. The tech stack and key dependencies
3. What the project does well
4. Gaps you notice

Be concise — bullet points only, no code.`, preamble, readme)
			},
		},
		{
			name: "gaps",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s
Previous analysis:
%s

Based on this analysis, identify the 5 most impactful missing features or improvements.
For each one: one line describing what to build and why it matters.
Focus on things users would actually notice. Be specific, not generic.`, preamble, truncate(prev[0], 2000))
			},
		},
		{
			name: "quality",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s
Project structure:
%s

Read the most active files and identify:
1. Top 3 bugs or error handling issues
2. Top 3 code quality improvements (dead code, complexity, naming)
3. Top 3 missing tests

One line each. Be specific — name the file and function.`, preamble, truncate(prev[0], 1500))
			},
		},
		{
			name: "features",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s
What the project needs (from analysis):
%s

Quality issues found:
%s

What are the 5 most impactful things to build next? Consider:
- Recent momentum (what's being actively worked on)
- Gaps identified above
- Quality issues that need fixing
- Features users would notice

For each: one line with concrete scope (not vague).`, preamble, truncate(prev[1], 1500), truncate(prev[2], 1500))
			},
		},
		{
			name: "synthesize",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`Based on this deep analysis of %s:

Structure: %s
Gaps: %s
Quality: %s
Feature ideas: %s

Think BIG. Generate the most exciting, ambitious, "wow that would be cool" feature ideas.
Not boring stuff like "add tests" or "clean up code" — think features that would make users say "whoa".
Think viral features, delightful UX, smart automations, things that feel like magic.

Return ONLY a JSON array with 8 bombastic ideas. No markdown, no explanation.
Each item must have ALL these fields:
[{"icon":"emoji","label":"catchy title (max 5 words)","desc":"one exciting sentence","category":"feature|bugfix|refactor","prompt":"detailed 2-3 sentence instruction for an AI agent to implement this","priority":1,"reasoning":"2-3 sentences: WHY this idea is brilliant for this project specifically. What use cases does it unlock? Why would users love it? What makes it a perfect fit given the current codebase and momentum?"}]
Priority: 1=game-changer, 2=impressive, 3=cool.`, projectName,
					truncate(prev[0], 800), truncate(prev[1], 800),
					truncate(prev[2], 800), truncate(prev[3], 800))
			},
		},
	}

	var outputs []string
	for i, step := range steps {
		prompt := step.prompt(outputs)
		log.Printf("[vibing-deep] %s step %d/%d: %s", projectName, i+1, len(steps), step.name)

		output, err := runVibingTask(taskMgr, projectPath, prompt, 3*time.Minute)
		if err != nil {
			log.Printf("[vibing-deep] %s step %d failed: %v — stopping", projectName, i+1, err)
			break
		}
		outputs = append(outputs, output)
		log.Printf("[vibing-deep] %s step %d done (%d chars)", projectName, i+1, len(output))
	}

	// Parse the final step's output as JSON suggestions
	if len(outputs) < len(steps) {
		return nil // didn't complete all steps
	}

	finalOutput := outputs[len(outputs)-1]
	return parseVibingSuggestions(finalOutput)
}

// runVibingTask creates a task, waits for it to finish, and returns the output.
// This is the bridge between the vibing system and any AI agent — agent-agnostic.
func runVibingTask(taskMgr *TaskManager, workDir, prompt string, timeout time.Duration) (string, error) {
	return runVibingTaskStreaming(taskMgr, workDir, prompt, timeout, nil)
}

// runVibingTaskStreaming runs a vibing task and calls onLine for each output line as it arrives.
// This enables real-time streaming of the AI's thinking to the mobile client.
func runVibingTaskStreaming(taskMgr *TaskManager, workDir, prompt string, timeout time.Duration, onLine func(line string)) (string, error) {
	// Temporarily switch workDir for this task
	taskMgr.mu.Lock()
	origDir := taskMgr.workDir
	taskMgr.workDir = workDir
	taskMgr.mu.Unlock()

	task, err := taskMgr.CreateTask(prompt, "", "haiku", "vibing-cache", "", "", nil)

	// Restore workDir
	taskMgr.mu.Lock()
	taskMgr.workDir = origDir
	taskMgr.mu.Unlock()

	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	// Wait for task to finish (or timeout), streaming output lines
	taskObj, ok := taskMgr.GetTask(task.ID)
	if !ok {
		return "", fmt.Errorf("task %s not found after creation", task.ID)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case line, ok := <-taskObj.outputCh:
			if !ok {
				// Channel closed — task is done
				goto done
			}
			if onLine != nil && strings.TrimSpace(line) != "" {
				onLine(line)
			}
		case <-taskObj.doneCh:
			// Drain remaining output
			for {
				select {
				case line, ok := <-taskObj.outputCh:
					if !ok {
						goto done
					}
					if onLine != nil && strings.TrimSpace(line) != "" {
						onLine(line)
					}
				default:
					goto done
				}
			}
		case <-timer.C:
			_ = taskMgr.StopTask(task.ID)
			return "", fmt.Errorf("task %s timed out after %v", task.ID, timeout)
		}
	}

done:
	// Get the output
	taskObj, ok = taskMgr.GetTask(task.ID)
	if !ok {
		return "", fmt.Errorf("task %s disappeared", task.ID)
	}

	if taskObj.Status == TaskStatusFailed {
		return "", fmt.Errorf("task %s failed", task.ID)
	}

	// Prefer ResultText (clean), fall back to Output (raw)
	output := taskObj.ResultText
	if output == "" {
		output = taskObj.Output
	}
	return output, nil
}

// parseVibingSuggestions extracts structured suggestions from AI output.
// The AI should return a JSON array but might wrap it in markdown code fences.
func parseVibingSuggestions(output string) []VibingSuggestion {
	// Strip markdown code fences if present
	cleaned := output
	if idx := strings.Index(cleaned, "["); idx >= 0 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "]"); idx >= 0 {
		cleaned = cleaned[:idx+1]
	}

	var suggestions []VibingSuggestion
	if err := json.Unmarshal([]byte(cleaned), &suggestions); err != nil {
		log.Printf("[vibing-deep] Failed to parse suggestions JSON: %v", err)
		return nil
	}

	// Assign IDs if missing
	for i := range suggestions {
		if suggestions[i].ID == "" {
			suggestions[i].ID = fmt.Sprintf("deep-%d", i+1)
		}
	}

	return suggestions
}

func vibingPhoneProjectSlug(projectPath string) string {
	if strings.TrimSpace(projectPath) == "" {
		return ""
	}
	if manifest, err := LoadManifest(projectPath); err == nil && manifest != nil {
		if slug := strings.TrimSpace(manifest.Env["YAVER_PHONE_PROJECT"]); slug != "" {
			return slug
		}
	}
	if cfg, err := LoadProjectConfig(projectPath); err == nil && cfg != nil {
		if slug := strings.TrimSpace(cfg.Env["YAVER_PHONE_PROJECT"]); slug != "" {
			return slug
		}
	}
	return ""
}

func vibingRuntimeDeployPrompt(action vibingRuntimeDeployAction) string {
	body, _ := json.Marshal(action)
	return vibingRuntimeDeployPrefix + string(body)
}

func parseVibingRuntimeDeployPrompt(prompt string) (*vibingRuntimeDeployAction, bool) {
	if !strings.HasPrefix(strings.TrimSpace(prompt), vibingRuntimeDeployPrefix) {
		return nil, false
	}
	raw := strings.TrimPrefix(strings.TrimSpace(prompt), vibingRuntimeDeployPrefix)
	var action vibingRuntimeDeployAction
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		return nil, false
	}
	action.Slug = strings.TrimSpace(action.Slug)
	if action.Slug == "" {
		return nil, false
	}
	if len(action.Targets) == 0 {
		action.Targets = []string{"yaver-cloud"}
	}
	return &action, true
}

func executeVibingRuntimeDeploy(s *HTTPServer, action *vibingRuntimeDeployAction) (map[string]interface{}, error) {
	targets := make([]string, 0, len(action.Targets))
	seen := map[string]bool{}
	for _, target := range action.Targets {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		targets = []string{"yaver-cloud"}
	}

	args := map[string]interface{}{
		"slug":    action.Slug,
		"targets": targets,
		"run":     action.Run,
		"dry_run": action.DryRun,
	}
	handled, payload := dispatchPhoneMCP(s, "phone_project_runtime_deploy", mustJSONRawMessage(args))
	if !handled {
		return nil, fmt.Errorf("runtime deploy dispatch failed")
	}
	if out, ok := payload.(map[string]interface{}); ok {
		return out, nil
	}
	return map[string]interface{}{"result": payload}, nil
}

func mustJSONRawMessage(v interface{}) json.RawMessage {
	body, _ := json.Marshal(v)
	return body
}

// handleVibing returns vibing state for a project — suggestions, quick actions, history.
func (s *HTTPServer) handleVibing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	query := r.URL.Query().Get("query")
	path := r.URL.Query().Get("path")

	if path == "" && query != "" {
		found, err := findProject(query)
		if err != nil {
			jsonError(w, http.StatusNotFound, "project not found: "+err.Error())
			return
		}
		path = found
	}
	if path == "" {
		path = s.taskMgr.workDir
	}

	// Check cache first
	vibingCache.mu.RLock()
	cached, hasCached := vibingCache.cache[path]
	vibingCache.mu.RUnlock()

	// Build history from live task list
	var history []string
	for _, t := range s.taskMgr.ListTasks() {
		if len(history) >= 10 {
			break
		}
		history = append(history, t.Title)
	}

	if hasCached {
		// Return cached suggestions (from Deep Shuffle) + fresh history
		cached.History = history
		log.Printf("[vibing] Served from cache: %s (%d suggestions)", cached.Project, len(cached.Suggestions))
		jsonReply(w, http.StatusOK, cached)
		return
	}

	// No cache — return quick actions only (suggestions come from Deep Shuffle)
	projectName := filepath.Base(path)
	info := DetectProjectInfo(path)
	quickActions := generateQuickActions(path, projectName, info.Framework)

	state := &VibingState{
		Project:      projectName,
		Path:         path,
		Framework:    info.Framework,
		Suggestions:  nil,
		QuickActions: quickActions,
		History:      history,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// Cache it
	vibingCache.mu.Lock()
	vibingCache.cache[path] = state
	vibingCache.mu.Unlock()

	log.Printf("[vibing] Generated %d quick actions for %s (suggestions via Deep Shuffle)", len(quickActions), projectName)
	jsonReply(w, http.StatusOK, state)
}

func resolveVibingProject(projectPath, projectName, bundleID string) (string, string) {
	if strings.TrimSpace(projectPath) == "" {
		if strings.TrimSpace(bundleID) != "" {
			if proj := findMobileProjectByBundleID(bundleID); proj != nil {
				projectPath = proj.Path
				if projectName == "" {
					projectName = proj.Name
				}
			}
		}
		if strings.TrimSpace(projectPath) == "" && strings.TrimSpace(projectName) != "" {
			if proj := findMobileProjectByName(projectName); proj != nil {
				projectPath = proj.Path
				projectName = proj.Name
			}
		}
	}
	if strings.TrimSpace(projectName) == "" && strings.TrimSpace(projectPath) != "" {
		if proj := findMobileProjectByPath(projectPath); proj != nil {
			projectName = proj.Name
		}
	}
	return strings.TrimSpace(projectPath), strings.TrimSpace(projectName)
}

func (s *HTTPServer) resolveVibingProjectForRequest(projectPath, projectName, bundleID string) (string, string) {
	projectPath, projectName = resolveVibingProject(projectPath, projectName, bundleID)
	if projectPath != "" {
		return projectPath, projectName
	}

	// Web-SDK vibing fallbacks. The feedback SDK that ships with a web
	// app (e.g. carrotbet) only knows its own projectName/appName
	// (e.g. "carrotbet"). Mobile-project lookup doesn't find web apps,
	// so we also try the workspace manifest and fall through to
	// taskMgr.workDir only if no monorepo declaration matches.
	if strings.TrimSpace(projectName) != "" {
		if abs, resolvedName := resolveVibingProjectFromWorkspace(s, projectName); abs != "" {
			return abs, resolvedName
		}
	}

	// Last-pushed-bundle fallback. When a guest bundle was Hermes-
	// pushed via /dev/build-native (e.g. `yaver insert sfmg`), the
	// agent records {workDir, projectName} on success. A subsequent
	// /vibing/execute from inside that loaded guest — which in 1.18.34
	// only sends `prompt` (no projectName/bundleId) — gets routed
	// to the right project here instead of falling through to the
	// agent's global cwd. Set in handleBuildNativeBundle's success
	// path; protected by a small mutex.
	if s != nil {
		s.lastNativeBundleMu.Lock()
		lastPath := s.lastNativeBundleProjectPath
		lastName := s.lastNativeBundleProjectName
		s.lastNativeBundleMu.Unlock()
		if projectPath == "" && lastPath != "" {
			projectPath = lastPath
			if projectName == "" {
				projectName = lastName
			}
		}
	}

	if s != nil && s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" && projectPath == "" {
		projectPath = strings.TrimSpace(s.taskMgr.workDir)
	}
	if projectPath == "" && s != nil && s.devServerMgr != nil {
		if st := s.devServerMgr.Status(); st != nil && strings.TrimSpace(st.WorkDir) != "" {
			projectPath = strings.TrimSpace(st.WorkDir)
		}
	}
	if projectName == "" && projectPath != "" {
		projectName = DetectProjectInfo(projectPath).Name
	}
	return strings.TrimSpace(projectPath), strings.TrimSpace(projectName)
}

// resolveVibingProjectFromWorkspace looks up projectName against the
// declarative monorepo manifest. Any app whose name matches (case-
// insensitive) and whose on-disk directory exists is returned.
// Returns ("","") when no match — callers fall through to other
// heuristics. Never errors; a missing / malformed manifest just
// means "no workspace match."
func resolveVibingProjectFromWorkspace(s *HTTPServer, projectName string) (string, string) {
	if s == nil || s.taskMgr == nil {
		return "", ""
	}
	root := s.taskMgr.workDir
	if root == "" {
		return "", ""
	}
	m, err := LoadWorkspaceManifest(root)
	if err != nil || m == nil {
		return "", ""
	}
	target := strings.ToLower(strings.TrimSpace(projectName))
	for i := range m.Apps {
		app := &m.Apps[i]
		if strings.ToLower(app.Name) != target {
			continue
		}
		abs := appAbsPath(root, m, app)
		if abs == "" {
			continue
		}
		if _, statErr := os.Stat(abs); statErr != nil {
			continue
		}
		return abs, app.Name
	}
	return "", ""
}

func (s *HTTPServer) vibingRunnerStatus(projectPath string) (RunnerConfig, RunnerRuntimeStatus, error) {
	runnerCfg := defaultRunner
	if s != nil && s.taskMgr != nil {
		runnerCfg = s.taskMgr.runner
	}
	if err := CheckRunnerBinary(runnerCfg.Command); err != nil {
		return runnerCfg, RunnerRuntimeStatus{}, err
	}
	return runnerCfg, DetectRunnerRuntimeStatus(runnerCfg, projectPath), nil
}

func (s *HTTPServer) handleVibingEligibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	projectPath, projectName := s.resolveVibingProjectForRequest(
		r.URL.Query().Get("projectPath"),
		r.URL.Query().Get("projectName"),
		r.URL.Query().Get("bundleId"),
	)
	if projectPath == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"canVibe":       false,
			"reason":        "This app is not linked to a detected project on the selected machine.",
			"guidance":      "Open the repo on a machine running `yaver serve`, then try again.",
			"needsGitSetup": false,
		})
		return
	}

	if guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")); guestUID != "" {
		if s.guestConfigMgr != nil && !s.guestConfigMgr.GuestCanAccessProject(guestUID, projectName) {
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":            true,
				"canVibe":       false,
				"projectName":   projectName,
				"reason":        "Your guest access is not allowed for this project.",
				"guidance":      "Ask the host to add this repo to your Feedback SDK guest grant.",
				"needsGitSetup": false,
			})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"canVibe":       true,
			"projectName":   projectName,
			"projectPath":   projectPath,
			"guidance":      "Using host-granted Feedback SDK access for this repo.",
			"needsGitSetup": false,
		})
		return
	}

	repoRemote := detectRepoRemoteFromGit(projectPath)
	providerKind := repoRemote.Provider
	repoFullName := repoRemote.Repo
	repoBindingSource := "git"
	if providerKind == "" || strings.TrimSpace(repoFullName) == "" {
		// Fall back to the agent-side project-remote registry. This lets a
		// host declare "project carrotbet lives at github.com/foo/bar" via
		// the web feedback SDK before any clone exists on disk.
		if registered := findProjectRemote(projectName); registered != nil {
			providerKind = CIProvider(registered.Provider)
			if strings.TrimSpace(registered.Repo) != "" {
				repoFullName = registered.Repo
			}
			if strings.TrimSpace(registered.Host) != "" {
				repoRemote.Host = registered.Host
			}
			repoBindingSource = "registry"
		}
	}
	if providerKind == "" || strings.TrimSpace(repoFullName) == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"canVibe":       false,
			"projectName":   projectName,
			"projectPath":   projectPath,
			"reason":        "This project is not connected to GitHub or GitLab.",
			"guidance":      "Connect git for this repo first. If the project is not in your GitHub/GitLab account, vibe coding stays disabled.",
			"needsGitSetup": true,
		})
		return
	}

	host := firstNonEmpty(strings.TrimSpace(repoRemote.Host), "github.com")
	if providerKind == CIGitLab && host == "github.com" {
		host = "gitlab.com"
	}
	provider := findProvider(host)
	if provider == nil || strings.TrimSpace(provider.Token) == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"canVibe":           false,
			"projectName":       projectName,
			"projectPath":       projectPath,
			"provider":          providerKind,
			"repoHost":          host,
			"repoFullName":      repoFullName,
			"repoBindingSource": repoBindingSource,
			"reason":            fmt.Sprintf("Connect your %s account in Yaver before vibe coding.", strings.Title(string(providerKind))),
			"guidance":          "This project must be visible in your connected git account.",
			"needsGitSetup":     true,
		})
		return
	}

	var (
		repos []RemoteRepo
		err   error
	)
	switch providerKind {
	case CIGitHub:
		repos, err = listGitHubReposPaged(provider.Token, 100, 10)
	case CIGitLab:
		repos, err = listGitLabReposPaged(host, provider.Token, 100, 10)
	default:
		err = fmt.Errorf("unsupported provider")
	}
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"canVibe":           false,
			"projectName":       projectName,
			"projectPath":       projectPath,
			"provider":          providerKind,
			"repoHost":          host,
			"repoFullName":      repoFullName,
			"repoBindingSource": repoBindingSource,
			"reason":            "Could not verify your repository access right now.",
			"guidance":          "Reconnect your git account in Yaver and try again.",
			"needsGitSetup":     true,
		})
		return
	}

	visible := false
	for _, repo := range repos {
		if strings.EqualFold(strings.TrimSpace(repo.FullName), strings.TrimSpace(repoFullName)) {
			visible = true
			break
		}
	}
	if !visible {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"canVibe":           false,
			"projectName":       projectName,
			"projectPath":       projectPath,
			"provider":          providerKind,
			"repoHost":          host,
			"repoFullName":      repoFullName,
			"repoBindingSource": repoBindingSource,
			"reason":            "This project is not in your connected git account, so vibe coding is disabled.",
			"guidance":          fmt.Sprintf("You need GitHub/GitLab access to %s before Yaver can vibe code on it.", repoFullName),
			"needsGitSetup":     true,
		})
		return
	}

	runnerCfg, runnerStatus, runnerErr := s.vibingRunnerStatus(projectPath)
	if runnerErr != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"canVibe":           false,
			"projectName":       projectName,
			"projectPath":       projectPath,
			"provider":          providerKind,
			"repoHost":          host,
			"repoFullName":      repoFullName,
			"repoBindingSource": repoBindingSource,
			"runner":            normalizeRunnerID(runnerCfg.RunnerID),
			"reason":            fmt.Sprintf("%s is not installed on this machine.", runnerCfg.Name),
			"guidance":          strings.TrimSpace(runnerErr.Error()),
			"needsGitSetup":     false,
		})
		return
	}
	if !runnerStatus.Ready {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":                true,
			"canVibe":           false,
			"projectName":       projectName,
			"projectPath":       projectPath,
			"provider":          providerKind,
			"repoHost":          host,
			"repoFullName":      repoFullName,
			"repoBindingSource": repoBindingSource,
			"runner":            normalizeRunnerID(runnerCfg.RunnerID),
			"needsRunnerAuth":   !runnerStatus.AuthConfigured,
			"reason":            fmt.Sprintf("%s is not ready on this machine.", runnerCfg.Name),
			"guidance":          firstNonEmpty(strings.TrimSpace(runnerStatus.Error), strings.TrimSpace(runnerStatus.Warning)),
			"needsGitSetup":     false,
		})
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":                true,
		"canVibe":           true,
		"projectName":       projectName,
		"projectPath":       projectPath,
		"provider":          providerKind,
		"repoHost":          host,
		"repoFullName":      repoFullName,
		"repoBindingSource": repoBindingSource,
		"runner":            normalizeRunnerID(runnerCfg.RunnerID),
		"needsGitSetup":     false,
	})
}

// handleVibingSurprise runs iterative deep analysis and streams suggestions via SSE.
// Each step produces ideas that appear one-by-one in the mobile UI.
func (s *HTTPServer) handleVibingSurprise(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		ProjectPath string `json:"projectPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	projectPath := req.ProjectPath
	projectName := filepath.Base(projectPath)
	info := DetectProjectInfo(projectPath)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)

	sendSSE := func(event string, data interface{}) {
		jsonBytes, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonBytes))
		if canFlush {
			flusher.Flush()
		}
	}

	// Send status updates as we go
	sendSSE("status", map[string]string{"message": "Analyzing project structure..."})

	commits, activeFiles := getRecentGitActivity(projectPath)
	recentWork := ""
	if len(commits) > 0 {
		recentWork = strings.Join(commits[:min(10, len(commits))], "\n")
	}

	readme := ""
	for _, name := range []string{"README.md", "readme.md"} {
		if data, err := os.ReadFile(filepath.Join(projectPath, name)); err == nil {
			readme = string(data)
			if len(readme) > 3000 {
				readme = readme[:3000]
			}
			break
		}
	}

	activeFilesStr := ""
	if len(activeFiles) > 0 {
		activeFilesStr = strings.Join(activeFiles[:min(15, len(activeFiles))], "\n")
	}

	preamble := fmt.Sprintf("Project: %s\nFramework: %s\nRecent commits:\n%s\nActive files:\n%s\n",
		projectName, info.Framework, recentWork, activeFilesStr)

	// 5 iterative steps — each one can produce suggestions that stream to the client
	type diceStep struct {
		name    string
		status  string
		prompt  func(prevOutputs []string) string
		extract bool // try to extract suggestions from output
	}

	steps := []diceStep{
		{
			name:   "explore",
			status: "Reading codebase and understanding architecture...",
			prompt: func(_ []string) string {
				return fmt.Sprintf(`%s
README:
%s

You are a creative product visionary analyzing this project. Identify:
1. What this project does and who uses it
2. The 3 most interesting technical capabilities
3. What's unique about the architecture
4. What adjacent problems users might have

Be concise — bullet points. Think like a startup founder looking for the next big thing.`, preamble, readme)
			},
		},
		{
			name:   "wild-ideas",
			status: "Brainstorming wild ideas...",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s

Project analysis:
%s

Think WILD. What are 5 features that would make people tweet about this project?
Not incremental improvements — think "holy shit that's cool" features.
Think: AI-powered features, real-time collaboration, smart automations, viral mechanics, delightful surprises.
For each: one catchy name + one sentence why it's exciting.`, preamble, truncate(prev[0], 2000))
			},
			extract: true,
		},
		{
			name:   "practical-magic",
			status: "Finding practical magic...",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s

Wild ideas brainstorm:
%s

Now ground these in reality. For the most feasible ones, what would make them ACTUALLY work
with this codebase? Also think of 3 NEW ideas that are:
- Buildable in a few hours
- Would genuinely surprise users
- Leverage what's already built

For each: catchy name + why it fits THIS project specifically.`, preamble, truncate(prev[1], 2000))
			},
			extract: true,
		},
		{
			name:   "moonshots",
			status: "Dreaming up moonshots...",
			prompt: func(prev []string) string {
				return fmt.Sprintf(`%s

Previous ideas:
%s

%s

Now combine the wildest ideas with the practical ones. Generate 4 "moonshot" features that are:
- Technically ambitious but achievable
- Would genuinely differentiate this project
- Feel like magic to users
- Have clear use cases

For each: name + one sentence + the "wow" factor.`, preamble, truncate(prev[1], 1500), truncate(prev[2], 1500))
			},
			extract: true,
		},
		{
			name:    "final",
			status:  "Crafting final suggestions...",
			extract: true,
			prompt: func(prev []string) string {
				return fmt.Sprintf(`You've deeply analyzed %s. Here's what you found:

Architecture: %s
Wild ideas: %s
Practical magic: %s
Moonshots: %s

Pick the 8 BEST ideas — the ones that would make someone say "I need to build this RIGHT NOW."
Mix of quick wins (buildable today) and ambitious features (worth the effort).

Return ONLY a JSON array. No markdown, no explanation.
Each item MUST have ALL these fields:
[{"icon":"emoji","label":"catchy title (max 5 words)","desc":"one exciting sentence that sells it","category":"feature|bugfix|refactor","prompt":"detailed 2-3 sentence instruction for an AI agent to implement this feature","priority":1,"reasoning":"2-3 sentences explaining: WHY is this brilliant for %s specifically? What use cases does it unlock? Why would users love it? What makes it a perfect fit given the codebase?"}]

Priority: 1=build this tonight, 2=build this week, 3=add to roadmap.
Make the reasoning compelling and specific to this project — not generic.`, projectName,
					truncate(prev[0], 600), truncate(prev[1], 600),
					truncate(prev[2], 600), truncate(prev[3], 600), projectName)
			},
		},
	}

	var outputs []string
	for i, step := range steps {
		sendSSE("status", map[string]string{
			"message": step.status,
			"step":    fmt.Sprintf("%d/%d", i+1, len(steps)),
		})

		prompt := step.prompt(outputs)
		log.Printf("[vibing-dice] %s step %d/%d: %s", projectName, i+1, len(steps), step.name)

		// Stream the AI's output lines to mobile as "thinking" events
		onLine := func(line string) {
			// Clean up runner-specific formatting (stream-json artifacts, etc.)
			clean := strings.TrimSpace(line)
			if clean == "" || strings.HasPrefix(clean, "{\"type\"") || strings.HasPrefix(clean, "```") {
				return
			}
			// Strip common AI output prefixes
			for _, prefix := range []string{"- ", "* ", "> "} {
				clean = strings.TrimPrefix(clean, prefix)
			}
			if len(clean) > 200 {
				clean = clean[:200] + "..."
			}
			sendSSE("thinking", map[string]string{
				"text": clean,
				"step": fmt.Sprintf("%d/%d", i+1, len(steps)),
			})
		}

		output, err := runVibingTaskStreaming(s.taskMgr, projectPath, prompt, 3*time.Minute, onLine)
		if err != nil {
			log.Printf("[vibing-dice] %s step %d failed: %v", projectName, i+1, err)
			sendSSE("error", map[string]string{"message": fmt.Sprintf("Step %d failed: %v", i+1, err)})
			outputs = append(outputs, "")
			continue
		}
		outputs = append(outputs, output)

		// Try to extract and stream intermediate suggestions
		if step.extract && i < len(steps)-1 {
			// For intermediate steps, try to parse any inline suggestions
			// (won't always work since they're not always JSON, but worth trying)
			if sgs := parseVibingSuggestions(output); len(sgs) > 0 {
				for _, sg := range sgs {
					sendSSE("suggestion", sg)
				}
			}
		}
	}

	// Parse and stream final suggestions
	if len(outputs) >= len(steps) && outputs[len(outputs)-1] != "" {
		finalSuggestions := parseVibingSuggestions(outputs[len(outputs)-1])
		for _, sg := range finalSuggestions {
			sendSSE("suggestion", sg)
		}

		// Also update the vibing cache
		vibingCache.mu.Lock()
		if cached, ok := vibingCache.cache[projectPath]; ok {
			cached.Suggestions = finalSuggestions
			cached.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		}
		vibingCache.mu.Unlock()
	}

	sendSSE("done", map[string]string{"message": "Done!"})
}

// handleVibingExecute runs a vibing suggestion as a task.
func (s *HTTPServer) handleVibingExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Prompt      string `json:"prompt"`
		ProjectPath string `json:"projectPath"`
		// Feedback SDK passes the running app's bundle id + project
		// name so we don't have to guess the target repo from the
		// free-text prompt. Without this, the fallback matcher runs
		// substring search across discovered projects and picks the
		// wrong one catastrophically (e.g. "in" inside 'Vibing' matched
		// the unrelated 'mprint' project and Claude vibed on the wrong
		// repo). Prefer these in the order below; only fall through to
		// the loose prompt-matcher when nothing identifies the target.
		ProjectName string `json:"projectName"`
		BundleID    string `json:"bundleId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Resolve the project directory in priority order:
	//   1. explicit projectPath (power users / CLI callers)
	//   2. SDK-provided bundleId   → scan mobile projects' Info.plist
	//   3. SDK-provided projectName → match against cached project names
	// If all three come up empty we leave taskMgr.workDir unchanged
	// and the taskMgr's own auto-detect runs (with the sloppy
	// substring matcher that burned us on "in" → mprint).
	req.ProjectPath, req.ProjectName = s.resolveVibingProjectForRequest(req.ProjectPath, req.ProjectName, req.BundleID)

	// H-8: project-gate guest callers BEFORE we mutate s.taskMgr.workDir.
	// Without this check a full-scope guest could pivot the agent's
	// global workdir to /Users/owner/.ssh by passing projectPath in the
	// request body — and any concurrent owner-spawned task would inherit
	// that workdir. The /tasks handler already does this; /vibing/execute
	// did not, until now.
	guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUID != "" && s.guestConfigMgr != nil {
		if !s.guestConfigMgr.GuestCanAccessProject(guestUID, req.ProjectName) {
			jsonError(w, http.StatusForbidden, "this guest is scoped to specific projects; the requested project is not in the allowed list")
			return
		}
	}

	if req.ProjectPath != "" {
		// Guest callers must NOT mutate the global taskMgr.workDir —
		// a concurrent owner request would silently inherit the guest's
		// chosen path. Pass workDir through TaskCreateOptions instead
		// so it stays local to this task.
		if guestUID == "" {
			s.taskMgr.mu.Lock()
			s.taskMgr.workDir = req.ProjectPath
			s.taskMgr.mu.Unlock()
		}
	}

	info := DetectProjectInfo(req.ProjectPath)
	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}
	if action, ok := parseVibingRuntimeDeployPrompt(req.Prompt); ok {
		result, err := executeVibingRuntimeDeploy(s, action)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":            true,
			"taskId":        "",
			"runtimeDeploy": result,
			"message":       firstNonEmpty(action.Prompt, "Runtime deploy executed."),
		})
		return
	}
	prompt := req.Prompt
	if ctx := vibingExecutionContext(req.ProjectPath, info.Framework, target, isDirectConnection(r)); ctx != "" {
		prompt = ctx + "\n\nUser request:\n" + req.Prompt
	}

	// Pick a runner that's actually ready instead of blindly trusting
	// the configured primary. Real symptom: yaver-test-ephemeral has
	// codex set as primary but codex isn't signed in, so the SDK chat
	// (which earlier saw "Claude Code ready" via /vibing/eligibility)
	// would create the task → CreateTask spawned codex → codex
	// crashed instantly with an auth error → empty output → user
	// stares at "Task running…" forever. Now we walk every installed
	// builtin runner and pick the first one that passes
	// CheckRunnerReady (binary present + auth/config check). The
	// configured primary still wins when it's actually ready, so the
	// behaviour for healthy machines is unchanged.
	pickedRunner := pickReadyVibingRunner(s)
	if guestUID != "" {
		// A guest's vibe must NEVER spend the owner's Claude/Codex
		// subscription: it double-bills the owner's personal plan (ToS) and
		// a subscription CLI can't authenticate inside the guest's isolation
		// container anyway. Route to a GLM/BYO runner; refuse (fail-closed)
		// if none is ready rather than silently fall back to the owner's plan.
		if pickedRunner == "" || isSubscriptionRunner(pickedRunner) {
			pickedRunner = pickReadyGuestVibeRunner(s)
		}
		if pickedRunner == "" {
			jsonError(w, http.StatusServiceUnavailable,
				"guest vibe requires a GLM/BYO runner (subscription runners are owner-only); none is ready on this machine")
			return
		}
	}

	taskOpts := TaskCreateOptions{}
	if guestUID != "" {
		taskOpts.GuestUserID = guestUID
		// Per-task workDir (instead of mutating the global taskMgr.workDir
		// which would leak the guest's project choice to concurrent owner
		// tasks). Empty string falls back to taskMgr.workDir as before.
		taskOpts.WorkDir = req.ProjectPath
		if s.guestConfigMgr != nil {
			cfg := s.guestConfigMgr.GetConfig(guestUID)
			// Force container isolation for feedback-only AND sdk-project
			// (tester) guests. A tester who opted into vibe is running the AI
			// coding agent against the dev's repo — it MUST be sandboxed
			// (no host filesystem, no ~/.yaver, no .git/config token) exactly
			// like the feedback-only fix path.
			taskOpts.GuestRequireIsolation = guestRequireIsolation(cfg) ||
				s.guestConfigMgr.IsFeedbackOnly(guestUID) ||
				s.guestConfigMgr.GetScope(guestUID) == GuestScopeSDKProject
			taskOpts.GuestUseHostAPIKeys = guestUseHostAPIKeys(cfg)
			taskOpts.GuestAllowGuestProvidedKeys = cfg == nil || cfg.AllowGuestProvidedAPIKeys == nil || *cfg.AllowGuestProvidedAPIKeys
			if cfg != nil {
				taskOpts.GuestCPULimitPercent = cfg.CPULimitPercent
				taskOpts.GuestRAMLimitMB = cfg.RAMLimitMB
			}
		}
	}

	task, err := s.taskMgr.CreateTaskWithOptions(prompt, "", "", "vibing", pickedRunner, "", nil, taskOpts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"runner": pickedRunner,
	})
}

// pickReadyVibingRunner returns a runnerID whose binary is installed
// AND whose runner-specific auth/config checks pass for the agent's
// current workdir. Preference order:
//
//  1. The agent's configured primary runner if it's ready AND in the
//     supported allowlist (claude / codex / opencode).
//  2. The first runner from supportedRunnerIDs that's ready, in
//     supportedRunnerIDs order (deterministic — not map iteration).
//  3. Empty string — let CreateTask fall through to its own defaults.
//     (CreateTask already has a "first installed builtin" fallback,
//     so we never block here even if nothing passes the auth check.)
//
// Why we restrict to supportedRunnerIDs instead of walking the full
// builtinRunners map: LoadRunnersFromBackend injects whatever Convex
// has in its runners table (aider, goose, amp, ollama, …) into the
// global builtinRunners. Iterating that map dropped feedback tasks
// onto aider when it happened to be installed and not auth'd — the
// user saw aider's OpenRouter sign-in banner instead of their picked
// runner running. Mobile UI only offers claude/codex/opencode, so
// the picker should match.
// isSubscriptionRunner reports whether a runnerID authenticates via a personal
// subscription login (Claude Max / ChatGPT Plus) rather than an API key. These
// are owner-only: a guest must never drive them — it double-bills the owner's
// plan and breaks the single-user subscription model. GLM/BYO runners
// (glm, opencode, aider, …) are key-based and safe to lend.
func isSubscriptionRunner(runnerID string) bool {
	switch normalizeRunnerID(runnerID) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

// pickReadyGuestVibeRunner is pickReadyVibingRunner restricted to GLM/BYO
// runners: it walks supportedRunnerIDs in order, skips subscription runners
// entirely, and returns the first key-based runner that is ready. Returns ""
// when no non-subscription runner is ready, so the caller can fail closed
// rather than fall back to the owner's personal plan.
func pickReadyGuestVibeRunner(s *HTTPServer) string {
	if s == nil || s.taskMgr == nil {
		return ""
	}
	s.taskMgr.mu.Lock()
	workDir := s.taskMgr.workDir
	s.taskMgr.mu.Unlock()
	for _, id := range supportedRunnerIDs {
		if isSubscriptionRunner(id) {
			continue
		}
		runner, ok := builtinRunners[id]
		if !ok {
			continue
		}
		if err := CheckRunnerReady(runner, workDir); err == nil {
			return id
		}
	}
	return ""
}

func pickReadyVibingRunner(s *HTTPServer) string {
	if s == nil || s.taskMgr == nil {
		return ""
	}
	workDir := ""
	s.taskMgr.mu.Lock()
	primary := s.taskMgr.runner
	workDir = s.taskMgr.workDir
	s.taskMgr.mu.Unlock()

	primaryID := normalizeRunnerID(primary.RunnerID)
	if strings.TrimSpace(primary.Command) != "" && IsSupportedRunner(primaryID) {
		if err := CheckRunnerReady(primary, workDir); err == nil {
			return primary.RunnerID
		}
	}
	for _, id := range supportedRunnerIDs {
		if normalizeRunnerID(id) == primaryID {
			continue
		}
		runner, ok := builtinRunners[id]
		if !ok {
			continue
		}
		if err := CheckRunnerReady(runner, workDir); err == nil {
			return id
		}
	}
	return ""
}

// handleVibingTaskByID is the SDK-accessible mirror of /tasks/{id}.
//
// /tasks/{id} requires owner-grade auth, so a Feedback SDK token
// (which can call /vibing/execute) used to be unable to read its
// own task back — leaving the chat UI's poll loop hanging on an
// empty bubble forever. This endpoint accepts SDK / guest tokens
// the same way /vibing/execute does, but only returns tasks whose
// Source == "vibing" so non-vibing tasks (shell, code-cli, etc.)
// stay owner-only.
//
// Routes handled:
//
//	GET  /vibing/task/<id>           → task info (status + output blob)
//	POST /vibing/task/<id>/continue  → append a follow-up turn
//
// The reply shape mirrors the corresponding /tasks/{id} reply
// (`{ ok: true, task: TaskInfo }`) so the SDK can share the
// unwrap logic across both code paths.
func (s *HTTPServer) handleVibingTaskByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/vibing/task/")
	parts := strings.SplitN(rest, "/", 2)
	taskID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if taskID == "" {
		jsonError(w, http.StatusBadRequest, "task ID required")
		return
	}

	task, ok := s.taskMgr.GetTask(taskID)
	if !ok {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	// Tripwire: never serve non-vibing tasks through this endpoint
	// even if the SDK guesses an unrelated task ID. Source is set by
	// CreateTask and is immutable for the task's lifetime.
	if !strings.EqualFold(strings.TrimSpace(task.Source), "vibing") {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	switch action {
	case "":
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		info := s.taskInfoFromTask(task, r)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":   true,
			"task": info,
		})
	case "continue":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		input := strings.TrimSpace(body.Input)
		if input == "" {
			jsonError(w, http.StatusBadRequest, "input is required")
			return
		}
		resumed, err := s.taskMgr.ResumeTaskWithOptions(taskID, input, nil, TaskResumeOptions{})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("resume failed: %v", err))
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"taskId": resumed.ID,
			"status": resumed.Status,
		})
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}
