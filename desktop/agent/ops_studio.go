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
		"Generate the Play Console permission-justification prose + demo-video shot-list for an app permission (e.g. FOREGROUND_SERVICE_SPECIAL_USE). Offline static analysis of AndroidManifest.xml — no device.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Permission string `json:"permission"`
				Path       string `json:"path"`
				Manifest   string `json:"manifest"`
				App        string `json:"app"`
				What       string `json:"what"`
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
			j := studio.GenerateJustification(facts, appName, req.What)
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
		"Start an async app-compliance capture job (records a permission-justification demo video on a redroid surface). Returns a jobId; poll studio_job_status. Needs apk + hostWorkDir (+ sshHost for an on-prem runner; omit for the managed-cloud farm box).",
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
