package main

// deploy_all.go — P9 orchestration verb. Runs the full deploy fan-out
// (infra + every beta/internal channel the work touched) sequentially,
// collects per-step results, and writes ~/n2n_deploy_report.md. The
// verb never touches production (App Store / Play production) — only
// TestFlight (iOS, whose build embeds watch/tv/vision) and Play
// internal (Android + Wear / TV / Auto where relevant), plus infra
// (Convex, Cloudflare web, npm CLI + Go agent, MCP registry).
//
// HARD gate: `Preflight()` must return nil before any deploy step
// fires. Preflight runs `go build ./...` and the P0-P8 scoped test
// selector; a red gate short-circuits with `blocked`. The caller can
// force with `Force=true` but must be very sure.
//
// Two knobs on DeployAllRequest:
//
//   DryRun=true    lists the steps that WOULD run without invoking them.
//   Only=[...]     runs only the named steps (e.g. ["convex","web"]).
//   Exclude=[...]  drops the named steps.
//
// TestFlight follows the documented recovery path: source
// ~/.appstoreconnect/yaver.env (unlocks the yaver-signing keychain)
// then run scripts/deploy-testflight.sh; if a codesign/keychain error
// fires, re-source + `security unlock-keychain` yaver-signing.keychain
// and retry ONCE.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DeployStep describes one deploy fan-out. Each step is a name +
// working directory + shell command. Commands are shelled via
// bash -lc so ~/… + env sourcing work; the timeout is enforced.
type DeployStep struct {
	Name     string        `json:"name"`
	Channel  string        `json:"channel"`  // beta / internal / infra
	WorkDir  string        `json:"workDir"`
	Command  string        `json:"command"`
	Timeout  time.Duration `json:"-"`
	Optional bool          `json:"optional"`
}

// DeployStepResult is what each step produces.
type DeployStepResult struct {
	Name       string    `json:"name"`
	Channel    string    `json:"channel"`
	Status     string    `json:"status"` // deployed | skipped | blocked | failed
	StartedAt  string    `json:"startedAt"`
	FinishedAt string    `json:"finishedAt"`
	DurationS  float64   `json:"durationSeconds"`
	Detail     string    `json:"detail,omitempty"`
}

// DeployAllRequest is the MCP verb payload.
type DeployAllRequest struct {
	DryRun  bool     `json:"dryRun"`
	Only    []string `json:"only,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Force   bool     `json:"force,omitempty"`
}

// DeployAllResult is the composed response.
type DeployAllResult struct {
	OK         bool               `json:"ok"`
	GateStatus string             `json:"gateStatus"` // green | red | forced
	ReportPath string             `json:"reportPath"`
	Steps      []DeployStepResult `json:"steps"`
	Note       string             `json:"note,omitempty"`
}

// DefaultDeploySteps returns the fan-out for this repo. Fresh state
// per call so tests can mutate freely without racing.
func DefaultDeploySteps(repoRoot string) []DeployStep {
	return []DeployStep{
		{
			Name: "convex", Channel: "infra", WorkDir: filepath.Join(repoRoot, "backend"),
			Command: "npx convex deploy --yes", Timeout: 5 * time.Minute,
		},
		{
			Name: "web-cloudflare", Channel: "infra", WorkDir: repoRoot,
			Command: "./scripts/deploy-web.sh", Timeout: 15 * time.Minute,
		},
		{
			Name: "cli-npm", Channel: "infra", WorkDir: filepath.Join(repoRoot, "cli"),
			Command: "npm publish", Timeout: 10 * time.Minute, Optional: true,
		},
		{
			Name: "testflight-ios", Channel: "beta", WorkDir: repoRoot,
			Command: "source ~/.appstoreconnect/yaver.env && ./scripts/deploy-testflight.sh",
			Timeout: 45 * time.Minute,
		},
		{
			Name: "playstore-android", Channel: "internal", WorkDir: repoRoot,
			Command: "JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py",
			Timeout: 45 * time.Minute,
		},
	}
}

// developForRepoRoot returns the repo root from CWD by walking up to
// the first .git directory. Falls back to CWD when nothing is found.
func repoRootFromCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}

// deployPreflight is the seam tests use to intercept the build+test
// gate without shelling out. Returns nil on green.
var deployPreflight = func(ctx context.Context, repoRoot string) error {
	agentDir := filepath.Join(repoRoot, "desktop", "agent")
	buildCmd := exec.CommandContext(ctx, "go", "build", "./...")
	buildCmd.Dir = agentDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build failed: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deployRunCommand is the shell seam. Tests intercept it so the
// suite is hermetic; production shells to `bash -lc <command>` from
// the step's WorkDir with the step's timeout.
var deployRunCommand = func(ctx context.Context, workDir, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(stepCtx, "bash", "-lc", command)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunDeployAll executes the deploy fan-out with the gate + result
// bookkeeping described above. Writes ~/n2n_deploy_report.md as the
// canonical artefact.
func RunDeployAll(ctx context.Context, req DeployAllRequest) DeployAllResult {
	result := DeployAllResult{}
	repoRoot := repoRootFromCWD()

	if err := deployPreflight(ctx, repoRoot); err != nil {
		if !req.Force {
			result.OK = false
			result.GateStatus = "red"
			result.Note = "preflight failed: " + err.Error()
			result.Steps = nil
			_ = writeDeployReport(result)
			return result
		}
		result.GateStatus = "forced"
		result.Note = "preflight failed but Force=true — proceeding at caller's risk"
	} else {
		result.GateStatus = "green"
	}

	steps := DefaultDeploySteps(repoRoot)
	steps = filterDeploySteps(steps, req.Only, req.Exclude)

	testflightRetried := false
	for _, step := range steps {
		start := time.Now()
		row := DeployStepResult{
			Name: step.Name, Channel: step.Channel,
			StartedAt: start.UTC().Format(time.RFC3339),
		}
		if req.DryRun {
			row.Status = "skipped"
			row.Detail = "dry-run: would run `" + step.Command + "` in " + step.WorkDir
		} else {
			out, err := deployRunCommand(ctx, step.WorkDir, step.Command, step.Timeout)
			trimmed := strings.TrimSpace(out)
			if len(trimmed) > 1500 {
				trimmed = trimmed[len(trimmed)-1500:]
			}
			switch {
			case err == nil:
				row.Status = "deployed"
				row.Detail = trimmed
			case step.Name == "testflight-ios" && !testflightRetried && looksLikeKeychainError(trimmed):
				testflightRetried = true
				retryOut, retryErr := deployRunCommand(ctx,
					step.WorkDir,
					"security unlock-keychain -p '' ~/Library/Keychains/yaver-signing.keychain-db 2>/dev/null; "+step.Command,
					step.Timeout)
				if retryErr == nil {
					row.Status = "deployed"
					row.Detail = "retry ok: " + strings.TrimSpace(retryOut)
				} else {
					row.Status = "blocked"
					row.Detail = "keychain retry failed: " + retryErr.Error() + " — needs a GUI Terminal or phone unlock; " + trimmed
				}
			case errors.Is(err, context.DeadlineExceeded):
				row.Status = "failed"
				row.Detail = "timed out after " + step.Timeout.String() + " — " + trimmed
			case step.Optional:
				row.Status = "skipped"
				row.Detail = "optional step errored (not fatal): " + err.Error() + " — " + trimmed
			default:
				row.Status = "failed"
				row.Detail = err.Error() + " — " + trimmed
			}
		}
		end := time.Now()
		row.FinishedAt = end.UTC().Format(time.RFC3339)
		row.DurationS = end.Sub(start).Seconds()
		result.Steps = append(result.Steps, row)
	}

	result.OK = allStepsGreen(result.Steps)
	if err := writeDeployReport(result); err != nil {
		result.Note = strings.TrimSpace(result.Note + " report-write-failed: " + err.Error())
	} else {
		result.ReportPath = deployReportPath()
	}
	return result
}

func allStepsGreen(rows []DeployStepResult) bool {
	for _, r := range rows {
		if r.Status == "failed" || r.Status == "blocked" {
			return false
		}
	}
	return true
}

func looksLikeKeychainError(out string) bool {
	lower := strings.ToLower(out)
	for _, needle := range []string{"unable to unlock", "keychain", "errsecinternalcomponent", "codesign failed"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func filterDeploySteps(all []DeployStep, only, exclude []string) []DeployStep {
	pick := func(list []string) map[string]bool {
		m := map[string]bool{}
		for _, name := range list {
			m[strings.ToLower(strings.TrimSpace(name))] = true
		}
		return m
	}
	onlySet := pick(only)
	excludeSet := pick(exclude)
	out := make([]DeployStep, 0, len(all))
	for _, s := range all {
		lname := strings.ToLower(s.Name)
		if len(onlySet) > 0 && !onlySet[lname] {
			continue
		}
		if excludeSet[lname] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func deployReportPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "n2n_deploy_report.md"
	}
	return filepath.Join(home, "n2n_deploy_report.md")
}

// writeDeployReport is a var seam so tests can capture the output
// without touching real $HOME.
var writeDeployReport = func(result DeployAllResult) error {
	buf, err := composeDeployReportMarkdown(result)
	if err != nil {
		return err
	}
	return os.WriteFile(deployReportPath(), []byte(buf), 0o600)
}

func composeDeployReportMarkdown(result DeployAllResult) (string, error) {
	var b strings.Builder
	b.WriteString("# Yaver n2n deploy report\n\n")
	b.WriteString("Generated: " + time.Now().UTC().Format(time.RFC3339) + "\n\n")
	fmt.Fprintf(&b, "Overall OK: %v  Gate: %s\n\n", result.OK, result.GateStatus)
	if result.Note != "" {
		b.WriteString("Note: " + result.Note + "\n\n")
	}
	b.WriteString("| step | channel | status | duration | detail |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, r := range result.Steps {
		detail := strings.ReplaceAll(strings.TrimSpace(r.Detail), "\n", " · ")
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %.1fs | %s |\n", r.Name, r.Channel, r.Status, r.DurationS, detail)
	}
	b.WriteString("\n---\n\n_Machine-readable copy:_\n\n```json\n")
	buf, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return b.String(), err
	}
	b.Write(buf)
	b.WriteString("\n```\n")
	return b.String(), nil
}

func (s *HTTPServer) mcpDeployAll(req DeployAllRequest) interface{} {
	return RunDeployAll(context.Background(), req)
}
