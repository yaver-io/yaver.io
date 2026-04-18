package main

// account_cmd.go — `yaver account` CLI subcommand.
//
// Mirrors what the web SettingsView / mobile Settings / MCP auth-linking
// tools expose. Useful when the user is at a terminal (no GUI handy) and
// wants to inspect or modify which OAuth providers are linked to their
// Yaver account, or fold two Yaver accounts together.
//
// Shape:
//
//   yaver account providers         # list linked providers + emails
//   yaver account link <provider>   # connect Google/Apple/Microsoft
//   yaver account unlink <provider> # disconnect, refusing if it's the only one
//   yaver account merge start       # mint an approval URL for manual merge
//   yaver account merge status <t>  # check status of a merge token
//   yaver account merge cancel <t>  # cancel a pending merge (target-side)
//
// All commands use the existing ~/.yaver/config.json auth token. If the
// user isn't signed in we say so and bail — no silent no-ops.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func runAccount(args []string) {
	if len(args) == 0 {
		accountUsage()
		return
	}
	ctx := context.Background()
	switch args[0] {
	case "providers", "list", "ls":
		runAccountProviders(ctx)
	case "link":
		runAccountLink(ctx, args[1:])
	case "unlink":
		runAccountUnlink(ctx, args[1:])
	case "merge":
		runAccountMerge(ctx, args[1:])
	case "help", "-h", "--help":
		accountUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver account %s\n\n", args[0])
		accountUsage()
		os.Exit(1)
	}
}

func accountUsage() {
	fmt.Print(`yaver account — manage OAuth sign-in methods and account merges

Usage:
  yaver account providers                   List linked sign-in methods
  yaver account link <apple|google|microsoft|msft>
                                            Open browser to link another provider
  yaver account unlink <provider> [--totp <code>]
                                            Remove a linked provider
  yaver account merge start [--totp <code>] Start a merge intent; prints approval URL
  yaver account merge status <token>        Check merge status
  yaver account merge cancel <token>        Cancel a pending merge (target side)

Signed in as the account in ~/.yaver/config.json. Run 'yaver auth' first
if no signed-in session is present.
`)
}

func runAccountProviders(ctx context.Context) {
	result, err := authListIdentities(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if result.Count == 0 {
		fmt.Println("No linked providers yet.")
		return
	}
	fmt.Printf("%d sign-in method(s) linked to this account:\n\n", result.Count)
	for _, id := range result.Identities {
		marker := "  "
		if id.IsPrimary {
			marker = "* "
		}
		line := fmt.Sprintf("%s%-10s", marker, id.Provider)
		if id.Email != "" {
			line += " — " + id.Email
		}
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("* = primary. Run 'yaver account link <provider>' to connect another.")
}

func runAccountLink(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver account link <apple|google|microsoft|msft>")
		os.Exit(1)
	}
	provider := strings.ToLower(args[0])
	if provider == "msft" {
		provider = "microsoft"
	}
	result, err := authLinkStart(ctx, provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Printf("Open this URL on any device with a browser:\n\n  %s\n\n", result.URL)
	fmt.Println("Or scan the QR code:")
	fmt.Print(result.QRASCII)
	fmt.Println()
	fmt.Printf("Waiting up to 2 minutes for %s to be linked…\n", provider)
	// Try to open in the default browser for local convenience.
	_ = accountOpenBrowser(result.URL)
	waitResult, err := authLinkWait(ctx, provider, 120, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if waitResult.Status == "linked" {
		fmt.Printf("\n  %s\n", waitResult.Message)
		return
	}
	fmt.Printf("\n  %s\n", waitResult.Message)
	os.Exit(2)
}

func runAccountUnlink(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver account unlink <provider> [--totp <code>]")
		os.Exit(1)
	}
	provider := strings.ToLower(args[0])
	totpCode := parseFlag(args[1:], "--totp")
	// The MCP helper doesn't surface the totp param, so we talk to the
	// HTTP endpoint directly when one is supplied. Without --totp we go
	// through the existing helper; if the backend responds 412 we retry
	// with a one-shot prompt for the code.
	if totpCode == "" {
		result, err := authUnlink(ctx, provider, "")
		if err == nil {
			fmt.Println(result.Message)
			if !result.OK {
				os.Exit(1)
			}
			return
		}
		// Treat 412 TOTP_REQUIRED as "prompt and retry" — otherwise fall
		// through to the error.
		if !strings.Contains(err.Error(), "HTTP 412") {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print("2FA enabled on this account. Enter your 6-digit code: ")
		var code string
		_, _ = fmt.Scanln(&code)
		totpCode = strings.TrimSpace(code)
	}
	// Direct DELETE with totpCode in the body
	if err := unlinkProviderWithTotp(ctx, provider, totpCode); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s unlinked.\n", provider)
}

func runAccountMerge(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver account merge <start|status|cancel> [...]")
		os.Exit(1)
	}
	switch args[0] {
	case "start":
		totpCode := parseFlag(args[1:], "--totp")
		result, err := startMergeWithTotp(ctx, totpCode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Printf("Merge request created. Target account: %s\n\n", result.TargetEmail)
		fmt.Println("Open this URL in a browser where the OTHER Yaver account is signed in:")
		fmt.Printf("\n  %s\n\n", result.ApprovalURL)
		fmt.Printf("Expires: %s\n", time.UnixMilli(result.ExpiresAtMs).Format(time.RFC1123))
		fmt.Println()
		fmt.Println("Watch with:  yaver account merge status " + result.MergeToken)
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver account merge status <mergeToken>")
			os.Exit(1)
		}
		result, err := authMergeStatus(ctx, args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Status:  %s\n", result.Status)
		if result.TargetEmail != "" {
			fmt.Printf("Target:  %s\n", result.TargetEmail)
		}
		fmt.Printf("Message: %s\n", result.Message)
	case "cancel":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver account merge cancel <mergeToken>")
			os.Exit(1)
		}
		if err := cancelMergeFromCLI(ctx, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Merge intent cancelled.")
	default:
		fmt.Fprintf(os.Stderr, "Unknown merge command: %s\n", args[0])
		os.Exit(1)
	}
}

// ── Thin direct-HTTP wrappers so we can pass totpCode where the MCP
//    helpers don't currently support it. Small enough that duplicating
//    them here is cheaper than plumbing options through the helpers.

func unlinkProviderWithTotp(ctx context.Context, provider, totpCode string) error {
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return err
	}
	body := map[string]string{}
	if totpCode != "" {
		body["totpCode"] = totpCode
	}
	resp, err := authedRequest(ctx, "DELETE", convexURL+"/auth/oauth-link/"+provider, token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d — %s", resp.StatusCode, readAllString(resp.Body))
	}
	return nil
}

func startMergeWithTotp(ctx context.Context, totpCode string) (AuthMergeStartResult, error) {
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return AuthMergeStartResult{}, err
	}
	body := map[string]string{"client": "cli"}
	if totpCode != "" {
		body["totpCode"] = totpCode
	}
	resp, err := authedRequest(ctx, "POST", convexURL+"/auth/account/merge/start", token, body)
	if err != nil {
		return AuthMergeStartResult{}, err
	}
	type payload struct {
		MergeToken  string `json:"mergeToken"`
		ExpiresAt   int64  `json:"expiresAt"`
		TargetEmail string `json:"targetEmail"`
	}
	parsed, raw, err := decodeAuthedJSONBody[payload](resp)
	if err != nil {
		return AuthMergeStartResult{}, fmt.Errorf("%v (%s)", err, raw)
	}
	url := webBaseURL() + "/account/merge?token=" + parsed.MergeToken
	return AuthMergeStartResult{
		MergeToken:  parsed.MergeToken,
		ApprovalURL: url,
		ExpiresAtMs: parsed.ExpiresAt,
		TargetEmail: parsed.TargetEmail,
	}, nil
}

func cancelMergeFromCLI(ctx context.Context, mergeToken string) error {
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return err
	}
	resp, err := authedRequest(ctx, "POST", convexURL+"/auth/account/merge/cancel", token, map[string]string{
		"mergeToken": mergeToken,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d — %s", resp.StatusCode, readAllString(resp.Body))
	}
	return nil
}

// ── tiny helpers ------------------------------------------------------

func parseFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return ""
}

func readAllString(r interface{ Read([]byte) (int, error) }) string {
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}

// accountOpenBrowser opens the URL in the user's default browser on a
// best-effort basis. Silent failures are fine — we already printed the
// URL so the user can copy it manually. Named to avoid colliding with
// main.go's existing openBrowser helper.
func accountOpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
