package main

import (
	"context"
	"encoding/json"
	"strings"
)

// ops_qa.go — the agentic app-test agent (docs/yaver-ai-app-test-agent.md,
// Part II) as MCP/ops verbs. P0 ships the Yaver Base Image lifecycle: build a
// warm golden redroid snapshot once, restore it in seconds per run. The test
// run/oracle/fix verbs (qa_run, qa_status) land in P2–P4 on top of this base.
//
// Build/Up are long-running and return a jobId (poll studio_job_status, the same
// status UI Studio uses). List/GC are synchronous + structured.

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("qa_base_build",
		"Build a Yaver Base Image: cold-boot redroid, optionally bake an APK (yaverApk) + sign-in/seed, then snapshot /data into a versioned, warm-restorable golden image. Long-running — returns a jobId; poll studio_job_status. Runs on the local farm box (default) or an on-prem host (sshHost). Dirs default under ~/.yaver for the local runner; pass hostWorkDir+snapshotDir for ssh.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioBaseRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startBaseBuild(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("qa_base_up",
		"Restore a Yaver Base Image and warm-boot redroid, leaving the container running and ready to install/drive an app-under-test. {version (empty → latest), sshHost?, hostWorkDir?, snapshotDir?, container?}. Long-running — returns a jobId; poll studio_job_status.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioBaseRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startBaseUp(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("qa_base_list",
		"List Yaver Base Image snapshots for the host's arch, newest first (version, image, arch, sha256, bytes, yaverBaked). {sshHost?, hostWorkDir?, snapshotDir?}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioBaseRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			spec, err := baseSpecFromReq(req, nil)
			if err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			mans, err := spec.List(context.Background())
			if err != nil {
				return OpsResult{OK: false, Code: "list_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"bases": mans}}
		})

	reg("qa_run",
		"Run the agentic app-test suite: drive each yaver-tests/flows/*.flow.yaml flow with the LLM brain on a redroid surface and report bugs the oracle bank catches (catch-only). {package, apk?, flowsDir?, mode: catch|fix, base? (warm Yaver Base Image), sshHost?, hostWorkDir?, testAccount? (\"ephemeral\" mints+injects+deletes a throwaway account for {{email}}/{{password}}/{{fullName}} placeholders), convexUrl?}. Long-running — returns a jobId; poll studio_job_status, then qa_report for the structured report card.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req qaRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startQARun(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("qa_report",
		"Structured result of a qa_run: {jobId} → flows, bugs caught/fixed (title/severity/oracle/detail/step), and per-flow expectation verdicts. The UI 'report card'. Run qa_run first and wait for completion (studio_job_status).",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			rep := getQAReport(strings.TrimSpace(req.JobID))
			if rep == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no report for that jobId (still running, or unknown)"}
			}
			return OpsResult{OK: true, Initial: rep}
		})

	reg("qa_base_gc",
		"Prune old Yaver Base Image snapshots, keeping the newest {keep} (default 2). {keep, sshHost?, hostWorkDir?, snapshotDir?}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req studioBaseRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			keep := req.Keep
			if keep == 0 {
				keep = 2
			}
			spec, err := baseSpecFromReq(req, nil)
			if err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			removed, err := spec.GC(context.Background(), keep)
			if err != nil {
				return OpsResult{OK: false, Code: "gc_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"removed": removed, "kept": keep}}
		})
}
