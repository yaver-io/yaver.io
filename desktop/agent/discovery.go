package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const projectsFileName = "PROJECTS.md"

type projectDiscoveryStatus struct {
	mu              sync.RWMutex
	inProgress      bool
	lastStartedAt   string
	lastCompletedAt string
	lastError       string
}

type projectDiscoverySnapshot struct {
	Status          string `json:"status"`
	Discovering     bool   `json:"discovering"`
	PartiallyReady  bool   `json:"partiallyReady"`
	LastStartedAt   string `json:"lastStartedAt,omitempty"`
	LastCompletedAt string `json:"lastCompletedAt,omitempty"`
	LastError       string `json:"lastError,omitempty"`
}

var discoveryStatusState = &projectDiscoveryStatus{}

func markProjectDiscoveryStarted() {
	discoveryStatusState.mu.Lock()
	defer discoveryStatusState.mu.Unlock()
	discoveryStatusState.inProgress = true
	discoveryStatusState.lastStartedAt = time.Now().UTC().Format(time.RFC3339)
	discoveryStatusState.lastError = ""
}

func markProjectDiscoveryFinished(err error) {
	discoveryStatusState.mu.Lock()
	defer discoveryStatusState.mu.Unlock()
	discoveryStatusState.inProgress = false
	if err != nil {
		discoveryStatusState.lastError = err.Error()
		return
	}
	discoveryStatusState.lastCompletedAt = time.Now().UTC().Format(time.RFC3339)
	discoveryStatusState.lastError = ""
}

func currentProjectDiscoverySnapshot() projectDiscoverySnapshot {
	discoveryStatusState.mu.RLock()
	defer discoveryStatusState.mu.RUnlock()
	snap := projectDiscoverySnapshot{
		Discovering:     discoveryStatusState.inProgress,
		LastStartedAt:   discoveryStatusState.lastStartedAt,
		LastCompletedAt: discoveryStatusState.lastCompletedAt,
		LastError:       discoveryStatusState.lastError,
	}
	switch {
	case discoveryStatusState.inProgress && discoveryStatusState.lastCompletedAt != "":
		snap.Status = "partial"
		snap.PartiallyReady = true
	case discoveryStatusState.inProgress:
		snap.Status = "discovering"
	case discoveryStatusState.lastCompletedAt != "":
		snap.Status = "ready"
	default:
		snap.Status = "idle"
	}
	return snap
}

// yaverDir returns ~/.yaver, creating it if necessary.
func yaverDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".yaver")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// projectsFilePath returns the full path to ~/.yaver/PROJECTS.md.
func projectsFilePath() (string, error) {
	dir, err := yaverDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectsFileName), nil
}

func isWSLHost() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

func projectDiscoveryRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}

	candidates := []string{
		home,
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Code"),
		filepath.Join(home, "src"),
		filepath.Join(home, "work"),
		filepath.Join(home, "dev"),
	}

	if isWSLHost() {
		user := filepath.Base(home)
		winHome := filepath.Join("/mnt/c/Users", user)
		candidates = append(candidates,
			winHome,
			filepath.Join(winHome, "Desktop"),
			filepath.Join(winHome, "source"),
			filepath.Join(winHome, "src"),
			filepath.Join(winHome, "Code"),
			filepath.Join(winHome, "Projects"),
			filepath.Join(winHome, "Workspace"),
		)
	}

	seen := map[string]bool{}
	var roots []string
	for _, root := range candidates {
		if root == "" || seen[root] {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// ensureProjectDiscovery checks if PROJECTS.md exists and is less than 24h old.
// If not, it runs discoverProjects() in a goroutine (non-blocking).
func ensureProjectDiscovery() {
	fp, err := projectsFilePath()
	if err != nil {
		log.Printf("[discovery] could not determine projects file path: %v", err)
		return
	}

	info, err := os.Stat(fp)
	if err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		// File exists and is fresh enough.
		return
	}

	go func() {
		log.Printf("[discovery] starting project discovery...")
		discoverProjects()
		log.Printf("[discovery] project discovery complete")
	}()
}

// getProjectContext reads ~/.yaver/PROJECTS.md and returns its contents.
// Returns empty string if the file does not exist or cannot be read.
func getProjectContext() string {
	fp, err := projectsFilePath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return ""
	}
	return string(data)
}

// discoverProjects scans the user's home directory for git repos,
// collects system info and available tools, and writes ~/.yaver/PROJECTS.md.
func discoverProjects() {
	markProjectDiscoveryStarted()
	var finishErr error
	defer func() {
		markProjectDiscoveryFinished(finishErr)
	}()

	var sb strings.Builder

	now := time.Now().UTC().Format(time.RFC3339)
	sb.WriteString("# Yaver Local Context\n")
	sb.WriteString("> This file is auto-generated by Yaver agent. It is stored locally only and NEVER uploaded to any server.\n")
	sb.WriteString(fmt.Sprintf("> Last updated: %s\n", now))
	sb.WriteString("\n")

	// --- System Info ---
	sb.WriteString("## System Info\n")
	writeSystemInfo(&sb)
	sb.WriteString("\n")

	// --- Available Tools ---
	sb.WriteString("## Available Tools\n")
	hasAgent := writeAvailableTools(&sb)
	sb.WriteString("\n")

	if !hasAgent {
		sb.WriteString("## Warning\n")
		sb.WriteString("No supported AI agent found (claude, codex, opencode). Install one to run tasks.\n")
		sb.WriteString("- Claude Code: https://docs.anthropic.com/en/docs/claude-code\n")
		sb.WriteString("- OpenAI Codex: https://github.com/openai/codex\n")
		sb.WriteString("- opencode: https://opencode.ai\n\n")
	}

	// --- Projects ---
	sb.WriteString("## Projects\n")
	writeProjects(&sb)

	// Write the file.
	fp, err := projectsFilePath()
	if err != nil {
		log.Printf("[discovery] could not determine file path: %v", err)
		finishErr = err
		return
	}
	if err := os.WriteFile(fp, []byte(sb.String()), 0600); err != nil {
		log.Printf("[discovery] could not write %s: %v", fp, err)
		finishErr = err
	}
}

// writeSystemInfo writes OS, hostname, CPU, RAM, home directory.
func writeSystemInfo(sb *strings.Builder) {
	home, _ := os.UserHomeDir()
	hostname, _ := os.Hostname()
	osName := runtime.GOOS
	arch := runtime.GOARCH
	cpuCount := runtime.NumCPU()

	// Friendly OS name
	friendlyOS := osName
	switch osName {
	case "darwin":
		friendlyOS = "macOS"
	case "linux":
		friendlyOS = "Linux"
	case "windows":
		friendlyOS = "Windows"
	}

	sb.WriteString(fmt.Sprintf("- OS: %s (%s/%s)\n", friendlyOS, osName, arch))
	sb.WriteString(fmt.Sprintf("- Hostname: %s\n", hostname))
	sb.WriteString(fmt.Sprintf("- CPU: %d cores\n", cpuCount))

	ramStr := getRAM()
	if ramStr != "" {
		sb.WriteString(fmt.Sprintf("- RAM: %s\n", ramStr))
	}

	sb.WriteString(fmt.Sprintf("- Home: %s\n", home))
}

// getRAM returns a human-readable RAM string.
func getRAM() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return ""
		}
		bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return ""
		}
		gb := bytes / (1024 * 1024 * 1024)
		return fmt.Sprintf("%d GB", gb)
	case "linux":
		f, err := os.Open("/proc/meminfo")
		if err != nil {
			return ""
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					kb, err := strconv.ParseUint(fields[1], 10, 64)
					if err == nil {
						gb := kb / (1024 * 1024)
						return fmt.Sprintf("%d GB", gb)
					}
				}
			}
		}
		return ""
	default:
		return ""
	}
}

// writeAvailableTools checks for known binaries and writes their paths/versions.
// Returns true if at least one AI agent (claude, codex, opencode) was found.
func writeAvailableTools(sb *strings.Builder) bool {
	aiAgents := map[string]bool{"claude": false, "codex": false, "opencode": false}
	tools := []struct {
		name       string
		versionCmd []string // command to get version, empty = no version check
	}{
		{"claude", []string{"claude", "--version"}},
		{"codex", nil},
		{"opencode", []string{"opencode", "--version"}},
		{"git", []string{"git", "--version"}},
		{"node", []string{"node", "--version"}},
		{"python", []string{"python3", "--version"}},
		{"go", []string{"go", "version"}},
		{"cargo", []string{"cargo", "--version"}},
		{"java", []string{"java", "-version"}},
		{"docker", []string{"docker", "--version"}},
		{"kubectl", []string{"kubectl", "version", "--client", "--short"}},
		{"terraform", []string{"terraform", "--version"}},
		{"brew", nil},
		{"npm", []string{"npm", "--version"}},
		{"yarn", []string{"yarn", "--version"}},
		{"pnpm", []string{"pnpm", "--version"}},
	}

	for _, t := range tools {
		binName := t.name
		// For python, look up python3 binary
		lookupName := binName
		if binName == "python" {
			lookupName = "python3"
		}

		path, err := exec.LookPath(lookupName)
		if err != nil {
			continue
		}

		// Track AI agents
		if _, isAgent := aiAgents[binName]; isAgent {
			aiAgents[binName] = true
		}

		version := ""
		if t.versionCmd != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			out, err := exec.CommandContext(ctx, t.versionCmd[0], t.versionCmd[1:]...).CombinedOutput()
			cancel()
			if err == nil {
				// Take first line only, trim whitespace.
				v := strings.TrimSpace(string(out))
				if idx := strings.IndexByte(v, '\n'); idx != -1 {
					v = v[:idx]
				}
				version = v
			}
		}

		if version != "" {
			sb.WriteString(fmt.Sprintf("- %s: %s (%s)\n", binName, path, version))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", binName, path))
		}
	}

	for _, found := range aiAgents {
		if found {
			return true
		}
	}
	return false
}

// writeProjects discovers git repos and writes project info.
func writeProjects(sb *strings.Builder) {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		sb.WriteString("_Could not determine home directory._\n")
		return
	}

	// Run find with 30s timeout — deeper scan to catch nested monorepo projects.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := append([]string{}, roots...)
	args = append(args,
		"-name", ".git", "-maxdepth", "6", "-type", "d",
		"-not", "-path", "*/node_modules/*",
		"-not", "-path", "*/.cache/*",
		"-not", "-path", "*/Library/*",
		"-not", "-path", "*/.local/*",
		"-not", "-path", "*/.cargo/*",
		"-not", "-path", "*/Pods/*",
		"-not", "-path", "*/.Trash/*",
		"-not", "-path", "*/AppData/*",
	)
	cmd := exec.CommandContext(ctx, "find", args...)
	out, err := cmd.Output()
	if err != nil {
		// find may return non-zero if some dirs are inaccessible — that's OK
		// as long as we got some output.
		if len(out) == 0 {
			sb.WriteString("_No projects found._\n")
			return
		}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var projects []projectInfo
	seenRepos := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// line is /path/to/project/.git — get parent
		repoDir := filepath.Dir(line)
		if seenRepos[repoDir] {
			continue
		}
		seenRepos[repoDir] = true
		info := gatherProjectInfo(repoDir)
		projects = append(projects, info)
	}

	// Sort by path for consistent output.
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Path < projects[j].Path
	})

	sb.WriteString("Only directories with `.git` are listed — these are the user's software projects.\n\n")

	for _, p := range projects {
		sb.WriteString(fmt.Sprintf("### %s\n", p.Path))
		sb.WriteString(fmt.Sprintf("- Branch: %s\n", p.Branch))
		sb.WriteString(fmt.Sprintf("- Last commit: \"%s\"\n", p.LastCommit))
		if len(p.Languages) > 0 {
			sb.WriteString(fmt.Sprintf("- Languages: %s\n", strings.Join(p.Languages, ", ")))
		}
		if p.ReadmePath != "" {
			sb.WriteString(fmt.Sprintf("- README: [%s](%s)\n", filepath.Base(p.ReadmePath), p.ReadmePath))
		}
		if p.Tree != "" {
			sb.WriteString("- Structure:\n```\n")
			sb.WriteString(p.Tree)
			sb.WriteString("\n```\n")
		}
		sb.WriteString("\n")
	}
}

type projectInfo struct {
	Path       string
	Branch     string
	LastCommit string
	Languages  []string
	Tree       string // limited directory tree
	ReadmePath string // path to copied README in ~/.yaver/projects/
}

func gatherProjectInfo(repoDir string) projectInfo {
	info := projectInfo{Path: repoDir}

	// Branch name
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		info.Branch = strings.TrimSpace(string(out))
	} else {
		info.Branch = "unknown"
	}

	// Last commit message
	out, err = exec.Command("git", "-C", repoDir, "log", "-1", "--pretty=%s").Output()
	if err == nil {
		info.LastCommit = strings.TrimSpace(string(out))
	} else {
		info.LastCommit = "unknown"
	}

	// Languages by file extension (quick scan of tracked files)
	out, err = exec.Command("git", "-C", repoDir, "ls-files").Output()
	if err == nil {
		info.Languages = detectLanguages(string(out))
	}

	// Directory tree (depth 2, exclude noise dirs, limit to 50 lines)
	info.Tree = getProjectTree(repoDir)

	// Copy README if exists
	info.ReadmePath = copyReadme(repoDir)

	return info
}

// copyReadme copies a project's README to ~/.yaver/projects/<safe-name>.md
// Returns the destination path, or empty string if no README found.
func copyReadme(repoDir string) string {
	// Look for common README filenames
	candidates := []string{"README.md", "Readme.md", "readme.md", "README.txt", "README", "README.rst"}
	var readmeSrc string
	for _, name := range candidates {
		p := filepath.Join(repoDir, name)
		if _, err := os.Stat(p); err == nil {
			readmeSrc = p
			break
		}
	}
	if readmeSrc == "" {
		return ""
	}

	// Also check for CLAUDE.md — copy that too if it exists
	claudeMdSrc := filepath.Join(repoDir, "CLAUDE.md")

	dir, err := yaverDir()
	if err != nil {
		return ""
	}
	projectsDir := filepath.Join(dir, "projects")
	if err := os.MkdirAll(projectsDir, 0700); err != nil {
		return ""
	}

	// Create a safe filename from the repo path
	safeName := strings.ReplaceAll(repoDir, "/", "_")
	safeName = strings.TrimPrefix(safeName, "_")

	// Copy README
	readmeDst := filepath.Join(projectsDir, safeName+"_README.md")
	data, err := os.ReadFile(readmeSrc)
	if err != nil {
		return ""
	}
	// Limit README to 10KB to avoid bloating context
	if len(data) > 10*1024 {
		data = append(data[:10*1024], []byte("\n\n... (truncated at 10KB)")...)
	}
	if err := os.WriteFile(readmeDst, data, 0600); err != nil {
		return ""
	}

	// Copy CLAUDE.md if exists
	if claudeData, err := os.ReadFile(claudeMdSrc); err == nil {
		claudeDst := filepath.Join(projectsDir, safeName+"_CLAUDE.md")
		if len(claudeData) > 10*1024 {
			claudeData = append(claudeData[:10*1024], []byte("\n\n... (truncated at 10KB)")...)
		}
		os.WriteFile(claudeDst, claudeData, 0600)
	}

	return readmeDst
}

// getProjectTree returns a limited directory tree for a project.
// Uses find with depth 2, excludes common noise directories, caps at 50 lines.
func getProjectTree(repoDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Use find instead of tree (tree may not be installed)
	// Exclude common noise directories
	// Use -prune to skip noisy directories entirely (faster than -not -path)
	cmd := exec.CommandContext(ctx, "find", repoDir,
		"-maxdepth", "2",
		"(", "-name", ".git", "-o", "-name", "node_modules", "-o", "-name", "vendor",
		"-o", "-name", "__pycache__", "-o", "-name", ".next", "-o", "-name", ".expo",
		"-o", "-name", ".cache", "-o", "-name", ".gradle", "-o", "-name", "Pods",
		"-o", "-name", ".dart_tool", "-o", "-name", ".DS_Store",
		")", "-prune", "-o",
		"(", "-type", "f", "-o", "-type", "d", ")", "-print",
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	// Make paths relative to repoDir
	var relative []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == repoDir {
			continue
		}
		rel, err := filepath.Rel(repoDir, line)
		if err != nil {
			continue
		}
		relative = append(relative, rel)
	}

	sort.Strings(relative)

	// Limit to 50 entries
	if len(relative) > 50 {
		relative = append(relative[:50], fmt.Sprintf("... and %d more", len(relative)-50))
	}

	if len(relative) == 0 {
		return ""
	}
	return strings.Join(relative, "\n")
}

// detectLanguages examines file extensions and returns a deduplicated list of languages.
func detectLanguages(lsFilesOutput string) []string {
	extMap := map[string]string{
		".go":    "Go",
		".js":    "JavaScript",
		".jsx":   "JavaScript",
		".ts":    "TypeScript",
		".tsx":   "TypeScript",
		".py":    "Python",
		".rb":    "Ruby",
		".rs":    "Rust",
		".java":  "Java",
		".kt":    "Kotlin",
		".swift": "Swift",
		".c":     "C",
		".cpp":   "C++",
		".h":     "C/C++",
		".cs":    "C#",
		".php":   "PHP",
		".sh":    "Shell",
		".bash":  "Shell",
		".zsh":   "Shell",
		".lua":   "Lua",
		".r":     "R",
		".scala": "Scala",
		".dart":  "Dart",
		".ex":    "Elixir",
		".exs":   "Elixir",
		".zig":   "Zig",
		".html":  "HTML",
		".css":   "CSS",
		".scss":  "SCSS",
		".sql":   "SQL",
	}

	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(lsFilesOutput))
	for scanner.Scan() {
		ext := strings.ToLower(filepath.Ext(scanner.Text()))
		if lang, ok := extMap[ext]; ok {
			seen[lang] = true
		}
	}

	var langs []string
	for lang := range seen {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return langs
}
