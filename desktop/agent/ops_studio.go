package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// ops_studio.go — the store-asset Studio as MCP/ops verbs, so the mobile app,
// web dashboard, and host Claude Code all reach it over the mesh via
// callOpsOnDevice (the same path every other capability uses). This is the
// "full support from managed cloud / on-prem" entry point: the verb runs on
// whichever device holds the repo + (for capture) the surface.
//
// studio_permission_prose is offline + fast (no device): analyze an app's
// permission usage and return the Play Console justification prose + shot-list.
// The actual video recording is driven by the capture layer (studio/redroid.go)
// on a runner; it is exposed via the CLI today and is the next verb to add.

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("studio_permission_prose",
		"Generate the Play Console permission-justification prose + demo-video shot-list for an app permission (e.g. FOREGROUND_SERVICE_SPECIAL_USE). Offline static analysis of AndroidManifest.xml — no device. Set useCase:true (with whatRuns/progressText/completionText) for the stronger NARRATIVE prose that argues necessity (a real task that would be killed mid-run without the foreground service). Works for any app.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Permission     string `json:"permission"`
				Path           string `json:"path"`
				Manifest       string `json:"manifest"`
				App            string `json:"app"`
				What           string `json:"what"`
				UseCase        bool   `json:"useCase"`
				WhatRuns       string `json:"whatRuns"`
				ProgressText   string `json:"progressText"`
				CompletionText string `json:"completionText"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			if strings.TrimSpace(req.Permission) == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "permission required (e.g. FOREGROUND_SERVICE_SPECIAL_USE)"}
			}
			root := strings.TrimSpace(req.Path)
			if root == "" {
				if cwd, err := os.Getwd(); err == nil {
					root = cwd
				}
			}
			manifestPath := strings.TrimSpace(req.Manifest)
			if manifestPath == "" {
				manifestPath = findAndroidManifest(root)
			}
			if manifestPath == "" {
				return OpsResult{OK: false, Code: "no_manifest", Error: "could not find AndroidManifest.xml under " + root + " — pass manifest"}
			}
			facts, err := studio.AnalyzeAndroidManifest(manifestPath, req.Permission)
			if err != nil {
				return OpsResult{OK: false, Code: "analyze_failed", Error: err.Error()}
			}
			facts.TriggerHint = studio.FindTrigger(root, facts)
			appName := strings.TrimSpace(req.App)
			if appName == "" {
				appName = "The app"
			}
			var j studio.Justification
			if req.UseCase {
				whatRuns := strings.TrimSpace(req.WhatRuns)
				if whatRuns == "" {
					whatRuns = req.What
				}
				j = studio.GenerateUseCaseJustification(facts, appName, studio.UseCaseConfig{
					WhatRuns:       whatRuns,
					ProgressText:   req.ProgressText,
					CompletionText: req.CompletionText,
				})
			} else {
				j = studio.GenerateJustification(facts, appName, req.What)
			}
			service := ""
			if facts.Service != nil {
				service = facts.Service.Name
			}
			return OpsResult{OK: true, Initial: map[string]any{
				"permission":  facts.Permission,
				"platform":    facts.Platform,
				"fgsType":     facts.FGSType,
				"service":     service,
				"subtype":     facts.SpecialUseSubtype,
				"trigger":     facts.TriggerHint,
				"declared":    facts.Declared,
				"taskOther":   j.TaskOther,
				"description": j.Description,
				"shotList":    j.ShotList,
				"warnings":    j.Warnings,
				"markdown":    j.Markdown(facts.Permission),
			}}
		})

	reg("studio_job_start",
		"Start an async app-compliance capture job (records a permission-justification demo video on a redroid surface). Returns a jobId; poll studio_job_status. Needs apk + hostWorkDir (+ sshHost for an on-prem runner; omit for the managed-cloud farm box). Pass a useCase object {whatRuns, startButtonText, stopButtonText, progressText, completionText, taskActions[]} to record the NARRATIVE video (gives a real task → shows work → backgrounds → task-finished notification) instead of the mechanical proof. Generic: any third-party app supplies its own apk/package/manifest + useCase strings.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioPermissionJobRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startPermissionVideo(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("studio_permission_video",
		"First-class one-call recorder for a Google Play permission-justification VIDEO. Defaults to the NARRATIVE use-case story (start a real task → show it working → background the app → 'task finished' notification → stop) which is what reviewers need for FOREGROUND_SERVICE_SPECIAL_USE; pass mechanical:true for the bare start→notify→stop proof. Works for Yaver itself AND any third-party app (supply apk, package, manifest, permission). Needs apk + hostWorkDir (+ sshHost for on-prem; omit for managed cloud). Returns a jobId; poll studio_job_status; the artifact is fetchable at /studio/jobs/<id>/captioned (or /raw).",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioPermissionJobRequest
			var extra struct {
				Mechanical bool `json:"mechanical"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
				_ = json.Unmarshal(payload, &extra)
			}
			if strings.TrimSpace(req.Permission) == "" {
				req.Permission = "FOREGROUND_SERVICE_SPECIAL_USE"
			}
			// Default to the narrative video unless explicitly asked for the
			// mechanical proof or a useCase was already supplied.
			if !extra.Mechanical && req.UseCase == nil {
				req.UseCase = &studioUseCaseReq{
					WhatRuns:       studioOrDefault(req.What, "an on-device coding agent running a real task"),
					ProgressText:   "running",
					CompletionText: "Task finished",
				}
			}
			job, err := studioJobs.startPermissionVideo(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("studio_screenshots_start",
		"Start an async store-screenshot capture job: launches the app on a redroid surface and captures a screenshot per scene (each scene = optional tapTexts to navigate + a shot). Needs apk + hostWorkDir (+ sshHost for on-prem). Poll studio_job_status.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioScreenshotsRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startScreenshots(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("studio_upload_screenshots",
		"Upload captured screenshots from a dir to the store. {dir, platform: ios|android, bundleId (iOS bundle id / Android package), locale, submit, version}. iOS uses App Store Connect (APP_STORE_KEY_* auth); Android uses a Play upload script (service-account creds).",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Dir      string `json:"dir"`
				Platform string `json:"platform"`
				BundleID string `json:"bundleId"`
				Locale   string `json:"locale"`
				Version  string `json:"version"`
				Submit   bool   `json:"submit"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			res, err := studioUploadScreenshots(req.Platform, req.Dir, req.BundleID, req.Locale, req.Version, req.Submit)
			if err != nil {
				return OpsResult{OK: false, Code: "upload_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: res}
		})

	reg("studio_job_status",
		"Live status of a Studio capture job: {jobId} → state/phase/log/artifacts. Omit jobId to list all jobs. Poll this to inform the user while the agent records compliance assets.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			if strings.TrimSpace(req.JobID) == "" {
				return OpsResult{OK: true, Initial: map[string]any{"jobs": studioJobs.list()}}
			}
			job := studioJobs.get(req.JobID)
			if job == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no such job"}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})
}
