package main

import "encoding/json"

// ops_twin.go — Remote Dev Twin control plane. These verbs let the mobile app
// start a remote recording/execution job on a surface such as redroid,
// Playwright, or ChromeDP. The phone controls and monitors; the remote dev
// machine runs and records.

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("twin_job_start",
		"Start a Remote Dev Twin job. Payload {surface: android-redroid|web-playwright|web-chromedp, mode, steps[], record, apk/package/hostWorkDir for Android, url/browser for web, sshHost/workDir for remote runners}. Returns a jobId; poll twin_job_status.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req twinJobRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := twinJobs.start(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("twin_job_status",
		"Live status of a Remote Dev Twin job. Payload {jobId}; omit jobId to list jobs. Completed jobs include artifact paths for video, trace, frames, screenshots, and metadata.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			if req.JobID == "" {
				return OpsResult{OK: true, Initial: map[string]any{"jobs": twinJobs.list()}}
			}
			job := twinJobs.get(req.JobID)
			if job == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no such twin job"}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})
}
