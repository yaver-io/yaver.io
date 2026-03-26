package main

import (
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// QualityCheckType represents the type of quality check.
type QualityCheckType string

const (
	QualityTest      QualityCheckType = "test"
	QualityLint      QualityCheckType = "lint"
	QualityTypeCheck QualityCheckType = "typecheck"
	QualityFormat    QualityCheckType = "format"
)

// QualityCheck describes an available quality check for a project.
type QualityCheck struct {
	Type      QualityCheckType `json:"type"`
	Available bool             `json:"available"`
	Command   string           `json:"command"`
	Framework string           `json:"framework"`
}

// QualityResult represents the outcome of a quality check run.
type QualityResult struct {
	ID         string           `json:"id"`
	Type       QualityCheckType `json:"type"`
	Command    string           `json:"command"`
	Status     string           `json:"status"` // "running", "passed", "failed"
	ExecID     string           `json:"execId,omitempty"`
	Output     string           `json:"output,omitempty"`
	ExitCode   *int             `json:"exitCode,omitempty"`
	Issues     int              `json:"issues"`
	StartedAt  string           `json:"startedAt"`
	FinishedAt string           `json:"finishedAt,omitempty"`
	Duration   int64            `json:"duration,omitempty"` // milliseconds
}

// QualityManager manages quality check runs.
type QualityManager struct {
	mu        sync.RWMutex
	results   map[string]*QualityResult
	execMgr   *ExecManager
	workDir   string
	notifyMgr *NotificationManager
}

// NewQualityManager creates a new quality manager.
func NewQualityManager(execMgr *ExecManager, workDir string) *QualityManager {
	return &QualityManager{
		results: make(map[string]*QualityResult),
		execMgr: execMgr,
		workDir: workDir,
	}
}

// DetectQualityChecks returns all available quality checks for a project.
func DetectQualityChecks(workDir string) []QualityCheck {
	checks := make([]QualityCheck, 0)

	hasFile := func(name string) bool {
		_, err := os.Stat(filepath.Join(workDir, name))
		return err == nil
	}

	hasPackageJSONDep := func(dep string) bool {
		data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
		if err != nil {
			return false
		}
		return strings.Contains(string(data), "\""+dep+"\"")
	}

	// --- Lint detection ---
	if hasFile("package.json") {
		if hasPackageJSONDep("eslint") {
			checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: "npx eslint . --max-warnings=0", Framework: "eslint"})
		} else if hasPackageJSONDep("biome") || hasPackageJSONDep("@biomejs/biome") {
			checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: "npx biome check .", Framework: "biome"})
		}
	}
	if hasFile("go.mod") {
		cmd := "go vet ./..."
		framework := "go_vet"
		if _, err := osexec.LookPath("golangci-lint"); err == nil {
			cmd = "golangci-lint run ./..."
			framework = "golangci-lint"
		}
		checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: cmd, Framework: framework})
	}
	if hasFile("pyproject.toml") {
		cmd := "python -m flake8 ."
		framework := "flake8"
		if _, err := osexec.LookPath("ruff"); err == nil {
			cmd = "ruff check ."
			framework = "ruff"
		}
		checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: cmd, Framework: framework})
	}
	if hasFile("Cargo.toml") {
		checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: "cargo clippy -- -D warnings", Framework: "clippy"})
	}
	if hasFile("pubspec.yaml") {
		checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: "flutter analyze", Framework: "flutter_analyze"})
	}
	if hasFile(".swiftlint.yml") {
		checks = append(checks, QualityCheck{Type: QualityLint, Available: true, Command: "swiftlint lint", Framework: "swiftlint"})
	}

	// --- Typecheck detection ---
	if hasFile("tsconfig.json") {
		checks = append(checks, QualityCheck{Type: QualityTypeCheck, Available: true, Command: "npx tsc --noEmit", Framework: "typescript"})
	}
	if hasFile("pyproject.toml") {
		data, _ := os.ReadFile(filepath.Join(workDir, "pyproject.toml"))
		if strings.Contains(string(data), "mypy") {
			checks = append(checks, QualityCheck{Type: QualityTypeCheck, Available: true, Command: "mypy .", Framework: "mypy"})
		}
	}
	if hasFile("go.mod") {
		checks = append(checks, QualityCheck{Type: QualityTypeCheck, Available: true, Command: "go build ./...", Framework: "go_build"})
	}

	// --- Format detection ---
	if hasFile("package.json") && hasPackageJSONDep("prettier") {
		checks = append(checks, QualityCheck{Type: QualityFormat, Available: true, Command: "npx prettier --check .", Framework: "prettier"})
	}
	if hasFile("go.mod") {
		checks = append(checks, QualityCheck{Type: QualityFormat, Available: true, Command: "gofmt -l .", Framework: "gofmt"})
	}
	if hasFile("Cargo.toml") {
		checks = append(checks, QualityCheck{Type: QualityFormat, Available: true, Command: "cargo fmt --check", Framework: "cargo_fmt"})
	}
	if hasFile("pubspec.yaml") {
		checks = append(checks, QualityCheck{Type: QualityFormat, Available: true, Command: "dart format --set-exit-if-changed .", Framework: "dart_format"})
	}
	if hasFile(".swift-format") || hasSwiftFiles(workDir) {
		if _, err := osexec.LookPath("swift-format"); err == nil {
			checks = append(checks, QualityCheck{Type: QualityFormat, Available: true, Command: "swift-format lint -r .", Framework: "swift_format"})
		}
	}

	// --- Test detection (delegates to existing DetectTestFramework) ---
	framework, command, _ := DetectTestFramework(workDir)
	if framework != "" {
		checks = append(checks, QualityCheck{Type: QualityTest, Available: true, Command: command, Framework: framework})
	}

	return checks
}

// RunQualityCheck starts a single quality check via ExecManager.
func (qm *QualityManager) RunQualityCheck(checkType QualityCheckType, workDir string) (*QualityResult, error) {
	if workDir == "" {
		workDir = qm.workDir
	}

	// Find the matching check
	checks := DetectQualityChecks(workDir)
	var matched *QualityCheck
	for i := range checks {
		if checks[i].Type == checkType {
			matched = &checks[i]
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("no %s check available for %s", checkType, workDir)
	}

	// Timeout: 5 min for lint/typecheck/format, 30 min for tests
	timeoutSec := 300
	if checkType == QualityTest {
		timeoutSec = 1800
	}

	session, err := qm.execMgr.StartExec(matched.Command, workDir, "", nil, timeoutSec)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", checkType, err)
	}

	result := &QualityResult{
		ID:        uuid.New().String()[:8],
		Type:      checkType,
		Command:   matched.Command,
		Status:    "running",
		ExecID:    session.ID,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}

	qm.mu.Lock()
	qm.results[result.ID] = result
	qm.mu.Unlock()

	// Monitor completion
	go qm.monitorCheck(result, session)

	return result, nil
}

// RunAllQualityChecks runs all available quality checks (lint + typecheck + format) concurrently.
func (qm *QualityManager) RunAllQualityChecks(workDir string) ([]*QualityResult, error) {
	if workDir == "" {
		workDir = qm.workDir
	}

	checks := DetectQualityChecks(workDir)
	if len(checks) == 0 {
		return nil, fmt.Errorf("no quality checks available for %s", workDir)
	}

	var results []*QualityResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, check := range checks {
		wg.Add(1)
		go func(c QualityCheck) {
			defer wg.Done()
			r, err := qm.RunQualityCheck(c.Type, workDir)
			if err != nil {
				log.Printf("[quality] Failed to start %s: %v", c.Type, err)
				return
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(check)
	}

	wg.Wait()
	return results, nil
}

func (qm *QualityManager) monitorCheck(result *QualityResult, session *ExecSession) {
	<-session.doneCh

	session.mu.RLock()
	exitCode := session.ExitCode
	stdout := session.stdout.String()
	session.mu.RUnlock()

	qm.mu.Lock()
	defer qm.mu.Unlock()

	now := time.Now().UTC()
	result.FinishedAt = now.Format(time.RFC3339)

	startTime, _ := time.Parse(time.RFC3339, result.StartedAt)
	result.Duration = now.Sub(startTime).Milliseconds()

	result.Output = stdout
	result.ExitCode = exitCode

	if exitCode != nil && *exitCode == 0 {
		result.Status = "passed"
		result.Issues = 0
	} else {
		result.Issues = countQualityIssues(stdout)
		// Warning: lint/format with few issues (1-5) is a warning, not a hard failure
		if (result.Type == QualityLint || result.Type == QualityFormat) && result.Issues > 0 && result.Issues <= 5 {
			result.Status = "warning"
		} else {
			result.Status = "failed"
		}
	}

	log.Printf("[quality] %s %s finished: %s (%d issues)", result.Type, result.ID, result.Status, result.Issues)

	// Notify on failure or warning
	if qm.notifyMgr != nil && (result.Status == "failed" || result.Status == "warning") {
		qm.notifyMgr.NotifyQualityCheck(string(result.Type), result.Status, result.Issues)
	}
}

// GetResult returns a quality result by ID.
func (qm *QualityManager) GetResult(id string) (*QualityResult, bool) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	r, ok := qm.results[id]
	return r, ok
}

// ListResults returns all quality results.
func (qm *QualityManager) ListResults() []*QualityResult {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	results := make([]*QualityResult, 0, len(qm.results))
	for _, r := range qm.results {
		results = append(results, r)
	}
	return results
}

// countQualityIssues counts approximate number of issues from lint/typecheck output.
func countQualityIssues(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "error") || strings.Contains(line, "Error") ||
			strings.Contains(line, "warning") || strings.Contains(line, "Warning") {
			count++
		}
	}
	if count == 0 && output != "" {
		count = 1
	}
	return count
}

// hasSwiftFiles checks if the directory contains any .swift files.
func hasSwiftFiles(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	if len(matches) > 0 {
		return true
	}
	matches, _ = filepath.Glob(filepath.Join(dir, "Sources", "*.swift"))
	return len(matches) > 0
}
