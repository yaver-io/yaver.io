package main

import (
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Docker tools
// ---------------------------------------------------------------------------

func mcpDockerPS() interface{} {
	out, err := runCmd("docker", "ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"containers": out}
}

func mcpDockerLogs(container string, tail int) interface{} {
	if tail <= 0 {
		tail = 100
	}
	out, err := runCmd("docker", "logs", "--tail", strconv.Itoa(tail), container)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"logs": out}
}

func mcpDockerExec(container, command string) interface{} {
	out, err := runCmd("docker", "exec", container, "sh", "-c", command)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"output": out}
}

func mcpDockerImages() interface{} {
	out, err := runCmd("docker", "images", "--format", "{{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"images": out}
}

func mcpDockerCompose(action, dir string) interface{} {
	args := []string{"compose"}
	switch action {
	case "up":
		args = append(args, "up", "-d")
	case "down":
		args = append(args, "down")
	case "ps":
		args = append(args, "ps", "--format", "json")
	case "logs":
		args = append(args, "logs", "--tail", "100")
	case "restart":
		args = append(args, "restart")
	default:
		return map[string]interface{}{"error": "unknown action: " + action + ". Use: up, down, ps, logs, restart"}
	}
	cmd := osexec.Command("docker", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("%s: %s", err, string(out))}
	}
	return map[string]interface{}{"output": string(out)}
}

// ---------------------------------------------------------------------------
// Test runner tools
// ---------------------------------------------------------------------------

func mcpRunTests(command, dir string) interface{} {
	if command == "" {
		command = detectTestCommand(dir)
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cmd := osexec.Command("sh", "-c", command)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	result := map[string]interface{}{
		"output":   string(out),
		"duration": duration.String(),
		"passed":   err == nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func detectTestCommand(dir string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go test ./... -v -count=1 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "node_modules/.bin/jest")); err == nil {
			return "npx jest --verbose 2>&1"
		}
		if _, err := os.Stat(filepath.Join(dir, "node_modules/.bin/vitest")); err == nil {
			return "npx vitest run 2>&1"
		}
		return "npm test 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "pytest.ini")); err == nil {
		return "pytest -v 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		return "pytest -v 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo test 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		return "make test 2>&1"
	}
	return "echo 'No test framework detected. Specify a command.'"
}

// ---------------------------------------------------------------------------
// HTTP client tool
// ---------------------------------------------------------------------------

func mcpHTTPRequest(method, url string, headers map[string]string, body string) interface{} {
	// SECURITY (audit 2026-07-13, A3): refuse cloud-metadata / link-local
	// targets and non-http(s) schemes before shelling out to curl.
	if err := guardOutboundHTTPURL(url); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	args := []string{"-s", "-w", "\n---HTTP_STATUS:%{http_code}---", "-X", method}
	for k, v := range headers {
		args = append(args, "-H", k+": "+v)
	}
	if body != "" {
		args = append(args, "-d", body)
	}
	args = append(args, url)

	out, err := runCmd("curl", args...)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	parts := strings.SplitN(out, "\n---HTTP_STATUS:", 2)
	result := map[string]interface{}{"body": parts[0]}
	if len(parts) > 1 {
		result["status_code"] = strings.TrimSuffix(parts[1], "---")
	}
	return result
}

// ---------------------------------------------------------------------------
// Log tail tool
// ---------------------------------------------------------------------------

func mcpTailLogs(path string, lines int) interface{} {
	if lines <= 0 {
		lines = 100
	}
	if path == "" {
		// Try common locations
		if runtime.GOOS == "darwin" {
			path = "/var/log/system.log"
		} else {
			// Use journalctl on Linux
			out, err := runCmd("journalctl", "--no-pager", "-n", strconv.Itoa(lines))
			if err != nil {
				return map[string]interface{}{"error": err.Error()}
			}
			return map[string]interface{}{"logs": out}
		}
	}
	out, err := runCmd("tail", "-n", strconv.Itoa(lines), path)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"logs": out, "path": path}
}

// ---------------------------------------------------------------------------
// Clipboard tools
// ---------------------------------------------------------------------------

func mcpClipboardRead() interface{} {
	var out string
	var err error
	switch runtime.GOOS {
	case "darwin":
		out, err = runCmd("pbpaste")
	case "linux":
		out, err = runCmd("xclip", "-selection", "clipboard", "-o")
		if err != nil {
			out, err = runCmd("xsel", "--clipboard", "--output")
		}
	case "windows":
		out, err = runCmd("powershell", "-command", "Get-Clipboard")
	default:
		return map[string]interface{}{"error": "unsupported OS"}
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"content": out}
}

func mcpClipboardWrite(content string) interface{} {
	var cmd *osexec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = osexec.Command("pbcopy")
	case "linux":
		cmd = osexec.Command("xclip", "-selection", "clipboard")
	case "windows":
		cmd = osexec.Command("powershell", "-command", "Set-Clipboard", "-Value", content)
	default:
		return map[string]interface{}{"error": "unsupported OS"}
	}
	if runtime.GOOS != "windows" {
		cmd.Stdin = strings.NewReader(content)
	}
	if err := cmd.Run(); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "length": len(content)}
}

// ---------------------------------------------------------------------------
// Process management tools
// ---------------------------------------------------------------------------

func mcpProcessList(filter string) interface{} {
	var out string
	var err error
	if runtime.GOOS == "windows" {
		out, err = runCmd("tasklist", "/FO", "CSV")
	} else {
		if filter != "" {
			out, err = runCmd("sh", "-c", fmt.Sprintf("ps aux | head -1; ps aux | grep -i '%s' | grep -v grep", filter))
		} else {
			out, err = runCmd("ps", "aux", "--sort=-%mem")
		}
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"processes": out}
}

func mcpProcessKill(pid int, signal string) interface{} {
	if signal == "" {
		signal = "TERM"
	}
	var err error
	if runtime.GOOS == "windows" {
		_, err = runCmd("taskkill", "/PID", strconv.Itoa(pid), "/F")
	} else {
		_, err = runCmd("kill", "-"+signal, strconv.Itoa(pid))
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "pid": pid, "signal": signal}
}

func mcpPortCheck(port int) interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("lsof", "-i", fmt.Sprintf(":%d", port), "-P", "-n")
	} else if runtime.GOOS == "windows" {
		out, err = runCmd("netstat", "-ano", "|", "findstr", fmt.Sprintf(":%d", port))
	} else {
		out, err = runCmd("ss", "-tlnp", fmt.Sprintf("sport = :%d", port))
	}
	if err != nil {
		return map[string]interface{}{"port": port, "in_use": false}
	}
	return map[string]interface{}{"port": port, "in_use": true, "details": out}
}

// ---------------------------------------------------------------------------
// Code quality tools
// ---------------------------------------------------------------------------

func mcpLint(dir, tool string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if tool == "" {
		tool = detectLintTool(dir)
	}
	cmd := osexec.Command("sh", "-c", tool)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := map[string]interface{}{
		"output": string(out),
		"clean":  err == nil,
		"tool":   tool,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func detectLintTool(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go vet ./... 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, ".eslintrc.json")); err == nil {
		return "npx eslint . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, ".eslintrc.js")); err == nil {
		return "npx eslint . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "eslint.config.js")); err == nil {
		return "npx eslint . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "eslint.config.mjs")); err == nil {
		return "npx eslint . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		return "ruff check . 2>&1 || python -m flake8 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo clippy 2>&1"
	}
	return "echo 'No linter detected. Specify a tool.'"
}

func mcpFormat(dir, tool string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if tool == "" {
		tool = detectFormatTool(dir)
	}
	cmd := osexec.Command("sh", "-c", tool)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := map[string]interface{}{
		"output": string(out),
		"tool":   tool,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func detectFormatTool(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "gofmt -w . 2>&1 && echo 'Formatted.'"
	}
	if _, err := os.Stat(filepath.Join(dir, ".prettierrc")); err == nil {
		return "npx prettier --write . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "prettier.config.js")); err == nil {
		return "npx prettier --write . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		return "ruff format . 2>&1 || python -m black . 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo fmt 2>&1"
	}
	return "echo 'No formatter detected. Specify a tool.'"
}

func mcpTypeCheck(dir, tool string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if tool == "" {
		tool = detectTypeCheckTool(dir)
	}
	cmd := osexec.Command("sh", "-c", tool)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := map[string]interface{}{
		"output": string(out),
		"clean":  err == nil,
		"tool":   tool,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func detectTypeCheckTool(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil {
		return "npx tsc --noEmit 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go build ./... 2>&1"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		return "mypy . 2>&1 || pyright . 2>&1"
	}
	return "echo 'No type checker detected. Specify a tool.'"
}

// ---------------------------------------------------------------------------
// Package dependency tools
// ---------------------------------------------------------------------------

func mcpDepsOutdated(dir, manager string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if manager == "" {
		manager = detectPackageManager(dir)
	}
	var cmdStr string
	switch manager {
	case "npm":
		cmdStr = "npm outdated --json 2>/dev/null || npm outdated 2>&1"
	case "yarn":
		cmdStr = "yarn outdated --json 2>/dev/null || yarn outdated 2>&1"
	case "pnpm":
		cmdStr = "pnpm outdated --json 2>/dev/null || pnpm outdated 2>&1"
	case "pip":
		cmdStr = "pip list --outdated --format json 2>&1"
	case "cargo":
		cmdStr = "cargo outdated 2>&1 || echo 'Install cargo-outdated: cargo install cargo-outdated'"
	case "go":
		cmdStr = "go list -u -m all 2>&1"
	default:
		return map[string]interface{}{"error": "unknown manager: " + manager}
	}
	cmd := osexec.Command("sh", "-c", cmdStr)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return map[string]interface{}{"outdated": string(out), "manager": manager}
}

func mcpDepsAudit(dir, manager string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if manager == "" {
		manager = detectPackageManager(dir)
	}
	var cmdStr string
	switch manager {
	case "npm":
		cmdStr = "npm audit --json 2>&1"
	case "yarn":
		cmdStr = "yarn audit --json 2>&1"
	case "pnpm":
		cmdStr = "pnpm audit --json 2>&1"
	case "pip":
		cmdStr = "pip-audit --format json 2>&1 || pip check 2>&1"
	case "cargo":
		cmdStr = "cargo audit 2>&1 || echo 'Install cargo-audit: cargo install cargo-audit'"
	case "go":
		cmdStr = "govulncheck ./... 2>&1 || echo 'Install govulncheck: go install golang.org/x/vuln/cmd/govulncheck@latest'"
	default:
		return map[string]interface{}{"error": "unknown manager: " + manager}
	}
	cmd := osexec.Command("sh", "-c", cmdStr)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return map[string]interface{}{"audit": string(out), "manager": manager}
}

func mcpDepsList(dir, manager string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if manager == "" {
		manager = detectPackageManager(dir)
	}
	var cmdStr string
	switch manager {
	case "npm":
		cmdStr = "npm ls --depth=0 --json 2>&1"
	case "yarn":
		cmdStr = "yarn list --depth=0 --json 2>&1"
	case "pnpm":
		cmdStr = "pnpm ls --depth=0 --json 2>&1"
	case "pip":
		cmdStr = "pip list --format json 2>&1"
	case "cargo":
		cmdStr = "cargo tree --depth 1 2>&1"
	case "go":
		cmdStr = "go list -m all 2>&1"
	default:
		return map[string]interface{}{"error": "unknown manager: " + manager}
	}
	cmd := osexec.Command("sh", "-c", cmdStr)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return map[string]interface{}{"dependencies": string(out), "manager": manager}
}

func detectPackageManager(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil {
		return "npm"
	}
	if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
		return "yarn"
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "npm"
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo"
	}
	if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); err == nil {
		return "pip"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		return "pip"
	}
	return "npm"
}

// ---------------------------------------------------------------------------
// GitHub tools
// ---------------------------------------------------------------------------

func mcpGitHubPRs(dir, state string) interface{} {
	if state == "" {
		state = "open"
	}
	args := []string{"pr", "list", "--state", state, "--json", "number,title,author,state,url,createdAt", "--limit", "20"}
	cmd := osexec.Command("gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gh cli error: %s (install: brew install gh)", string(out))}
	}
	var prs []interface{}
	json.Unmarshal(out, &prs)
	return map[string]interface{}{"pull_requests": prs}
}

func mcpGitHubIssues(dir, state string) interface{} {
	if state == "" {
		state = "open"
	}
	args := []string{"issue", "list", "--state", state, "--json", "number,title,author,state,url,labels,createdAt", "--limit", "20"}
	cmd := osexec.Command("gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gh cli error: %s", string(out))}
	}
	var issues []interface{}
	json.Unmarshal(out, &issues)
	return map[string]interface{}{"issues": issues}
}

func mcpGitHubCIStatus(dir string) interface{} {
	cmd := osexec.Command("gh", "run", "list", "--json", "databaseId,displayTitle,status,conclusion,headBranch,createdAt", "--limit", "10")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("gh cli error: %s", string(out))}
	}
	var runs []interface{}
	json.Unmarshal(out, &runs)
	return map[string]interface{}{"workflow_runs": runs}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func runCmd(name string, args ...string) (string, error) {
	cmd := osexec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
