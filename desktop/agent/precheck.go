package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// CheckResult holds the outcome of a single pre-deployment check.
type CheckResult struct {
	Name     string
	Status   string // "pass", "fail", "warn", "skip"
	Duration time.Duration
	Message  string
	Details  string // multiline command output
}

// CheckReport is the full report returned by PreCheckManager.Run.
type CheckReport struct {
	Results     []CheckResult
	TotalChecks int
	Passed      int
	Failed      int
	Warnings    int
	Skipped     int
	Duration    time.Duration
	Ready       bool // true when no critical (fail) checks
}

// CheckConfig controls which checks run and whether auto-fix is attempted.
type CheckConfig struct {
	Skip    []string // check names to skip (case-insensitive)
	Fix     bool     // attempt auto-fix where supported
	WorkDir string   // override manager workDir for this run
}

// PreCheckManager runs pre-deployment checks for a project directory.
type PreCheckManager struct {
	mu      sync.Mutex
	workDir string
}

// NewPreCheckManager returns a manager rooted at workDir.
func NewPreCheckManager(workDir string) *PreCheckManager {
	return &PreCheckManager{workDir: workDir}
}

// ListChecks returns the canonical ordered list of check names.
func (m *PreCheckManager) ListChecks() []string {
	return []string{
		"git-clean",
		"typecheck",
		"lint",
		"format",
		"tests",
		"build",
		"bundle-size",
		"security-audit",
		"env-vars",
		"deps",
	}
}

// Run executes all checks in order and returns a full report.
func (m *PreCheckManager) Run(config *CheckConfig) (*CheckReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workDir := m.workDir
	if config != nil && config.WorkDir != "" {
		workDir = config.WorkDir
	}
	fix := config != nil && config.Fix

	skipSet := make(map[string]bool)
	if config != nil {
		for _, s := range config.Skip {
			skipSet[strings.ToLower(s)] = true
		}
	}

	type checkFn struct {
		name string
		fn   func(workDir string, fix bool) *CheckResult
	}

	checks := []checkFn{
		{"git-clean", func(wd string, _ bool) *CheckResult { return m.checkGitClean(wd) }},
		{"typecheck", func(wd string, _ bool) *CheckResult { return m.checkTypeCheck(wd) }},
		{"lint", func(wd string, f bool) *CheckResult { return m.checkLint(wd, f) }},
		{"format", func(wd string, f bool) *CheckResult { return m.checkFormat(wd, f) }},
		{"tests", func(wd string, _ bool) *CheckResult { return m.checkTests(wd) }},
		{"build", func(wd string, _ bool) *CheckResult { return m.checkBuild(wd) }},
		{"bundle-size", func(wd string, _ bool) *CheckResult { return m.checkBundleSize(wd) }},
		{"security-audit", func(wd string, _ bool) *CheckResult { return m.checkSecurityAudit(wd) }},
		{"env-vars", func(wd string, _ bool) *CheckResult { return m.checkEnvVars(wd) }},
		{"deps", func(wd string, _ bool) *CheckResult { return m.checkDeps(wd) }},
	}

	report := &CheckReport{}
	start := time.Now()

	for _, c := range checks {
		if skipSet[strings.ToLower(c.name)] {
			result := &CheckResult{
				Name:    c.name,
				Status:  "skip",
				Message: "skipped by config",
			}
			report.Results = append(report.Results, *result)
			report.TotalChecks++
			report.Skipped++
			continue
		}

		result := c.fn(workDir, fix)
		report.Results = append(report.Results, *result)
		report.TotalChecks++
		switch result.Status {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
		case "warn":
			report.Warnings++
		case "skip":
			report.Skipped++
		}
	}

	report.Duration = time.Since(start)
	report.Ready = report.Failed == 0
	return report, nil
}

// RunSingle runs exactly one check by name.
func (m *PreCheckManager) RunSingle(name string, fix bool) (*CheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workDir := m.workDir

	switch strings.ToLower(name) {
	case "git-clean":
		return m.checkGitClean(workDir), nil
	case "typecheck":
		return m.checkTypeCheck(workDir), nil
	case "lint":
		return m.checkLint(workDir, fix), nil
	case "format":
		return m.checkFormat(workDir, fix), nil
	case "tests":
		return m.checkTests(workDir), nil
	case "build":
		return m.checkBuild(workDir), nil
	case "bundle-size":
		return m.checkBundleSize(workDir), nil
	case "security-audit":
		return m.checkSecurityAudit(workDir), nil
	case "env-vars":
		return m.checkEnvVars(workDir), nil
	case "deps":
		return m.checkDeps(workDir), nil
	default:
		return nil, fmt.Errorf("unknown check: %q (run ListChecks() for available names)", name)
	}
}

// FormatReport renders the report as human-readable text.
func (m *PreCheckManager) FormatReport(report *CheckReport) string {
	const separator = "═══════════════════════════════════════════"

	var b strings.Builder

	b.WriteString("Pre-Deployment Check Report\n")
	b.WriteString(separator + "\n")

	// Column widths
	const nameWidth = 22
	const durWidth = 6

	for _, r := range report.Results {
		icon := statusIcon(r.Status)
		name := r.Name
		// Pad name to fixed width
		namePadded := name
		for utf8.RuneCountInString(namePadded) < nameWidth {
			namePadded += " "
		}
		dur := fmtDuration(r.Duration)
		durPadded := dur
		for len(durPadded) < durWidth {
			durPadded = " " + durPadded
		}
		b.WriteString(fmt.Sprintf("  %s %s  %s   %s\n", icon, namePadded, durPadded, r.Message))
	}

	b.WriteString(separator + "\n")

	// Summary line
	if report.Ready {
		b.WriteString(fmt.Sprintf("Result: ✓ READY TO DEPLOY (%d passed", report.Passed))
	} else {
		b.WriteString(fmt.Sprintf("Result: ✗ NOT READY (%d passed", report.Passed))
	}
	if report.Warnings > 0 {
		b.WriteString(fmt.Sprintf(", %d warnings", report.Warnings))
	}
	if report.Failed > 0 {
		b.WriteString(fmt.Sprintf(", %d failed", report.Failed))
	}
	if report.Skipped > 0 {
		b.WriteString(fmt.Sprintf(", %d skipped", report.Skipped))
	}
	b.WriteString(")\n")
	b.WriteString(fmt.Sprintf("Total time: %s\n", fmtDuration(report.Duration)))

	return b.String()
}

// ─── individual checks ───────────────────────────────────────────────────────

func (m *PreCheckManager) checkGitClean(workDir string) *CheckResult {
	start := time.Now()
	name := "git-clean"

	out, err := runCommandIn(workDir, "git", "status", "--porcelain")
	dur := time.Since(start)
	if err != nil {
		return &CheckResult{Name: name, Status: "fail", Duration: dur,
			Message: "git status failed", Details: out}
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "Working tree is clean"}
	}
	detail := strings.Join(lines, "\n")
	return &CheckResult{Name: name, Status: "fail", Duration: dur,
		Message: fmt.Sprintf("%d uncommitted change(s)", len(lines)),
		Details: detail}
}

func (m *PreCheckManager) checkTypeCheck(workDir string) *CheckResult {
	start := time.Now()
	name := "typecheck"

	pt := detectProjectType(workDir)
	dur := time.Since(start)

	switch pt {
	case "nodejs":
		// Only run tsc if tsconfig.json is present
		if !fileExists(filepath.Join(workDir, "tsconfig.json")) {
			return &CheckResult{Name: name, Status: "skip", Duration: dur,
				Message: "no tsconfig.json found"}
		}
		out, err := runCommandIn(workDir, "npx", "--no-install", "tsc", "--noEmit")
		dur2 := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur2,
				Message: "TypeScript errors found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur2,
			Message: "No type errors"}

	case "go":
		out, err := runCommandIn(workDir, "go", "vet", "./...")
		dur2 := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur2,
				Message: "go vet errors found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur2,
			Message: "No vet errors"}

	case "python":
		if _, err := exec.LookPath("mypy"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "mypy not installed"}
		}
		out, err := runCommandIn(workDir, "mypy", ".")
		dur2 := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur2,
				Message: "mypy errors found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur2,
			Message: "No type errors"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: dur,
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkLint(workDir string, fix bool) *CheckResult {
	start := time.Now()
	name := "lint"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		// Prefer Biome if configured, else ESLint
		if fileExists(filepath.Join(workDir, "biome.json")) || fileExists(filepath.Join(workDir, "biome.jsonc")) {
			args := []string{"--no-install", "biome", "check"}
			if fix {
				args = append(args, "--apply")
			}
			args = append(args, ".")
			out, err := runCommandIn(workDir, "npx", args...)
			dur := time.Since(start)
			if err != nil {
				return &CheckResult{Name: name, Status: "fail", Duration: dur,
					Message: "Biome lint errors", Details: out}
			}
			return &CheckResult{Name: name, Status: "pass", Duration: dur,
				Message: "No lint errors (Biome)"}
		}
		// ESLint
		eslintCfg := eslintConfigFile(workDir)
		if eslintCfg == "" {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "no ESLint or Biome config found"}
		}
		args := []string{"--no-install", "eslint", "."}
		if fix {
			args = append(args, "--fix")
		}
		out, err := runCommandIn(workDir, "npx", args...)
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "ESLint errors found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No lint errors (ESLint)"}

	case "go":
		if _, err := exec.LookPath("golangci-lint"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "golangci-lint not installed"}
		}
		out, err := runCommandIn(workDir, "golangci-lint", "run", "./...")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "golangci-lint errors found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No lint errors"}

	case "python":
		if _, err := exec.LookPath("ruff"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "ruff not installed"}
		}
		args := []string{"check", "."}
		if fix {
			args = append(args, "--fix")
		}
		out, err := runCommandIn(workDir, "ruff", args...)
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Ruff lint errors", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No lint errors (Ruff)"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkFormat(workDir string, fix bool) *CheckResult {
	start := time.Now()
	name := "format"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		// Check for Prettier config
		if !prettierConfigExists(workDir) {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "no Prettier config found"}
		}
		var out string
		var err error
		if fix {
			out, err = runCommandIn(workDir, "npx", "--no-install", "prettier", "--write", ".")
		} else {
			out, err = runCommandIn(workDir, "npx", "--no-install", "prettier", "--check", ".")
		}
		dur := time.Since(start)
		if err != nil {
			if fix {
				return &CheckResult{Name: name, Status: "fail", Duration: dur,
					Message: "Prettier auto-format failed", Details: out}
			}
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Files not formatted (Prettier)", Details: out}
		}
		if fix {
			return &CheckResult{Name: name, Status: "pass", Duration: dur,
				Message: "Files auto-formatted (Prettier)"}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "All files formatted (Prettier)"}

	case "go":
		// gofmt -l lists unformatted files
		out, err := runCommandIn(workDir, "gofmt", "-l", ".")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "gofmt check failed", Details: out}
		}
		lines := nonEmptyLines(out)
		if len(lines) == 0 {
			return &CheckResult{Name: name, Status: "pass", Duration: dur,
				Message: "All files formatted (gofmt)"}
		}
		if fix {
			_, _ = runCommandIn(workDir, "gofmt", "-w", ".")
			return &CheckResult{Name: name, Status: "pass", Duration: time.Since(start),
				Message: fmt.Sprintf("Auto-formatted %d file(s) (gofmt)", len(lines))}
		}
		return &CheckResult{Name: name, Status: "fail", Duration: dur,
			Message: fmt.Sprintf("%d file(s) not formatted", len(lines)),
			Details: strings.Join(lines, "\n")}

	case "python":
		if _, err := exec.LookPath("black"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "black not installed"}
		}
		var out string
		var err error
		if fix {
			out, err = runCommandIn(workDir, "black", ".")
		} else {
			out, err = runCommandIn(workDir, "black", "--check", ".")
		}
		dur := time.Since(start)
		if err != nil {
			if fix {
				return &CheckResult{Name: name, Status: "fail", Duration: dur,
					Message: "Black auto-format failed", Details: out}
			}
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Files not formatted (Black)", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "All files formatted (Black)"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkTests(workDir string) *CheckResult {
	start := time.Now()
	name := "tests"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		pm := detectPackageManager(workDir)
		out, err := runCommandIn(workDir, pm, "test", "--", "--passWithNoTests", "--watchAll=false")
		dur := time.Since(start)
		if err != nil {
			// Retry without Jest-specific flags for non-Jest runners
			out2, err2 := runCommandIn(workDir, pm, "test")
			if err2 != nil {
				return &CheckResult{Name: name, Status: "fail", Duration: time.Since(start),
					Message: "Tests failed", Details: out2}
			}
			_ = out
			return parseTestOutput(name, "nodejs", out2, time.Since(start))
		}
		return parseTestOutput(name, "nodejs", out, dur)

	case "go":
		out, err := runCommandIn(workDir, "go", "test", "./...")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Tests failed", Details: out}
		}
		return parseTestOutput(name, "go", out, dur)

	case "python":
		if _, err := exec.LookPath("pytest"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "pytest not installed"}
		}
		out, err := runCommandIn(workDir, "pytest", "--tb=short", "-q")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Tests failed", Details: out}
		}
		return parseTestOutput(name, "python", out, dur)

	case "rust":
		out, err := runCommandIn(workDir, "cargo", "test")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Tests failed", Details: out}
		}
		return parseTestOutput(name, "rust", out, dur)

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkBuild(workDir string) *CheckResult {
	start := time.Now()
	name := "build"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		pm := detectPackageManager(workDir)
		// Check if build script exists in package.json
		if !packageJSONHasScript(workDir, "build") {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "no 'build' script in package.json"}
		}
		out, err := runCommandIn(workDir, pm, "run", "build")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Build failed", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "Build successful"}

	case "go":
		out, err := runCommandIn(workDir, "go", "build", "./...")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Build failed", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "Build successful"}

	case "rust":
		out, err := runCommandIn(workDir, "cargo", "build", "--release")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "fail", Duration: dur,
				Message: "Build failed", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "Build successful (release)"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkBundleSize(workDir string) *CheckResult {
	start := time.Now()
	name := "bundle-size"

	// Only meaningful for JS/TS projects
	if detectProjectType(workDir) != "nodejs" {
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "not a Node.js project"}
	}

	// Check common output dirs
	candidates := []string{
		filepath.Join(workDir, ".next", "static"),
		filepath.Join(workDir, "dist"),
		filepath.Join(workDir, "build"),
		filepath.Join(workDir, "out"),
	}

	var found string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			found = c
			break
		}
	}
	if found == "" {
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no build output directory found (run build first)"}
	}

	size := dirSize(found)
	dur := time.Since(start)

	human := humanBytes(size)
	const warnThreshold = 5 * 1024 * 1024  // 5 MB
	const failThreshold = 20 * 1024 * 1024 // 20 MB

	switch {
	case size > failThreshold:
		return &CheckResult{Name: name, Status: "fail", Duration: dur,
			Message: fmt.Sprintf("%s (exceeds 20 MB limit)", human)}
	case size > warnThreshold:
		return &CheckResult{Name: name, Status: "warn", Duration: dur,
			Message: fmt.Sprintf("%s (over 5 MB warning threshold)", human)}
	default:
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: fmt.Sprintf("%s (under 5 MB limit)", human)}
	}
}

func (m *PreCheckManager) checkSecurityAudit(workDir string) *CheckResult {
	start := time.Now()
	name := "security-audit"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		pm := detectPackageManager(workDir)
		var out string
		var err error
		switch pm {
		case "yarn":
			out, err = runCommandIn(workDir, "yarn", "audit", "--level", "high")
		case "pnpm":
			out, err = runCommandIn(workDir, "pnpm", "audit", "--prod")
		default:
			out, err = runCommandIn(workDir, "npm", "audit", "--production")
		}
		dur := time.Since(start)
		if err != nil {
			// npm audit exits non-zero when vulnerabilities are found
			severity := auditSeverity(out)
			if severity == "high" || severity == "critical" {
				return &CheckResult{Name: name, Status: "fail", Duration: dur,
					Message: fmt.Sprintf("High/critical vulnerabilities found (%s)", pm+
						" audit)"), Details: out}
			}
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: "Moderate vulnerabilities found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No known vulnerabilities"}

	case "go":
		if _, err := exec.LookPath("govulncheck"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "govulncheck not installed (go install golang.org/x/vuln/cmd/govulncheck@latest)"}
		}
		out, err := runCommandIn(workDir, "govulncheck", "./...")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: "Vulnerabilities found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No known vulnerabilities"}

	case "python":
		if _, err := exec.LookPath("pip-audit"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "pip-audit not installed (pip install pip-audit)"}
		}
		out, err := runCommandIn(workDir, "pip-audit")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: "Vulnerabilities found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No known vulnerabilities"}

	case "rust":
		if _, err := exec.LookPath("cargo-audit"); err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
				Message: "cargo-audit not installed (cargo install cargo-audit)"}
		}
		out, err := runCommandIn(workDir, "cargo", "audit")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: "Vulnerabilities found", Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "No known vulnerabilities"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

func (m *PreCheckManager) checkEnvVars(workDir string) *CheckResult {
	start := time.Now()
	name := "env-vars"

	// Find example env file
	candidates := []string{
		filepath.Join(workDir, ".env.example"),
		filepath.Join(workDir, ".env.local.example"),
		filepath.Join(workDir, ".env.sample"),
	}
	var exampleFile string
	for _, c := range candidates {
		if fileExists(c) {
			exampleFile = c
			break
		}
	}
	if exampleFile == "" {
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no .env.example file found"}
	}

	// Find actual env file
	actualCandidates := []string{
		filepath.Join(workDir, ".env.local"),
		filepath.Join(workDir, ".env"),
	}
	var actualFile string
	for _, c := range actualCandidates {
		if fileExists(c) {
			actualFile = c
			break
		}
	}

	exampleKeys := parseEnvKeys(exampleFile)
	if len(exampleKeys) == 0 {
		return &CheckResult{Name: name, Status: "pass", Duration: time.Since(start),
			Message: "No env vars required"}
	}

	var actualKeys map[string]bool
	if actualFile != "" {
		actualKeys = parseEnvKeys(actualFile)
	} else {
		actualKeys = map[string]bool{}
	}

	// Also consider OS environment
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			actualKeys[parts[0]] = true
		}
	}

	var missing []string
	for k := range exampleKeys {
		if !actualKeys[k] {
			missing = append(missing, k)
		}
	}

	dur := time.Since(start)
	if len(missing) == 0 {
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: fmt.Sprintf("All %d env vars present", len(exampleKeys))}
	}
	return &CheckResult{Name: name, Status: "warn", Duration: dur,
		Message: fmt.Sprintf("Missing: %s", strings.Join(missing, ", ")),
		Details: strings.Join(missing, "\n")}
}

func (m *PreCheckManager) checkDeps(workDir string) *CheckResult {
	start := time.Now()
	name := "deps"

	pt := detectProjectType(workDir)

	switch pt {
	case "nodejs":
		pm := detectPackageManager(workDir)
		var out string
		var err error
		switch pm {
		case "yarn":
			out, err = runCommandIn(workDir, "yarn", "outdated")
		case "pnpm":
			out, err = runCommandIn(workDir, "pnpm", "outdated")
		default:
			out, err = runCommandIn(workDir, "npm", "outdated")
		}
		dur := time.Since(start)
		// npm outdated exits 1 when there are outdated packages
		lines := nonEmptyLines(out)
		if err != nil && len(lines) > 1 {
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: fmt.Sprintf("%d outdated package(s)", len(lines)-1), // subtract header
				Details: out}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "All up to date"}

	case "go":
		out, err := runCommandIn(workDir, "go", "list", "-m", "-u", "all")
		dur := time.Since(start)
		if err != nil {
			return &CheckResult{Name: name, Status: "skip", Duration: dur,
				Message: fmt.Sprintf("go list failed: %v", err)}
		}
		// Lines with "[v..." indicate available updates
		var outdated []string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "[v") {
				outdated = append(outdated, strings.TrimSpace(line))
			}
		}
		if len(outdated) > 0 {
			return &CheckResult{Name: name, Status: "warn", Duration: dur,
				Message: fmt.Sprintf("%d module(s) have updates", len(outdated)),
				Details: strings.Join(outdated, "\n")}
		}
		return &CheckResult{Name: name, Status: "pass", Duration: dur,
			Message: "All up to date"}

	default:
		return &CheckResult{Name: name, Status: "skip", Duration: time.Since(start),
			Message: "no supported project type detected"}
	}
}

// ─── internal helpers ────────────────────────────────────────────────────────

// detectProjectType returns "nodejs", "go", "python", "rust", or "unknown".
func detectProjectType(workDir string) string {
	if fileExists(filepath.Join(workDir, "package.json")) {
		return "nodejs"
	}
	if fileExists(filepath.Join(workDir, "go.mod")) {
		return "go"
	}
	if fileExists(filepath.Join(workDir, "Cargo.toml")) {
		return "rust"
	}
	if fileExists(filepath.Join(workDir, "setup.py")) ||
		fileExists(filepath.Join(workDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workDir, "requirements.txt")) {
		return "python"
	}
	return "unknown"
}

// runCommandIn runs a command in workDir and returns combined stdout+stderr output.
func runCommandIn(workDir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// nonEmptyLines splits s by newline and returns non-empty trimmed lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// fmtDuration formats a duration as "Xs" or "X.Xs".
func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// statusIcon maps a status string to an emoji indicator.
func statusIcon(status string) string {
	switch status {
	case "pass":
		return "✓"
	case "fail":
		return "✗"
	case "warn":
		return "!"
	case "skip":
		return "–"
	default:
		return "?"
	}
}

// eslintConfigFile returns the first ESLint config file found, or "".
func eslintConfigFile(workDir string) string {
	names := []string{
		".eslintrc", ".eslintrc.js", ".eslintrc.cjs",
		".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.json",
		"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
	}
	for _, n := range names {
		if fileExists(filepath.Join(workDir, n)) {
			return filepath.Join(workDir, n)
		}
	}
	return ""
}

// prettierConfigExists returns true if a Prettier config file is found.
func prettierConfigExists(workDir string) bool {
	names := []string{
		".prettierrc", ".prettierrc.js", ".prettierrc.cjs",
		".prettierrc.yaml", ".prettierrc.yml", ".prettierrc.json",
		"prettier.config.js", "prettier.config.cjs",
	}
	for _, n := range names {
		if fileExists(filepath.Join(workDir, n)) {
			return true
		}
	}
	// Check "prettier" key in package.json (naive check)
	pkgPath := filepath.Join(workDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		return bytes.Contains(data, []byte(`"prettier"`))
	}
	return false
}

// packageJSONHasScript returns true if the named script exists in package.json.
func packageJSONHasScript(workDir, script string) bool {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return false
	}
	// Naive but dependency-free check: look for `"build":` or `"build" :`
	return bytes.Contains(data, []byte(fmt.Sprintf(`"%s":`, script))) ||
		bytes.Contains(data, []byte(fmt.Sprintf(`"%s" :`, script)))
}

// parseEnvKeys reads an env file and returns a set of defined key names.
// Lines starting with # are comments; blank lines are ignored.
func parseEnvKeys(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	keys := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) >= 1 {
			key := strings.TrimSpace(parts[0])
			if key != "" {
				keys[key] = true
			}
		}
	}
	return keys
}

// auditSeverity scans npm audit output for the worst severity level mentioned.
func auditSeverity(out string) string {
	lower := strings.ToLower(out)
	if strings.Contains(lower, "critical") {
		return "critical"
	}
	if strings.Contains(lower, "high") {
		return "high"
	}
	if strings.Contains(lower, "moderate") {
		return "moderate"
	}
	if strings.Contains(lower, "low") {
		return "low"
	}
	return "unknown"
}

// parseTestOutput extracts a summary line from test runner output.
func parseTestOutput(name, projectType, out string, dur time.Duration) *CheckResult {
	// Try to find a summary line (heuristic for common test runners)
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		// Jest: "Tests: X passed"
		// Go: "ok  example/pkg"
		// pytest: "X passed"
		// cargo: "test result: ok."
		if strings.Contains(lower, "passed") ||
			strings.Contains(lower, "ok") ||
			strings.Contains(lower, "test result") {
			return &CheckResult{Name: name, Status: "pass", Duration: dur,
				Message: line}
		}
	}
	_ = projectType
	return &CheckResult{Name: name, Status: "pass", Duration: dur,
		Message: "Tests passed"}
}
