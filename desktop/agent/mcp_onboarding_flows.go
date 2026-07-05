package main

import (
	"context"
	"encoding/json"
	"strings"
)

type yaverSelfHostOnboardingArgs struct {
	RepoQuery       string `json:"repo_query"`
	Runner          string `json:"runner"`
	GitProvider     string `json:"git_provider"`
	StartGitOAuth   bool   `json:"start_git_oauth"`
	IncludeCloudCTA bool   `json:"include_cloud_cta"`
}

type yaverManagedCloudOnboardingArgs struct {
	RepoQuery       string `json:"repo_query"`
	MachineType     string `json:"machine_type"`
	Region          string `json:"region"`
	ConfirmCheckout bool   `json:"confirm_checkout"`
	AcceptCost      bool   `json:"accept_cost"`
	StartGitOAuth   bool   `json:"start_git_oauth"`
	GitProvider     string `json:"git_provider"`
}

func mcpYaverSelfHostOnboarding(args yaverSelfHostOnboardingArgs) map[string]interface{} {
	cfg, _ := LoadConfig()
	status := collectMachineOnboardingStatus()

	out := map[string]interface{}{
		"ok":        true,
		"flow":      "self-hosted",
		"audience":  "normie-friendly MCP setup for a user's own Mac/Linux/Windows/WSL/VPS",
		"installed": true,
		"auth": map[string]interface{}{
			"configured": cfg != nil && strings.TrimSpace(cfg.AuthToken) != "",
			"next":       "If false, run `yaver auth` or ask the user to open the returned headless auth URL/code from `yaver auth --headless`.",
		},
		"serve": map[string]interface{}{
			"device_registered": cfg != nil && strings.TrimSpace(cfg.DeviceID) != "",
			"command":           "yaver serve",
			"next":              "Keep the agent running; on Linux it installs a user systemd unit, and on macOS use launchd daemon setup for headless boxes.",
		},
		"mobile": map[string]interface{}{
			"goal": "Pair the phone as the control plane.",
			"next": "Open Yaver on the phone, sign in with the same account, then select this machine from Devices. If the box is in bootstrap mode, use the displayed pair code/link.",
		},
		"repo": map[string]interface{}{
			"requested": args.RepoQuery,
			"next":      "Use `code_repos` to list candidate repos, then `code_repo_set` with the selected repo. For a brand-new phone-started app, use project_wizard/project_new_quick or mobile project creation first.",
		},
		"runner": map[string]interface{}{
			"requested": firstNonEmpty(args.Runner, "codex|claude-code|opencode"),
			"next":      "Use `runner_auth_setup` for API-key based setup, or `runner_auth_browser_start` for browser/OAuth runner setup. Keep Yaver registered as the runner MCP server.",
		},
		"git": map[string]interface{}{
			"providers": status.Providers,
			"next":      "For least friction, use `git_oauth_start` and `git_oauth_status` so the user approves GitHub/GitLab in a browser instead of pasting a PAT. Then use `machine_onboarding_status` to verify clone/deploy readiness.",
		},
		"integrations": yaverIntegrationOnboardingGuide("self-hosted"),
		"agent_next_steps": []map[string]interface{}{
			{"tool": "yaver_onboard", "why": "show the legacy checklist for anything missing"},
			{"tool": "machine_onboarding_status", "why": "inspect OpenAI/GitHub/GitLab readiness"},
			{"tool": "code_repos", "why": "let the user pick an existing repo/project"},
			{"tool": "code_repo_set", "why": "bind Yaver Code to the selected repo"},
			{"tool": "runner_auth_setup", "why": "install/auth the selected AI runner and register Yaver MCP"},
		},
	}
	if args.IncludeCloudCTA {
		out["cloud_upgrade"] = map[string]interface{}{
			"pitch": "When the user wants an always-on/private remote box, call `yaver_managed_cloud_onboarding` with confirm_checkout=false first, then require explicit cost acceptance before checkout.",
			"tool":  "yaver_managed_cloud_onboarding",
		}
	}
	if args.StartGitOAuth {
		out["git_oauth"] = startOnboardingGitOAuth(args.GitProvider)
	}
	return out
}

func mcpYaverManagedCloudOnboarding(args yaverManagedCloudOnboardingArgs) map[string]interface{} {
	if strings.TrimSpace(args.MachineType) == "" {
		args.MachineType = "cpu"
	}
	if strings.TrimSpace(args.Region) == "" {
		args.Region = "eu"
	}

	status := opsCloudStatusHandler(OpsContext{}, nil)
	out := map[string]interface{}{
		"ok":           true,
		"flow":         "managed-cloud",
		"machine_type": args.MachineType,
		"region":       args.Region,
		"repo": map[string]interface{}{
			"requested": args.RepoQuery,
			"next":      "After a cloud machine appears, use `git_push_creds` or `machine_onboarding_apply` for clone credentials, then `code_attach`, `code_repos`, `code_repo_set`, and `code_deploy`.",
		},
		"cost_guardrail": map[string]interface{}{
			"requires_explicit_acceptance": true,
			"message":                      "Managed cloud may create a billable Yaver Hetzner machine. Call again with confirm_checkout=true and accept_cost=true only after the user explicitly approves the displayed cost/cap in the UI or chat.",
		},
		"current_status": status,
		"integrations":   yaverIntegrationOnboardingGuide("managed-cloud"),
		"post_purchase_onboarding": []map[string]interface{}{
			{"step": "wait_for_machine", "tool": "yaver_managed_cloud_onboarding", "detail": "poll with confirm_checkout=false until a managed machine is listed"},
			{"step": "sync_git", "tool": "git_push_creds", "detail": "push local GitHub/GitLab clone/deploy creds to the new managed box; tokens never go to Convex"},
			{"step": "runner_auth", "tool": "runner_auth_setup", "detail": "configure Codex/Claude/opencode on the managed box"},
			{"step": "select_repo", "tool": "code_repos + code_repo_set", "detail": "let the user choose which repo/app this box should own"},
			{"step": "deploy", "tool": "code_deploy", "detail": "deploy the selected repo from the managed cloud target"},
		},
	}

	if args.StartGitOAuth {
		out["git_oauth"] = startOnboardingGitOAuth(args.GitProvider)
	}
	if args.ConfirmCheckout {
		if !args.AcceptCost {
			out["ok"] = false
			out["checkout_error"] = "confirm_checkout=true requires accept_cost=true after explicit user approval"
			return out
		}
		payload, _ := json.Marshal(map[string]string{
			"machineType": args.MachineType,
			"region":      args.Region,
		})
		out["checkout"] = opsCloudCheckoutHandler(OpsContext{}, payload)
	}
	return out
}

func startOnboardingGitOAuth(provider string) map[string]interface{} {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || provider == "auto" {
		provider = "github"
	}
	if provider != "github" && provider != "gitlab" {
		return map[string]interface{}{"ok": false, "error": "git_provider must be github or gitlab"}
	}
	sess, err := startGitOAuthDevice(context.Background(), provider, "")
	if err != nil {
		return map[string]interface{}{
			"ok":       false,
			"provider": provider,
			"error":    err.Error(),
			"fallback": "Ask the user to create a fine-grained PAT, then call machine_onboarding_apply with github_token/gitlab_token. Prefer Device Flow once a Yaver OAuth client id is configured.",
		}
	}
	return map[string]interface{}{
		"ok":               true,
		"provider":         sess.Provider,
		"host":             sess.Host,
		"session_id":       sess.ID,
		"user_code":        sess.UserCode,
		"verification_uri": sess.VerificationURI,
		"interval":         sess.Interval,
		"expires_at":       sess.ExpiresAt.Unix(),
		"next":             "Show verification_uri and user_code to the user, then poll git_oauth_status with session_id.",
	}
}

func yaverIntegrationOnboardingGuide(runtime string) map[string]interface{} {
	remoteNote := "For remote runtime use, connect on the target box or push credentials peer-to-peer; car/watch/TV never receive provider tokens."
	if runtime == "self-hosted" {
		remoteNote = "For self-hosted runtime use, connect on the machine that will execute the ops. Car/watch/TV only call Yaver verbs."
	}
	return map[string]interface{}{
		"principle": remoteNote,
		"surfaces":  []string{"car", "watch", "tv", "mobile", "mcp", "cli"},
		"providers": []map[string]interface{}{
			{
				"id":          "google",
				"label":       "Google: Gmail, Calendar, Meet",
				"connect":     "Configure Google/Gmail OAuth in Yaver email settings or gateway OAuth; authorize Gmail + Calendar scopes.",
				"unlocks":     []string{"mail_search", "mail_unread", "meeting_next", "meeting_join_next"},
				"remote_note": "Meet links come from Google Calendar conference data and open through the selected Yaver runtime.",
			},
			{
				"id":          "microsoft",
				"label":       "Microsoft 365: Outlook mail, Calendar, Teams",
				"connect":     "Configure Microsoft OAuth / Graph credentials in Yaver email settings or gateway OAuth.",
				"unlocks":     []string{"mail_search", "mail_unread", "meeting_next", "meeting_join_next"},
				"remote_note": "Teams links come from Microsoft Graph calendar events and open through the selected Yaver runtime.",
			},
			{
				"id":          "zoom",
				"label":       "Zoom",
				"connect":     "Current path is join-link based from calendar/local bookings. Full Zoom OAuth/API control is a planned connector.",
				"unlocks":     []string{"meeting_next", "meeting_join_next", "meeting_open_url"},
				"remote_note": "Zoom opens as an official Zoom/web link from the selected runtime.",
			},
			{
				"id":          "github",
				"label":       "GitHub: repos, PRs, issues, Actions",
				"connect":     "Use git_oauth_start/git_oauth_status or git_connect for Device Flow; use git_push_creds/git_push to move creds to an owned remote box.",
				"unlocks":     []string{"git_connect", "git_push", "git_prs", "git_issues", "git_ci_status", "github_prs", "github_issues", "github_ci_status", "gh_run"},
				"remote_note": "Tokens are stored on the executing box in Yaver/git credential storage and do not transit Convex.",
			},
			{
				"id":          "gitlab",
				"label":       "GitLab: repos, MRs, issues, pipelines",
				"connect":     "Use git_oauth_start/git_oauth_status or git_connect for Device Flow; use git_push_creds/git_push to move creds to an owned remote box.",
				"unlocks":     []string{"git_connect", "git_push", "git_prs", "git_issues", "git_ci_status", "gitlab_mrs", "gitlab_issues", "gitlab_ci", "glab_run"},
				"remote_note": "Self-managed GitLab is supported by passing the host when connecting.",
			},
		},
		"recommended_sequence": []map[string]interface{}{
			{"step": "pick_runtime", "detail": "Choose the box that should execute integrations from car/watch/TV."},
			{"step": "connect_identity", "detail": "Run provider OAuth/device flow on that box, or push existing creds to it via Yaver peer-to-peer."},
			{"step": "verify", "detail": "Call a read-only verb first: mail_unread, meeting_next, git_prs, or git_ci_status."},
			{"step": "enable_voice", "detail": "Car/watch/TV commands call provider-neutral Yaver verbs, not raw provider APIs."},
		},
	}
}
