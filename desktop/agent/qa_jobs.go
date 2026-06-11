package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/studio"
	"github.com/yaver-io/agent/testkit"
)

// qa_jobs.go — async wrapper that runs an agentic QA flow suite (qa_flow.go) as a
// studioJob the mobile/web UI and an agentic LLM poll via studio_job_status. The
// structured bug report is held in qaReports for the qa_report verb (the UI's
// "report card"). Catch-only today (P2); fix mode (P4) extends the same job.

type qaRunRequest struct {
	APK      string `json:"apk"`      // app artifact to install (omit when driving a pre-installed/base app)
	Package  string `json:"package"`  // app package id to launch
	Activity string `json:"activity"` // optional launch activity
	FlowsDir string `json:"flowsDir"` // dir of *.flow.yaml (default ./yaver-tests/flows)
	Mode     string `json:"mode"`     // "catch" (default) | "fix"

	// TestAccount, when "ephemeral", makes the runner mint a randomized
	// throwaway account and substitute {{email}}/{{password}}/{{fullName}} into
	// every flow before driving — then delete it on teardown. This is what lets
	// signup/onboarding flows run unattended without credentials in the repo.
	TestAccount string `json:"testAccount"` // "" | "ephemeral"
	ConvexURL   string `json:"convexUrl"`   // override; else signed-in config, else default

	// surface placement (mirrors studioBaseRequest)
	Base        string `json:"base"`        // restore this Yaver Base Image instead of cold boot
	Image       string `json:"image"`       // redroid image
	SSHHost     string `json:"sshHost"`     // empty = local farm box
	SSHOpts     string `json:"sshOpts"`     //
	HostWorkDir string `json:"hostWorkDir"` //
	SnapshotDir string `json:"snapshotDir"` //
	Container   string `json:"container"`   // default yaver-qa
}

// qaReports holds the structured report per job id (Screenshots dropped from the
// stored copy to bound memory; the UI renders counts + bug list + verdicts).
var qaReports = struct {
	sync.Mutex
	m map[string]*qaReport
}{m: map[string]*qaReport{}}

func storeQAReport(jobID string, r *qaReport) {
	qaReports.Lock()
	defer qaReports.Unlock()
	cp := *r
	cp.Screenshots = nil
	qaReports.m[jobID] = &cp
}

func getQAReport(jobID string) *qaReport {
	qaReports.Lock()
	defer qaReports.Unlock()
	return qaReports.m[jobID]
}

func (m *studioJobManager) startQARun(req qaRunRequest) (*studioJob, error) {
	flowsDir := strings.TrimSpace(req.FlowsDir)
	if flowsDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			flowsDir = filepath.Join(cwd, "yaver-tests", "flows")
		}
	}
	flows, err := loadFlows(flowsDir)
	if err != nil {
		return nil, fmt.Errorf("load flows from %s: %w", flowsDir, err)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("no *.flow.yaml in %s", flowsDir)
	}
	if strings.TrimSpace(req.Package) == "" && strings.TrimSpace(req.APK) == "" {
		return nil, fmt.Errorf("package (and usually apk) required")
	}

	// Ephemeral credential injection: mint a throwaway account synchronously so a
	// failure surfaces before the long-running job starts, then template it into
	// every flow. The account is deleted in the goroutine's defer.
	convexURL := resolveQAConvexURL(req.ConvexURL)
	var testAccount *qaTestAccount
	if strings.EqualFold(strings.TrimSpace(req.TestAccount), "ephemeral") {
		acct, err := createEphemeralQAAccount(context.Background(), convexURL)
		if err != nil {
			return nil, fmt.Errorf("create ephemeral test account: %w", err)
		}
		applyTestAccountTemplate(flows, acct)
		testAccount = acct
	}

	job := m.newJob("qa-run", "")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		if testAccount != nil {
			defer func() {
				if err := testAccount.delete(context.WithoutCancel(context.Background()), convexURL); err != nil {
					job.log("", "warning: could not delete ephemeral test account: "+err.Error())
				} else {
					job.log("", "deleted ephemeral test account "+testAccount.Email)
				}
			}()
		}
		job.mu.Lock()
		job.State = studioRunning
		job.mu.Unlock()

		if testAccount != nil {
			job.log("", "using ephemeral test account "+testAccount.Email)
		} else if flowsReferenceTestAccount(flows) {
			job.log("", "warning: a flow references {{email}}/{{password}} but testAccount!=\"ephemeral\" — placeholders left unsubstituted")
		}

		ctx := context.Background()
		cfg, err := qaConfigFromRequest(ctx, req, flows, func(l string) { job.log("", l) })
		if err != nil {
			m.fail(job, err.Error())
			return
		}
		job.log("running", fmt.Sprintf("%d flow(s) on %s", len(flows), cfg.surfaceLabel))

		report, err := runQAFlows(ctx, cfg.flowCfg)
		if err != nil {
			m.fail(job, "qa run: "+err.Error())
			return
		}
		storeQAReport(job.ID, report)

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".yaver", "qa", job.ID)
		_ = os.MkdirAll(dir, 0o755)

		job.mu.Lock()
		job.Dir = dir
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		job.log("done", fmt.Sprintf("%d caught / %d fixed across %d flow(s) — %s",
			report.Caught, report.Fixed, len(report.Flows), passLabel(report.Passed)))
	}()
	return job, nil
}

func passLabel(ok bool) string {
	if ok {
		return "PASS (no bugs)"
	}
	return "bugs found"
}

type qaResolvedConfig struct {
	flowCfg      qaFlowConfig
	surfaceLabel string
}

// qaConfigFromRequest resolves the runner, surface (cold redroid or warm base),
// app, and brain factory.
func qaConfigFromRequest(ctx context.Context, req qaRunRequest, flows []studio.Scenario, logf func(string)) (*qaResolvedConfig, error) {
	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(req.SSHHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(req.SSHOpts)}
	}
	_, local := runner.(studio.LocalRunner)
	hostWork := strings.TrimSpace(req.HostWorkDir)
	snapDir := strings.TrimSpace(req.SnapshotDir)
	if local {
		if home, err := os.UserHomeDir(); err == nil {
			if hostWork == "" {
				hostWork = filepath.Join(home, ".yaver", "qa-data")
			}
			if snapDir == "" {
				snapDir = filepath.Join(home, ".yaver", "base")
			}
		}
	}
	if hostWork == "" {
		return nil, fmt.Errorf("hostWorkDir required for an ssh runner")
	}
	container := strings.TrimSpace(req.Container)
	if container == "" {
		container = "yaver-qa"
	}

	var surface studio.CaptureSurface
	provision, teardown := true, true
	if base := strings.TrimSpace(req.Base); base != "" {
		bs := &studio.BaseSpec{
			R: runner, Image: req.Image, HostWorkDir: hostWork, SnapshotDir: snapDir,
			Version: base, Container: container, Log: logf,
		}
		surf, _, err := bs.Up(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore base %q: %w", base, err)
		}
		surface = surf
		provision, teardown = false, false // already warm; keep it for reuse
	} else {
		surface = &studio.RedroidSurface{
			R: runner, Name: container, Image: req.Image, HostWorkDir: hostWork, Log: logf,
		}
	}

	// One model lane for navigator + asserter (the user's BYOK / gateway config).
	visionCfg := testkit.LoadVisionConfig()
	brainFor := func(s studio.Scenario) studio.TestBrain {
		return newLLMBrain(newHTTPQAModel(visionCfg), s.Goal)
	}

	return &qaResolvedConfig{
		surfaceLabel: runner.Label(),
		flowCfg: qaFlowConfig{
			Surface:   surface,
			App:       studio.App{Package: req.Package, Activity: req.Activity},
			APKPath:   req.APK,
			Flows:     flows,
			BrainFor:  brainFor,
			Oracles:   studio.DefaultOracles,
			Mode:      req.Mode,
			Provision: provision,
			Teardown:  teardown,
			Log:       logf,
		},
	}, nil
}
