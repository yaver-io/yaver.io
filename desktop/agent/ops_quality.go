package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ops_quality.go composes the existing browser and Redroid QA runners into one
// Talos-facing certification job. It intentionally delegates execution to the
// established ops_testkit and qa_jobs paths so artifacts, reports, profiles, and
// Redroid surfaces keep their existing contracts.

type talosQualityRunRequest struct {
	BrowserMode string                     `json:"browserMode"` // skip|chromedp|playwright-yaml|playwright-native
	Browser     testkitRunRequest          `json:"browser"`
	Native      playwrightNativeRunRequest `json:"native"`
	RunQA       bool                       `json:"runQA"`
	QA          qaRunRequest               `json:"qa"`
}

type talosQualityReport struct {
	JobID        string         `json:"jobId"`
	Passed       bool           `json:"passed"`
	BrowserMode  string         `json:"browserMode,omitempty"`
	Preflight    map[string]any `json:"preflight,omitempty"`
	BrowserJobID string         `json:"browserJobId,omitempty"`
	QAJobID      string         `json:"qaJobId,omitempty"`
	Web          *testkitReport `json:"web,omitempty"`
	Android      *qaReport      `json:"android,omitempty"`
	Summary      []string       `json:"summary,omitempty"`
}

var talosQualityReports = struct {
	sync.Mutex
	m map[string]*talosQualityReport
}{m: map[string]*talosQualityReport{}}

func storeTalosQualityReport(jobID string, r *talosQualityReport) {
	talosQualityReports.Lock()
	defer talosQualityReports.Unlock()
	talosQualityReports.m[jobID] = r
}

func getTalosQualityReport(jobID string) *talosQualityReport {
	talosQualityReports.Lock()
	defer talosQualityReports.Unlock()
	return talosQualityReports.m[jobID]
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("talos_quality_run",
		"Run a combined Talos quality certification on this machine: browser tests via chromedp/YAML Playwright/native Playwright, plus optional Redroid QA. Payload: {browserMode, browser, native, runQA, qa}. Long-running; poll studio_job_status then talos_quality_report.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req talosQualityRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startTalosQualityRun(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("talos_quality_report",
		"Structured report for a talos_quality_run: web Playwright/chromedp result, Android Redroid QA result, child job ids, pass/fail summary. Payload: {jobId}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			rep := getTalosQualityReport(strings.TrimSpace(req.JobID))
			if rep == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no quality report for that jobId (still running, or unknown)"}
			}
			return OpsResult{OK: true, Initial: rep}
		})
}

func (m *studioJobManager) startTalosQualityRun(req talosQualityRunRequest) (*studioJob, error) {
	mode := strings.TrimSpace(req.BrowserMode)
	if mode == "" {
		mode = "playwright-yaml"
	}
	switch mode {
	case "skip", "chromedp", "playwright-yaml", "playwright-native":
	default:
		return nil, fmt.Errorf("unsupported browserMode %q", mode)
	}
	if mode == "skip" && !req.RunQA {
		return nil, fmt.Errorf("enable browser tests or runQA")
	}
	job := m.newJob("talos-quality", "")
	go m.runTalosQuality(job, req, mode)
	return job, nil
}

func (m *studioJobManager) runTalosQuality(job *studioJob, req talosQualityRunRequest, mode string) {
	defer func() {
		if r := recover(); r != nil {
			m.fail(job, fmt.Sprintf("panic: %v", r))
		}
	}()
	job.mu.Lock()
	job.State = studioRunning
	job.mu.Unlock()

	rep := &talosQualityReport{JobID: job.ID, BrowserMode: mode, Passed: true}
	rep.Preflight = buildTalosQualityPreflight(req, mode)
	summary := []string{}
	if ready, _ := rep.Preflight["ready"].(bool); !ready {
		summary = append(summary, "preflight: dependencies missing")
	} else {
		summary = append(summary, "preflight: ready")
	}

	if mode != "skip" {
		job.log("browser", "starting "+mode+" browser run")
		child, err := startTalosQualityBrowserChild(req, mode)
		if err != nil {
			m.fail(job, "browser start: "+err.Error())
			return
		}
		rep.BrowserJobID = child.ID
		state := waitForStudioJob(child.ID, 12*time.Hour, func(line string) { job.log("", "browser: "+line) })
		web := getTestkitReport(child.ID)
		rep.Web = web
		if state == studioFailed || web == nil || web.Failed > 0 {
			rep.Passed = false
		}
		if web != nil {
			summary = append(summary, fmt.Sprintf("web: %d passed / %d failed", web.Passed, web.Failed))
		} else {
			summary = append(summary, "web: no report")
		}
	}

	if req.RunQA {
		job.log("android", "starting Redroid QA run")
		child, err := studioJobs.startQARun(req.QA)
		if err != nil {
			m.fail(job, "redroid qa start: "+err.Error())
			return
		}
		rep.QAJobID = child.ID
		state := waitForStudioJob(child.ID, 12*time.Hour, func(line string) { job.log("", "android: "+line) })
		qa := getQAReport(child.ID)
		rep.Android = qa
		if state == studioFailed || qa == nil || !qa.Passed {
			rep.Passed = false
		}
		if qa != nil {
			summary = append(summary, fmt.Sprintf("android: %d caught / %d fixed", qa.Caught, qa.Fixed))
		} else {
			summary = append(summary, "android: no report")
		}
	}

	rep.Summary = summary
	storeTalosQualityReport(job.ID, rep)

	job.mu.Lock()
	job.State = studioCompleted
	job.FinishedAt = time.Now()
	job.mu.Unlock()
	if rep.Passed {
		job.log("done", "Talos quality PASS")
	} else {
		job.log("done", "Talos quality found failures")
	}
}

func startTalosQualityBrowserChild(req talosQualityRunRequest, mode string) (*studioJob, error) {
	switch mode {
	case "chromedp":
		return studioJobs.startTestkitRun(req.Browser)
	case "playwright-yaml":
		b := req.Browser
		if b.Headed {
			b.Headful = true
		}
		b.ForcePlaywright = true
		if b.Project == "" {
			b.Project = "playwright"
		}
		if strings.TrimSpace(b.StorageState) == "" && strings.TrimSpace(b.Profile) != "" {
			path, err := playwrightStorageStatePath(b.Profile)
			if err != nil {
				return nil, err
			}
			b.StorageState = path
		}
		return studioJobs.startTestkitRun(b)
	case "playwright-native":
		return studioJobs.startPlaywrightNativeRun(req.Native)
	default:
		return nil, fmt.Errorf("unsupported browserMode %q", mode)
	}
}

func buildTalosQualityPreflight(req talosQualityRunRequest, mode string) map[string]any {
	deps, ready := checkTestkitDeps()
	out := map[string]any{
		"ready":      ready,
		"os":         runtime.GOOS,
		"pkgManager": detectPkgManager(),
		"deps":       deps,
	}
	if mode == "playwright-yaml" {
		pw := playwrightReadiness(req.Browser.Dir)
		out["playwright"] = pw
		if ok, _ := pw["ok"].(bool); !ok {
			out["ready"] = false
		}
	}
	if mode == "playwright-native" {
		pw := playwrightReadiness(req.Native.Dir)
		out["playwright"] = pw
		if ok, _ := pw["ok"].(bool); !ok {
			out["ready"] = false
		}
	}
	if req.RunQA {
		redroidReady := true
		missing := []string{}
		for _, d := range deps {
			if d.Name == "docker" || d.Name == "redroid-image" {
				if !d.Present {
					redroidReady = false
					missing = append(missing, d.Name)
				}
			}
		}
		out["redroid"] = map[string]any{"required": true, "ready": redroidReady, "missing": missing}
		if !redroidReady {
			out["ready"] = false
		}
	}
	return out
}

func waitForStudioJob(jobID string, timeout time.Duration, logf func(string)) studioJobState {
	deadline := time.Now().Add(timeout)
	lastSeen := 0
	for {
		child := studioJobs.get(jobID)
		if child == nil {
			return studioFailed
		}
		child.mu.Lock()
		state := child.State
		lines := append([]string(nil), child.LogLines...)
		errText := child.Error
		child.mu.Unlock()
		for lastSeen < len(lines) {
			if logf != nil {
				logf(lines[lastSeen])
			}
			lastSeen++
		}
		if state == studioCompleted || state == studioFailed {
			if errText != "" && logf != nil {
				logf("error: " + errText)
			}
			return state
		}
		if time.Now().After(deadline) {
			if logf != nil {
				logf("timed out waiting for child job " + jobID)
			}
			return studioFailed
		}
		time.Sleep(1 * time.Second)
	}
}
