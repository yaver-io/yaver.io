package main

// git_push_creds_cmd.go — `yaver git push-creds <device>` and the matching
// MCP tool `git_push_creds`. Reads local GitHub/GitLab tokens via the
// existing detect helpers (gh CLI / env / git credential helper / vault)
// and applies them to one or more owned remote machines using the same
// /machine/onboarding/apply endpoint that the dashboard hits, so a fresh
// box gets clone-credentials.json + the optional CI/deploy vault entry
// without anyone re-pasting a PAT.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// repeatableFlag backs `--device <id>` (specifiable multiple times) for
// the push-creds CLI. flag.Var requires a Value implementation; this is
// the smallest one that just collects every occurrence.
type repeatableFlag []string

func (r *repeatableFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatableFlag) Set(v string) error { *r = append(*r, v); return nil }

// runGitCLI dispatches `yaver git <subcommand>`. Today only `push-creds`
// lives here; future git-related convenience commands hang off the same
// dispatcher rather than getting their own top-level case in main.go.
// (Named runGitCLI rather than runGit because git_http.go already owns
// a runGit helper that shells out to the git binary.)
func runGitCLI(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: yaver git <subcommand>")
		fmt.Println()
		fmt.Println("subcommands:")
		fmt.Println("  push-creds <device|alias> [...]   forward local GitHub/GitLab creds to one or more owned machines")
		fmt.Println("  push-creds --all                  forward to every owned online peer")
		fmt.Println("  oauth <github|gitlab> [--device <id>]   start a Device Flow on the local or a remote agent")
		os.Exit(2)
	}
	switch args[0] {
	case "push-creds":
		runGitPushCreds(args[1:])
	case "oauth":
		runGitOAuth(args[1:])
	case "-h", "--help", "help":
		runGitCLI(nil)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: yaver git %s\n", args[0])
		os.Exit(2)
	}
}

func runGitPushCreds(args []string) {
	fs := flag.NewFlagSet("git push-creds", flag.ExitOnError)
	var (
		provider   string
		gitlabHost string
		github     string
		gitlab     string
		noClone    bool
		noCI       bool
		notes      string
		all        bool
		dryRun     bool
		outJSON    bool
		devices    repeatableFlag
	)
	fs.Var(&devices, "device", "owned device id or alias (repeatable; same as positional args)")
	fs.StringVar(&provider, "provider", "all", "github | gitlab | all")
	fs.StringVar(&gitlabHost, "gitlab-host", "gitlab.com", "GitLab host (only used when provider includes gitlab)")
	fs.StringVar(&github, "github-token", "", "use this GitHub token instead of auto-detecting")
	fs.StringVar(&gitlab, "gitlab-token", "", "use this GitLab token instead of auto-detecting")
	fs.BoolVar(&noClone, "no-clone", false, "skip clone/pull credentials on the target")
	fs.BoolVar(&noCI, "no-ci", false, "skip CI/deploy vault entry on the target")
	fs.StringVar(&notes, "notes", "", "free-form note attached to the vault entry")
	fs.BoolVar(&all, "all", false, "fan out to every owned online peer (excludes this machine)")
	fs.BoolVar(&dryRun, "dry-run", false, "show what would happen without applying")
	fs.BoolVar(&outJSON, "json", false, "emit a JSON summary instead of text")
	_ = fs.Parse(args)

	requested := uniqueNonEmptyStrings(append([]string(nil), append(devices, fs.Args()...)...))
	if !all && len(requested) == 0 {
		fmt.Fprintln(os.Stderr, "git push-creds: provide at least one device id/alias, or use --all")
		os.Exit(2)
	}

	cfg := mustLoadAuthConfig()
	known, err := listDevicesEnsuringAuth(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git push-creds: cannot list devices: %v\n", err)
		os.Exit(1)
	}

	// Resolve aliases / partial IDs to full deviceIds. Drops self silently
	// (proxyToDevice would 400 with errProxyLocal) so `--all` is safe to
	// run even on a primary box that's online itself.
	selfID := strings.TrimSpace(cfg.DeviceID)
	targetIDs := make([]string, 0, len(requested))
	for _, hint := range requested {
		d, rerr := resolveDevice(hint, known)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "git push-creds: %v\n", rerr)
			os.Exit(1)
		}
		if d.DeviceID == selfID {
			fmt.Fprintf(os.Stderr, "git push-creds: skipping %s — that's this machine. Use `yaver vault` to set local creds, not push-creds.\n", hint)
			continue
		}
		targetIDs = append(targetIDs, d.DeviceID)
	}
	if all {
		for _, d := range known {
			if d.IsGuest || d.DeviceID == selfID || !d.IsOnline {
				continue
			}
			targetIDs = append(targetIDs, d.DeviceID)
		}
	}
	targetIDs = uniqueNonEmptyStrings(targetIDs)
	if len(targetIDs) == 0 {
		fmt.Fprintln(os.Stderr, "git push-creds: no eligible owned online peers to push to")
		os.Exit(1)
	}

	// Auto-detect tokens locally when not explicitly supplied. We only
	// auto-detect for the providers the user actually asked for so a
	// `--provider github` invocation doesn't quietly leak a GitLab PAT
	// from the local keychain.
	provider = strings.ToLower(strings.TrimSpace(provider))
	wantGitHub := provider == "github" || provider == "all" || provider == ""
	wantGitLab := provider == "gitlab" || provider == "all" || provider == ""
	github = strings.TrimSpace(github)
	gitlab = strings.TrimSpace(gitlab)
	if wantGitHub && github == "" {
		github = detectGitHubToken()
	}
	if wantGitLab && gitlab == "" {
		gitlab = detectGitLabToken(gitlabHost)
	}
	if !wantGitHub {
		github = ""
	}
	if !wantGitLab {
		gitlab = ""
	}
	if github == "" && gitlab == "" {
		fmt.Fprintln(os.Stderr, "git push-creds: no GitHub or GitLab tokens found locally. Pass --github-token / --gitlab-token explicitly.")
		os.Exit(1)
	}

	req := machineOnboardingApplyRequest{
		GitHubToken: github,
		GitLabToken: gitlab,
		GitLabHost:  gitlabHost,
		Notes:       notes,
	}
	if noClone {
		v := false
		req.ApplyClone = &v
	}
	if noCI {
		v := false
		req.ApplyCIToken = &v
	}

	if dryRun {
		printPushCredsDryRun(targetIDs, req, github != "", gitlab != "", outJSON)
		return
	}

	type pushResult struct {
		DeviceID string         `json:"device_id"`
		Result   map[string]any `json:"result,omitempty"`
		Error    string         `json:"error,omitempty"`
	}
	results := make([]pushResult, 0, len(targetIDs))
	for _, t := range targetIDs {
		out, err := applyMachineOnboardingRemote(t, req)
		if err != nil {
			results = append(results, pushResult{DeviceID: t, Error: err.Error()})
			continue
		}
		results = append(results, pushResult{DeviceID: t, Result: out})
	}

	if outJSON {
		b, _ := json.MarshalIndent(map[string]any{
			"applied_github": github != "",
			"applied_gitlab": gitlab != "",
			"gitlab_host":    gitlabHost,
			"results":        results,
		}, "", "  ")
		fmt.Println(string(b))
		return
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].DeviceID < results[j].DeviceID })
	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Printf("✗ %s: %s\n", shortDeviceID(r.DeviceID), r.Error)
			continue
		}
		bits := []string{}
		if github != "" {
			bits = append(bits, "github")
		}
		if gitlab != "" {
			bits = append(bits, "gitlab ("+gitlabHost+")")
		}
		fmt.Printf("✓ %s: applied %s\n", shortDeviceID(r.DeviceID), strings.Join(bits, " + "))
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func shortDeviceID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:8]
}

func printPushCredsDryRun(targets []string, req machineOnboardingApplyRequest, hasGH, hasGL bool, outJSON bool) {
	summary := map[string]any{
		"dry_run":      true,
		"targets":      targets,
		"github_token": hasGH,
		"gitlab_token": hasGL,
		"gitlab_host":  req.GitLabHost,
		"apply_clone":  boolOrDefault(req.ApplyClone, true),
		"apply_ci":     boolOrDefault(req.ApplyCIToken, true),
		"notes":        req.Notes,
	}
	if outJSON {
		b, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("DRY RUN — would apply to %d target(s):\n", len(targets))
	for _, t := range targets {
		fmt.Printf("  - %s\n", shortDeviceID(t))
	}
	if hasGH {
		fmt.Println("  GitHub token: present (auto-detected or supplied)")
	}
	if hasGL {
		fmt.Printf("  GitLab token: present (host %s)\n", req.GitLabHost)
	}
	fmt.Printf("  apply_clone=%v  apply_ci=%v\n",
		boolOrDefault(req.ApplyClone, true),
		boolOrDefault(req.ApplyCIToken, true))
}

// mcpGitPushCreds backs the `git_push_creds` MCP tool. Same semantics as
// the CLI: detect GitHub/GitLab tokens locally (unless explicitly
// supplied), forward to one or more owned remote machines via the
// existing /machine/onboarding/apply endpoint, return per-target results.
//
// `all=true` enumerates every owned online peer that isn't this machine.
type gitPushCredsMCPArgs struct {
	DeviceID    string   `json:"device_id"`
	DeviceIDs   []string `json:"device_ids"`
	All         bool     `json:"all"`
	Provider    string   `json:"provider"`
	GitLabHost  string   `json:"gitlab_host"`
	GitHubToken string   `json:"github_token"`
	GitLabToken string   `json:"gitlab_token"`
	ApplyClone  *bool    `json:"apply_clone"`
	ApplyCI     *bool    `json:"apply_ci_token"`
	Notes       string   `json:"notes"`
}

func mcpGitPushCreds(a gitPushCredsMCPArgs) interface{} {
	requested := uniqueNonEmptyStrings(append([]string{a.DeviceID}, a.DeviceIDs...))
	cfg, cerr := LoadConfig()
	if cerr != nil || cfg == nil {
		return map[string]any{"error": "not authenticated"}
	}
	selfID := strings.TrimSpace(cfg.DeviceID)
	targetIDs := append([]string(nil), requested...)
	if a.All {
		known, err := listDevicesEnsuringAuth(cfg)
		if err != nil {
			return map[string]any{"error": "cannot list devices: " + err.Error()}
		}
		for _, d := range known {
			if d.IsGuest || !d.IsOnline || d.DeviceID == selfID {
				continue
			}
			targetIDs = append(targetIDs, d.DeviceID)
		}
	}
	// Drop self if it sneaked in via a literal device_id arg.
	filtered := targetIDs[:0]
	for _, t := range targetIDs {
		if strings.TrimSpace(t) == "" || t == selfID {
			continue
		}
		filtered = append(filtered, t)
	}
	targetIDs = uniqueNonEmptyStrings(filtered)
	if len(targetIDs) == 0 {
		return map[string]any{"error": "no targets — pass device_id, device_ids, or all=true"}
	}

	provider := strings.ToLower(strings.TrimSpace(a.Provider))
	wantGitHub := provider == "github" || provider == "all" || provider == ""
	wantGitLab := provider == "gitlab" || provider == "all" || provider == ""
	gitlabHost := strings.TrimSpace(a.GitLabHost)
	if gitlabHost == "" {
		gitlabHost = "gitlab.com"
	}
	github := strings.TrimSpace(a.GitHubToken)
	gitlab := strings.TrimSpace(a.GitLabToken)
	if wantGitHub && github == "" {
		github = detectGitHubToken()
	}
	if wantGitLab && gitlab == "" {
		gitlab = detectGitLabToken(gitlabHost)
	}
	if !wantGitHub {
		github = ""
	}
	if !wantGitLab {
		gitlab = ""
	}
	if github == "" && gitlab == "" {
		return map[string]any{"error": "no GitHub or GitLab tokens found locally; pass github_token / gitlab_token explicitly"}
	}

	apply := machineOnboardingApplyRequest{
		GitHubToken:  github,
		GitLabToken:  gitlab,
		GitLabHost:   gitlabHost,
		ApplyClone:   a.ApplyClone,
		ApplyCIToken: a.ApplyCI,
		Notes:        a.Notes,
	}

	results := make([]map[string]any, 0, len(targetIDs))
	for _, t := range targetIDs {
		out, err := applyMachineOnboardingRemote(t, apply)
		entry := map[string]any{"device_id": t}
		if err != nil {
			entry["error"] = err.Error()
		} else {
			entry["result"] = out
		}
		results = append(results, entry)
	}
	return map[string]any{
		"applied_github": github != "",
		"applied_gitlab": gitlab != "",
		"gitlab_host":    gitlabHost,
		"results":        results,
	}
}
