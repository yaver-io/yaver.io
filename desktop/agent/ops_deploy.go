package main

// ops_deploy.go — verb "deploy": push the current project to a hosting
// target. Same consolidating spirit as build/push — one verb, N
// provider branches, so agents don't have to learn per-provider
// tools. Streams provider output via the execMgr.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type opsDeployPayload struct {
	// Target: cloud | cloudflare | vercel | fly | netlify | railway |
	// firebase | platform | testflight | playstore | convex | eas.
	// When ops is called with machine=auto, the dispatcher uses this
	// target plus workDir/project metadata to pick the best executor.
	Target  string `json:"target"`
	WorkDir string `json:"workDir,omitempty"`
	// Env: production / staging / preview / custom.
	Env string `json:"env,omitempty"`
	// Extra args appended to the provider CLI.
	Args []string `json:"args,omitempty"`
	// TimeoutSec: kill the deploy after this many seconds. 0 = none.
	TimeoutSec int `json:"timeoutSec,omitempty"`
	// InstallDeps: caller approval to download + install a missing
	// toolchain before deploying (e.g. JDK 17 / Android SDK for the
	// playstore target). testflight on a non-macOS host is impossible
	// and is rejected regardless of this flag.
	InstallDeps bool `json:"installDeps,omitempty"`
	// Action: "deploy" (default) or "rollback". rollback runs the
	// provider's native rollback CLI rather than pushing a new build.
	// Each provider exposes a different rollback shape; the verb
	// hides that behind a single action so an AI agent (or the
	// workspace UI's "rollback" chip) doesn't need a per-provider
	// rule.
	Action string `json:"action,omitempty"`
	// Deployment: optional explicit deployment id / build id /
	// version to roll back TO. Most providers accept "previous" or
	// equivalent when this is empty; we pass it through verbatim.
	Deployment string `json:"deployment,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "deploy",
		Description: "Deploy the project at workDir to a hosting target. target=cloud (Yaver cloud), cloudflare, vercel, fly, netlify, railway, firebase, platform (Yaver platform), convex, eas (Expo), testflight, playstore. action=deploy (default) | rollback — rollback uses the provider's native rollback API (Vercel `vercel rollback`, Fly `flyctl releases rollback`, Netlify `netlify rollback`, Cloudflare Pages `wrangler pages rollback`, Railway `railway rollback`, Convex `npx convex env get DEPLOY_KEY && npx convex deploy --previous-deployment`). Platform-aware (testflight refuses on non-macOS) and dependency-aware (playstore returns deps_missing if JDK/Android SDK absent; pass installDeps:true to install with approval). Streams provider output.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"target":      map[string]interface{}{"type": "string"},
				"workDir":     map[string]interface{}{"type": "string"},
				"env":         map[string]interface{}{"type": "string"},
				"args":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"timeoutSec":  map[string]interface{}{"type": "integer"},
				"installDeps": map[string]interface{}{"type": "boolean"},
				"action":      map[string]interface{}{"type": "string", "enum": []string{"deploy", "rollback"}, "default": "deploy"},
				"deployment":  map[string]interface{}{"type": "string", "description": "Explicit deployment id / build id to roll back to (most providers accept empty = previous)"},
			},
			"additionalProperties": false,
		},
		Handler:    opsDeployHandler,
		Streaming:  true,
		AllowGuest: true,
	})
}

func opsDeployHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsDeployPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}

	// C-6: guest hardening. The deploy verb is AllowGuest=true so a
	// shared-machine deploy guest can ship TestFlight/Play/CF builds.
	// But guests cannot:
	//   - inject shell metacharacters via Args[] (the join would
	//     concatenate into `sh -c <cmd>` and metacharacters escape
	//     the intended argv)
	//   - point the deploy at an arbitrary workDir on the host
	//     (must come from the workspace manifest, not the request)
	if c.Caller == "guest" {
		for _, a := range p.Args {
			if argContainsShellMetacharacter(a) {
				return OpsResult{OK: false, Code: "bad_payload", Error: "guest deploy: args[] entry contains shell metacharacters"}
			}
		}
		// Force workDir resolution server-side. p.WorkDir from a guest
		// is dropped on the floor — the receiving handler doesn't have
		// a workspace manifest yet, so the guest path uses cwd. The
		// HTTP-layer /deploy/ship handler is the canonical guest deploy
		// entrypoint with full project resolution; for ops/deploy we
		// keep behaviour conservative.
		workDir = "."
	}
	p.WorkDir = workDir

	resolvedTarget, inferredFrom, resolveErr := resolveDeployTarget(p, stackDetect(workDir))
	if resolveErr != nil {
		var derr *deployResolveError
		if errors.As(resolveErr, &derr) {
			return OpsResult{OK: false, Code: derr.Code, Error: derr.Error(), Initial: derr.initial()}
		}
		return OpsResult{OK: false, Code: "bad_payload", Error: resolveErr.Error()}
	}
	p.Target = resolvedTarget

	// Platform + dependency gate. For testflight this rejects non-macOS
	// hosts up front (it is impossible, not merely missing a tool); for
	// playstore it blocks until JDK 17 + Android SDK are present, with
	// installDeps:true the caller's approval to install them. Cloud /
	// web / backend targets classify as nativeNone and pass straight
	// through.
	if pf := runBuildPreflight(c.Ctx, classifyNative("deploy", p.Target, workDir), p.InstallDeps, nil); !pf.OK {
		return OpsResult{OK: false, Code: pf.Code, Error: pf.Error, Initial: preflightInitial(pf)}
	}

	extra := strings.Join(p.Args, " ")

	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action == "" {
		action = "deploy"
	}

	// Rollback dispatch — provider-native rollback CLI per target.
	// We keep this above the forward-deploy switch so a typo in
	// p.Action ("rollbock") is rejected explicitly rather than
	// silently doing a deploy.
	if action == "rollback" {
		return opsDeployRollbackHandler(c, p, extra)
	}
	if action != "deploy" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "action must be deploy or rollback"}
	}
	switch strings.ToLower(strings.TrimSpace(p.Target)) {
	case "cloud", "yaver-cloud":
		return OpsResult{OK: false, Code: "unimplemented", Error: "cloud deploy is not implemented on ops deploy; use cloud_deploy instead", Initial: map[string]interface{}{"mcpTool": "cloud_deploy"}}
	case "platform":
		return OpsResult{OK: false, Code: "unimplemented", Error: "platform deploy is not implemented on ops deploy; use platform_deploy instead", Initial: map[string]interface{}{"mcpTool": "platform_deploy"}}
	case "testflight":
		return OpsResult{OK: false, Code: "unimplemented", Error: "testflight deploy is not implemented on ops deploy; use mobile_project_build instead", Initial: map[string]interface{}{"mcpTool": "mobile_project_build"}}
	case "playstore", "play":
		return OpsResult{OK: false, Code: "unimplemented", Error: "playstore deploy is not implemented on ops deploy; use mobile_project_build instead", Initial: map[string]interface{}{"mcpTool": "mobile_project_build"}}
	}

	cmd, tool, err := resolveDeployCommand(p, stackDetect(workDir))
	if err != nil {
		var derr *deployResolveError
		if errors.As(err, &derr) {
			return OpsResult{OK: false, Code: derr.Code, Error: derr.Error(), Initial: derr.initial()}
		}
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}

	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised"}
	}
	sess, err := c.Server.execMgr.StartExec(strings.TrimSpace(cmd), workDir, "", nil, p.TimeoutSec)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}
	initial := map[string]interface{}{
		"sessionId": sess.ID,
		"tool":      tool,
		"command":   strings.TrimSpace(cmd),
		"workDir":   workDir,
		"env":       p.Env,
		"sseHint":   fmt.Sprintf("/exec/%s/stream for live output", sess.ID),
	}
	if inferredFrom != "" {
		initial["inferredTarget"] = resolvedTarget
		initial["inferredFrom"] = inferredFrom
	}
	final := waitOpsExec(sess)
	for k, v := range final {
		initial[k] = v
	}
	ok, _ := initial["ok"].(bool)
	code, _ := initial["code"].(string)
	errText, _ := initial["error"].(string)
	return OpsResult{OK: ok, Code: code, Error: errText, Initial: initial}
}

type deployResolveError struct {
	Code       string
	Message    string
	Scanned    string
	Candidates []string
}

func (e *deployResolveError) Error() string { return e.Message }

func (e *deployResolveError) initial() map[string]interface{} {
	out := map[string]interface{}{}
	if e.Scanned != "" {
		out["scanned"] = e.Scanned
	}
	if len(e.Candidates) > 0 {
		out["candidates"] = append([]string(nil), e.Candidates...)
	}
	return out
}

func resolveDeployTarget(p opsDeployPayload, det *StackDetection) (target, inferredFrom string, err error) {
	if strings.TrimSpace(p.Target) != "" {
		if err := validateTargetAgainstDetection(strings.TrimSpace(p.Target), det); err != nil {
			return "", "", err
		}
		return strings.TrimSpace(p.Target), "", nil
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}
	deployable := det.DeployableTargets()
	switch len(deployable) {
	case 0:
		return "", "", &deployResolveError{
			Code:    "bad_payload",
			Message: "no deployable target detected in " + workDir + " — scanned stack markers but found nothing deployable",
			Scanned: workDir,
		}
	case 1:
		opsTarget := firstOpsTarget(deployable[0])
		if opsTarget == "" {
			return "", "", &deployResolveError{
				Code:    "bad_payload",
				Message: "detected " + deployable[0].ID + " but it exposes no deploy action",
				Scanned: workDir,
			}
		}
		return opsTarget, deployable[0].Evidence, nil
	default:
		candidates := make([]string, 0, len(deployable))
		for _, tgt := range deployable {
			if opsTarget := firstOpsTarget(tgt); opsTarget != "" {
				candidates = append(candidates, opsTarget)
			}
		}
		candidates = dedupeSorted(candidates)
		return "", "", &deployResolveError{
			Code:       "ambiguous_target",
			Message:    "multiple deployable targets detected; specify target explicitly",
			Scanned:    workDir,
			Candidates: candidates,
		}
	}
}

func validateTargetAgainstDetection(target string, det *StackDetection) error {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "supabase-functions", "supabase-db":
		if tgt := findDetectedTarget(det, "supabase"); tgt == nil || tgt.Weak || !tgt.Supported {
			return &deployResolveError{
				Code:    "bad_payload",
				Message: "supabase deploy refused: no local Supabase project detected in this workDir",
			}
		}
	}
	return nil
}

func resolveDeployCommand(p opsDeployPayload, det *StackDetection) (cmd, tool string, err error) {
	target, _, err := resolveDeployTarget(p, det)
	if err != nil {
		return "", "", err
	}
	p.Target = target
	extra := strings.Join(p.Args, " ")
	envFlag := ""
	if p.Env != "" {
		envFlag = " --env=" + opsShellQuote(p.Env)
	}
	switch strings.ToLower(strings.TrimSpace(p.Target)) {
	case "cloud", "yaver-cloud":
		// Points to the existing cloud_deploy tool; its full
		// implementation handles plan + provision + push.
		return "", "", &deployResolveError{Code: "bad_payload", Message: "cloud deploy is handled by the cloud_deploy MCP tool"}
	case "cloudflare", "cf", "workers":
		cmd, tool = "npx wrangler deploy"+envFlag+" "+extra, "cloudflare"
	case "pages":
		cmd, tool = "npx wrangler pages deploy "+extra, "cloudflare-pages"
	case "vercel":
		prod := ""
		if p.Env == "production" || p.Env == "prod" {
			prod = " --prod"
		}
		cmd, tool = "npx vercel"+prod+" "+extra, "vercel"
	case "fly", "fly.io":
		cmd, tool = "flyctl deploy "+extra, "fly"
	case "netlify":
		prod := ""
		if p.Env == "production" || p.Env == "prod" {
			prod = " --prod"
		}
		cmd, tool = "npx netlify-cli deploy"+prod+" "+extra, "netlify"
	case "railway":
		cmd, tool = "railway up "+extra, "railway"
	case "firebase":
		cmd, tool = "firebase deploy "+extra, "firebase"
	case "convex":
		cmd, tool = "npx convex deploy "+extra, "convex"
	case "supabase-functions":
		cmd, tool = "supabase functions deploy "+strings.TrimSpace(extra), "supabase"
	case "supabase-db":
		cmd, tool = "supabase db push "+strings.TrimSpace(extra), "supabase"
	case "eas", "expo":
		cmd, tool = "eas submit "+extra, "eas"
	case "platform":
		return "", "", &deployResolveError{Code: "bad_payload", Message: "platform deploy is handled by the platform_deploy MCP tool"}
	case "testflight":
		return "", "", &deployResolveError{Code: "bad_payload", Message: "testflight deploy is handled by mobile_project_build"}
	case "playstore", "play":
		return "", "", &deployResolveError{Code: "bad_payload", Message: "playstore deploy is handled by mobile_project_build"}
	default:
		return "", "", &deployResolveError{Code: "bad_payload", Message: "unknown target: " + p.Target}
	}
	return strings.TrimSpace(cmd), tool, nil
}

// opsDeployRollbackHandler routes to the provider-native rollback CLI.
// All forward-deploy validation already ran in opsDeployHandler (auth,
// guest gate, preflight); this just maps target → rollback command.
//
// Provider rollback shapes (verified against each tool's CLI):
//
//	cloudflare/pages   wrangler pages rollback [deployment]
//	vercel             vercel rollback [deployment-url]
//	netlify            netlify rollback (rolls to previous deploy)
//	fly                flyctl releases rollback [version]
//	railway            railway rollback (interactive when no version)
//	firebase           firebase hosting:rollback [--site=NAME] [version]
//	cloudflare workers wrangler rollback [version-id]
//	convex             convex env get DEPLOY_KEY (no native rollback;
//	                                              emit hint instead)
//
// testflight / playstore have no native rollback — you ship a new
// build with a higher version. Refuse rather than fake it.
func opsDeployRollbackHandler(c OpsContext, p opsDeployPayload, extra string) OpsResult {
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}
	deployment := strings.TrimSpace(p.Deployment)
	deploymentArg := ""
	if deployment != "" {
		deploymentArg = " " + opsShellQuote(deployment)
	}
	var cmd, tool string
	switch strings.ToLower(p.Target) {
	case "cloudflare", "cf", "workers":
		cmd, tool = "npx wrangler rollback"+deploymentArg+" "+extra, "cloudflare-rollback"
	case "pages":
		cmd, tool = "npx wrangler pages rollback"+deploymentArg+" "+extra, "cloudflare-pages-rollback"
	case "vercel":
		cmd, tool = "npx vercel rollback"+deploymentArg+" "+extra, "vercel-rollback"
	case "fly", "fly.io":
		cmd, tool = "flyctl releases rollback"+deploymentArg+" "+extra, "fly-rollback"
	case "netlify":
		cmd, tool = "npx netlify-cli rollback "+extra, "netlify-rollback"
	case "railway":
		cmd, tool = "railway rollback "+extra, "railway-rollback"
	case "firebase":
		cmd, tool = "firebase hosting:rollback "+extra, "firebase-rollback"
	case "convex":
		return OpsResult{OK: false, Code: "no_rollback", Error: "convex has no native rollback — re-deploy a previous git commit instead"}
	case "supabase-functions", "supabase-db":
		return OpsResult{OK: false, Code: "no_rollback", Error: "Supabase has no native rollback for functions or db push — deploy a corrective change instead"}
	case "testflight":
		return OpsResult{OK: false, Code: "no_rollback", Error: "TestFlight has no rollback — submit a new build with a higher CFBundleVersion"}
	case "playstore", "play":
		return OpsResult{OK: false, Code: "no_rollback", Error: "Play Store has no rollback — submit a new build with a higher versionCode (or use a staged-rollout halt)"}
	case "cloud", "yaver-cloud":
		return OpsResult{OK: false, Code: "unimplemented", Error: "cloud rollback is not implemented on ops deploy; use cloud_deploy instead", Initial: map[string]interface{}{"mcpTool": "cloud_deploy"}}
	case "platform":
		return OpsResult{OK: false, Code: "unimplemented", Error: "platform rollback is not implemented on ops deploy; use platform_deploy instead", Initial: map[string]interface{}{"mcpTool": "platform_deploy"}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "rollback not supported for target: " + p.Target}
	}

	sess, err := c.Server.execMgr.StartExec(strings.TrimSpace(cmd), workDir, "", nil, p.TimeoutSec)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}
	initial := map[string]interface{}{
		"sessionId":  sess.ID,
		"tool":       tool,
		"command":    strings.TrimSpace(cmd),
		"workDir":    workDir,
		"deployment": deployment,
		"sseHint":    fmt.Sprintf("/exec/%s/stream for live rollback output", sess.ID),
	}
	final := waitOpsExec(sess)
	for k, v := range final {
		initial[k] = v
	}
	ok, _ := initial["ok"].(bool)
	code, _ := initial["code"].(string)
	errText, _ := initial["error"].(string)
	return OpsResult{OK: ok, Code: code, Error: errText, Initial: initial}
}

func waitOpsExec(sess *ExecSession) map[string]interface{} {
	<-sess.doneCh
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	out := map[string]interface{}{
		"status":     string(sess.Status),
		"finishedAt": sess.FinishedAt,
	}
	if sess.ExitCode != nil {
		out["exitCode"] = *sess.ExitCode
	}
	const tail = 4096
	stdout := sess.Stdout
	stderr := sess.Stderr
	if len(stdout) > tail {
		stdout = stdout[len(stdout)-tail:]
	}
	if len(stderr) > tail {
		stderr = stderr[len(stderr)-tail:]
	}
	if strings.TrimSpace(stdout) != "" {
		out["stdoutTail"] = stdout
	}
	if strings.TrimSpace(stderr) != "" {
		out["stderrTail"] = stderr
	}
	switch sess.Status {
	case ExecStatusCompleted:
		out["ok"] = true
		out["code"] = ""
	case ExecStatusKilled:
		out["ok"] = false
		out["code"] = statusBlocked
		out["error"] = "deploy timed out"
	default:
		out["ok"] = false
		out["code"] = "exec_failed"
		if strings.TrimSpace(stderr) != "" {
			out["error"] = strings.TrimSpace(stderr)
		} else if strings.TrimSpace(stdout) != "" {
			out["error"] = strings.TrimSpace(stdout)
		} else {
			out["error"] = "deploy failed"
		}
	}
	return out
}

// argContainsShellMetacharacter reports whether s contains any byte
// commonly used to break out of a quoted shell argument when joined
// into a `sh -c <cmd>` string. Used to gate guest-supplied Args[]
// entries on ops deploy.
func argContainsShellMetacharacter(s string) bool {
	for _, r := range s {
		switch r {
		case ';', '|', '&', '$', '`', '<', '>', '\\', '"', '\'', '\n', '\r', '(', ')', '{', '}':
			return true
		}
	}
	return false
}

func findDetectedTarget(det *StackDetection, id string) *DetectedTarget {
	if det == nil {
		return nil
	}
	for i := range det.Targets {
		if det.Targets[i].ID == id {
			return &det.Targets[i]
		}
	}
	return nil
}
