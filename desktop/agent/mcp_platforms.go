package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	osexec "os/exec"
	"slices"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Supabase — wraps supabase CLI
// ---------------------------------------------------------------------------

func mcpSupabaseStatus(dir string) interface{} {
	return supabaseRun(dir, "status")
}

func mcpSupabaseDB(dir, query string) interface{} {
	return supabaseRun(dir, "db", "execute", "--sql", query)
}

func mcpSupabaseMigrations(dir string) interface{} {
	return supabaseRun(dir, "migration", "list")
}

func mcpSupabaseFunctions(dir string) interface{} {
	return supabaseRun(dir, "functions", "list")
}

func mcpSupabaseDeploy(dir, function string) interface{} {
	if function != "" {
		return supabaseRun(dir, "functions", "deploy", function)
	}
	return supabaseRun(dir, "db", "push")
}

func supabaseRun(dir string, args ...string) interface{} {
	cmd := osexec.Command("supabase", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("supabase: %s (install: brew install supabase/tap/supabase)", err), "output": string(out)}
	}
	return map[string]interface{}{"output": string(out)}
}

// ---------------------------------------------------------------------------
// Convex — wraps npx convex CLI
// ---------------------------------------------------------------------------

func mcpConvexStatus(dir string) interface{} {
	return convexRun(dir, "dashboard")
}

func mcpConvexDeploy(dir string) interface{} {
	return convexRun(dir, "deploy", "--yes")
}

func mcpConvexLogs(dir string) interface{} {
	return convexRun(dir, "logs", "--success", "--limit", "20")
}

func mcpConvexFunctions(dir string) interface{} {
	return convexRun(dir, "functions", "list")
}

func mcpConvexRun(dir, functionPath, argsJSON string) interface{} {
	cliArgs := []string{"run", functionPath}
	if argsJSON != "" {
		cliArgs = append(cliArgs, argsJSON)
	}
	return convexRun(dir, cliArgs...)
}

func convexRun(dir string, args ...string) interface{} {
	allArgs := append([]string{"convex"}, args...)
	cmd := osexec.Command("npx", allArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("convex: %s", err), "output": string(out)}
	}
	return map[string]interface{}{"output": string(out)}
}

// ---------------------------------------------------------------------------
// Cloudflare — wraps wrangler CLI
// ---------------------------------------------------------------------------

func mcpCFWorkers(dir string) interface{} {
	return wranglerRun(dir, "deployments", "list")
}

func mcpCFDeploy(dir string) interface{} {
	return wranglerRun(dir, "deploy")
}

func mcpCFTail(dir, workerName string) interface{} {
	// Get recent logs (non-interactive)
	return wranglerRun(dir, "tail", workerName, "--format", "json", "--once")
}

func mcpCFPages(dir string) interface{} {
	return wranglerRun(dir, "pages", "deployment", "list")
}

func mcpCFR2(action, bucket, key string) interface{} {
	switch action {
	case "list":
		return wranglerRun("", "r2", "object", "list", bucket)
	case "buckets":
		return wranglerRun("", "r2", "bucket", "list")
	default:
		return map[string]interface{}{"error": "action: list, buckets"}
	}
}

func mcpCFD1(action, dbName, query string) interface{} {
	switch action {
	case "list":
		return wranglerRun("", "d1", "list")
	case "query":
		return wranglerRun("", "d1", "execute", dbName, "--command", query)
	default:
		return map[string]interface{}{"error": "action: list, query"}
	}
}

func mcpCFKV(action, namespace, key, value string) interface{} {
	switch action {
	case "list":
		return wranglerRun("", "kv", "namespace", "list")
	case "keys":
		return wranglerRun("", "kv", "key", "list", "--namespace-id", namespace)
	case "get":
		return wranglerRun("", "kv", "key", "get", "--namespace-id", namespace, key)
	case "put":
		return wranglerRun("", "kv", "key", "put", "--namespace-id", namespace, key, value)
	default:
		return map[string]interface{}{"error": "action: list, keys, get, put"}
	}
}

func mcpCFDNS(zone, action, name, dnsType, content string) interface{} {
	switch action {
	case "list":
		return wranglerRun("", "dns", "records", "list", zone)
	default:
		return map[string]interface{}{"error": "action: list"}
	}
}

func wranglerRun(dir string, args ...string) interface{} {
	allArgs := append([]string{"wrangler"}, args...)
	cmd := osexec.Command("npx", allArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("wrangler: %s (install: npm install -g wrangler)", err), "output": string(out)}
	}
	return map[string]interface{}{"output": string(out)}
}

// ---------------------------------------------------------------------------
// GitLab — wraps glab CLI
// ---------------------------------------------------------------------------

func mcpGitLabMRs(dir, state string) interface{} {
	if state == "" {
		state = "opened"
	}
	args := []string{"mr", "list", "--state", state}
	return glabRun(dir, args...)
}

func mcpGitLabIssues(dir, state string) interface{} {
	if state == "" {
		state = "opened"
	}
	return glabRun(dir, "issue", "list", "--state", state)
}

func mcpGitLabPipelines(dir string) interface{} {
	return glabRun(dir, "ci", "list")
}

func mcpGitLabPipelineJobs(dir, pipelineID string) interface{} {
	return glabRun(dir, "ci", "view", pipelineID)
}

func mcpGitLabCI(dir string) interface{} {
	return glabRun(dir, "ci", "status")
}

func glabRun(dir string, args ...string) interface{} {
	cmd := osexec.Command("glab", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("glab: %s (install: brew install glab)", err), "output": string(out)}
	}
	return map[string]interface{}{"output": string(out)}
}

// ---------------------------------------------------------------------------
// GitHub CLI extras — beyond what we already have
// ---------------------------------------------------------------------------

func mcpGitHubRepoInfo(dir string) interface{} {
	cmd := osexec.Command("gh", "repo", "view", "--json", "name,owner,description,stargazerCount,forkCount,isPrivate,defaultBranchRef,languages,licenseInfo,url")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpGitHubReleases(dir string) interface{} {
	cmd := osexec.Command("gh", "release", "list", "--json", "tagName,name,publishedAt,isDraft,isPrerelease", "--limit", "10")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": string(out)}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return map[string]interface{}{"releases": result}
}

func mcpGitHubStargazers(repo string) interface{} {
	out, err := runCmd("gh", "api", fmt.Sprintf("repos/%s", repo), "--jq", ".stargazers_count")
	if err != nil {
		return map[string]interface{}{"error": out}
	}
	return map[string]interface{}{"repo": repo, "stars": strings.TrimSpace(out)}
}

// ---------------------------------------------------------------------------
// PlanetScale — wraps pscale CLI
// ---------------------------------------------------------------------------

func mcpPlanetScaleBranches(db string) interface{} {
	out, err := runCmd("pscale", "branch", "list", db, "--format", "json")
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("pscale: %s (install: brew install planetscale/tap/pscale)", out)}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return result
}

func mcpPlanetScaleDeploy(db, branch string) interface{} {
	out, err := runCmd("pscale", "deploy-request", "create", db, branch)
	if err != nil {
		return map[string]interface{}{"error": out}
	}
	return map[string]interface{}{"output": out}
}

// ---------------------------------------------------------------------------
// Prisma — wraps prisma CLI
// ---------------------------------------------------------------------------

func mcpPrismaStatus(dir string) interface{} {
	cmd := osexec.Command("npx", "prisma", "migrate", "status")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"output": string(out)}
}

func mcpPrismaGenerate(dir string) interface{} {
	cmd := osexec.Command("npx", "prisma", "generate")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

func mcpPrismaPush(dir string) interface{} {
	cmd := osexec.Command("npx", "prisma", "db", "push")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

// ---------------------------------------------------------------------------
// Drizzle ORM
// ---------------------------------------------------------------------------

func mcpDrizzlePush(dir string) interface{} {
	cmd := osexec.Command("npx", "drizzle-kit", "push")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

func mcpDrizzleGenerate(dir string) interface{} {
	cmd := osexec.Command("npx", "drizzle-kit", "generate")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

// ---------------------------------------------------------------------------
// Fly.io — wraps flyctl
// ---------------------------------------------------------------------------

func mcpFlyStatus(dir string) interface{} {
	cmd := osexec.Command("flyctl", "status", "--json")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("flyctl: %s", string(out))}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpFlyDeploy(dir string) interface{} {
	cmd := osexec.Command("flyctl", "deploy")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

func mcpFlyLogs(appName string) interface{} {
	out, err := runCmd("flyctl", "logs", "--app", appName, "--no-tail")
	if err != nil {
		return map[string]interface{}{"error": out}
	}
	return map[string]interface{}{"logs": out}
}

// ---------------------------------------------------------------------------
// Railway — wraps railway CLI
// ---------------------------------------------------------------------------

func mcpRailwayStatus(dir string) interface{} {
	cmd := osexec.Command("railway", "status", "--json")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("railway: %s", string(out))}
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result
}

func mcpRailwayDeploy(dir string) interface{} {
	cmd := osexec.Command("railway", "up")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

// ---------------------------------------------------------------------------
// gh_run / glab_run — generic CLI passthrough so coding agents can
// invoke any subcommand without us shipping a per-verb wrapper. The
// agent already has 12+ specific tools (github_prs, github_issues,
// github_ci_status, gitlab_mrs, …) but those cover read-only cases.
// Anything write-side (gh repo create, gh workflow run, gh release
// create, glab snippet create, …) used to require a coding agent
// to shell out manually, which doesn't work when the runner runs
// inside a sandbox without the same PATH posture.
//
// Both helpers preflight DetectGitProviderCLIs to (a) bail with a
// useful "install with `yaver install gh`" message if missing and
// (b) bail fast if installed-but-not-authed, since `gh pr create`
// against an unauthed gh hangs on a stdin prompt.
// ---------------------------------------------------------------------------

func mcpGhRun(dir string, args []string, confirm bool) interface{} {
	return runForgeCLICommand("gh", dir, args, confirm)
}

func mcpGlabRun(dir string, args []string, confirm bool) interface{} {
	return runForgeCLICommand("glab", dir, args, confirm)
}

func runForgeCLICommand(name, dir string, args []string, confirm bool) interface{} {
	clis := DetectGitProviderCLIs()
	cli, ok := clis[name]
	if !ok || !cli.Available {
		if name == "glab" {
			return map[string]interface{}{"error": "glab CLI not on PATH — install with `yaver install glab` (or brew install glab)"}
		}
		return map[string]interface{}{"error": "gh CLI not on PATH — install with `yaver install gh` (or brew install gh)"}
	}
	if !cli.Authed {
		if name == "glab" {
			return map[string]interface{}{"error": "glab is installed but not authenticated — run `glab auth login` (one-time, opens browser)"}
		}
		return map[string]interface{}{"error": "gh is installed but not authenticated — run `gh auth login` (one-time, opens browser)"}
	}
	if len(args) == 0 {
		return map[string]interface{}{"error": fmt.Sprintf("args is required (%s subcommand + flags)", name)}
	}
	if err := validateForgeCLIArgs(name, args, confirm); err != "" {
		return map[string]interface{}{"error": err, "argv": append([]string{name}, args...)}
	}
	cmd := osexec.Command(cli.Path, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	resp := map[string]interface{}{"output": string(out), "argv": append([]string{name}, args...)}
	if err != nil {
		resp["error"] = err.Error()
	}
	return resp
}

func validateForgeCLIArgs(name string, args []string, confirm bool) string {
	normalized := make([]string, 0, len(args))
	for _, arg := range args {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(arg)))
	}

	if name == "gh" {
		switch {
		case slices.Equal(normalized, []string{"auth", "token"}):
			return "gh auth token is blocked because it prints the active credential"
		case len(normalized) >= 2 && normalized[0] == "secret" && normalized[1] == "list":
			return "gh secret list is blocked on gh_run"
		case len(normalized) >= 3 && (normalized[0] == "repo" || normalized[0] == "org" || normalized[0] == "environment") && normalized[1] == "secret" && normalized[2] == "list":
			return fmt.Sprintf("gh %s secret list is blocked on gh_run", normalized[0])
		}
	}
	if name == "glab" && len(normalized) >= 3 && normalized[0] == "auth" && normalized[1] == "status" && slices.Contains(normalized[2:], "--show-token") {
		return "glab auth status --show-token is blocked because it prints the active credential"
	}
	if forgeCLIDestructive(normalized) && !confirm {
		return fmt.Sprintf("%s destructive command blocked without confirm:true", name)
	}
	return ""
}

func forgeCLIDestructive(args []string) bool {
	if len(args) >= 2 && args[0] == "repo" && args[1] == "delete" {
		return true
	}
	if len(args) >= 2 && args[0] == "api" {
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "-x", "--method":
				if i+1 < len(args) && args[i+1] == "delete" {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Write-op wrappers — opinionated tools so coding agents can DO
// repo ops with structured input rather than mass-marshaling args.
// Each is a thin shim over mcpGhRun / mcpGlabRun with input
// validation + sensible defaults. The plain *_run tools above are
// the escape hatch for anything not covered here.
// ---------------------------------------------------------------------------

func mcpGitHubPRCreate(dir, title, body, base, head string, draft bool) interface{} {
	if strings.TrimSpace(title) == "" {
		return map[string]interface{}{"error": "title is required"}
	}
	args := []string{"pr", "create", "--title", title}
	if body != "" {
		args = append(args, "--body", body)
	} else {
		args = append(args, "--body", "")
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	if head != "" {
		args = append(args, "--head", head)
	}
	if draft {
		args = append(args, "--draft")
	}
	return mcpGhRun(dir, args, false)
}

func mcpGitHubIssueCreate(dir, title, body string, labels []string) interface{} {
	if strings.TrimSpace(title) == "" {
		return map[string]interface{}{"error": "title is required"}
	}
	args := []string{"issue", "create", "--title", title, "--body", body}
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l != "" {
			args = append(args, "--label", l)
		}
	}
	return mcpGhRun(dir, args, false)
}

func mcpGitHubWorkflowRun(dir, workflow, ref string, inputs map[string]string) interface{} {
	if strings.TrimSpace(workflow) == "" {
		return map[string]interface{}{"error": "workflow is required (filename like ci.yml or workflow display name)"}
	}
	args := []string{"workflow", "run", workflow}
	if ref != "" {
		args = append(args, "--ref", ref)
	}
	for k, v := range inputs {
		args = append(args, "--field", k+"="+v)
	}
	return mcpGhRun(dir, args, false)
}

func mcpGitLabMRCreate(dir, title, description, sourceBranch, targetBranch string, draft bool) interface{} {
	if strings.TrimSpace(title) == "" {
		return map[string]interface{}{"error": "title is required"}
	}
	args := []string{"mr", "create", "--title", title, "--description", description}
	if sourceBranch != "" {
		args = append(args, "--source-branch", sourceBranch)
	}
	if targetBranch != "" {
		args = append(args, "--target-branch", targetBranch)
	}
	if draft {
		args = append(args, "--draft")
	}
	// glab won't push interactively when a TTY isn't attached; tell
	// it explicitly so non-interactive callers don't hang.
	args = append(args, "--yes")
	return mcpGlabRun(dir, args, false)
}

func mcpGitLabIssueCreate(dir, title, description string, labels []string) interface{} {
	if strings.TrimSpace(title) == "" {
		return map[string]interface{}{"error": "title is required"}
	}
	args := []string{"issue", "create", "--title", title, "--description", description}
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l != "" {
			args = append(args, "--label", l)
		}
	}
	args = append(args, "--yes")
	return mcpGlabRun(dir, args, false)
}

// Unused import guard
var _ = http.StatusOK
var _ = io.Discard
var _ = time.Now
