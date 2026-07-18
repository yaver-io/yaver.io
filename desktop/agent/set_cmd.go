package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type emailOAuthSetOptions struct {
	Email         string
	Password      string
	PasswordEnv   string
	PasswordStdin bool
	ConvexURL     string
	Machine       string
	PrintToken    bool
	RequireOwner  bool
}

func runSet(args []string) {
	if len(args) == 0 {
		setUsage()
		return
	}
	switch strings.ToLower(args[0]) {
	case "emailoauth", "email-oauth", "email_password", "email-password":
		runSetEmailOAuth(context.Background(), args[1:], os.Stdin)
	case "help", "-h", "--help":
		setUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver set %s\n\n", args[0])
		setUsage()
		os.Exit(1)
	}
}

func setUsage() {
	fmt.Print(`yaver set — configure automation-friendly local settings

Usage:
  yaver set emailOauth status
  yaver set emailOauth enable --allowed-emails owner@example.com
  yaver set emailOauth disable
  yaver set emailOauth --email <email> --password-env YAVER_TEST_PASSWORD
  yaver set emailOauth --email <email> --password-stdin
  yaver set emailOauth --email <email> --password <password>

Options:
  --convex-url <url>       Convex site URL (defaults to production)
  --machine <alias>        Run token setup on a remote owned machine
  --print-token            Print the session token to stdout for wrappers
  --require-owner          Exit non-zero unless the signed-in user is an owner

This command logs in through the gated email/password auth path and stores only
the returned Yaver session token in ~/.yaver/config.json. It never stores the
raw password; keep YAVER_TEST_EMAIL/YAVER_TEST_PASSWORD in GitHub Secrets,
Convex env, or the local keychain/vault instead.

The enable/disable actions use the local Convex CLI from the yaver.io repo to
set YAVER_EMAIL_PASSWORD_AUTH_ENABLED. They require operator credentials for
the deployment and intentionally are not exposed as a backend HTTP mutation.
`)
}

func runSetEmailOAuth(ctx context.Context, args []string, stdin io.Reader) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "status":
			runSetEmailOAuthStatus(ctx)
			return
		case "enable", "on":
			runSetEmailOAuthGate(ctx, true, args[1:])
			return
		case "disable", "off":
			runSetEmailOAuthGate(ctx, false, args[1:])
			return
		}
	}

	opts, err := parseEmailOAuthSetOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(opts.Machine) != "" {
		if err := runSetEmailOAuthRemote(ctx, opts); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	password, err := resolveEmailOAuthPassword(opts, stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	token, err := LoginWithEmail(opts.ConvexURL, opts.Email, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	user, err := ValidateTokenInfo(opts.ConvexURL, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: token validation failed: %v\n", err)
		os.Exit(1)
	}
	if opts.RequireOwner && !user.IsOwner {
		fmt.Fprintf(os.Stderr, "Error: signed-in user %s is not marked as owner\n", user.Email)
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConvexSiteURL = opts.ConvexURL
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
	}
	cfg.RelayServers = nil
	cfg.RelayPassword = ""
	applyDefaultHeadlessKeepAwake(cfg)
	if err := SetAuthToken(cfg, token); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	nudgeRunningDaemonToReloadAuth()

	fmt.Printf("emailOauth configured for %s", user.Email)
	if user.IsOwner {
		fmt.Print(" (owner)")
	}
	fmt.Println(".")
	if opts.PrintToken {
		fmt.Println(token)
	}
	_ = ctx
}

func runSetEmailOAuthStatus(ctx context.Context) {
	result, err := authCapabilities(ctx, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Message)
	if len(result.RecommendedSecrets) > 0 {
		fmt.Printf("recommended secrets: %s\n", strings.Join(result.RecommendedSecrets, ", "))
	}
	fmt.Printf("raw password storage: %s\n", result.RawPasswordStorage)
}

func runSetEmailOAuthGate(ctx context.Context, enabled bool, args []string) {
	fs := flag.NewFlagSet("emailOauth gate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	allowedEmails := fs.String("allowed-emails", "", "Comma-separated allowlist")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := setConvexEmailOAuthGate(ctx, enabled, strings.TrimSpace(*allowedEmails)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if enabled {
		fmt.Println("emailOauth enabled in Convex env.")
		if strings.TrimSpace(*allowedEmails) != "" {
			fmt.Printf("allowed emails: %s\n", strings.TrimSpace(*allowedEmails))
		}
		fmt.Println("Close it after tests with: yaver set emailOauth disable")
		return
	}
	fmt.Println("emailOauth disabled in Convex env.")
}

func setConvexEmailOAuthGate(ctx context.Context, enabled bool, allowedEmails string) error {
	repoRoot, err := findYaverRepoRoot()
	if err != nil {
		return err
	}
	backendDir := filepath.Join(repoRoot, "backend")
	value := "false"
	if enabled {
		value = "true"
	}
	if err := runConvexEnvSet(ctx, backendDir, "YAVER_EMAIL_PASSWORD_AUTH_ENABLED", value); err != nil {
		return err
	}
	if enabled && strings.TrimSpace(allowedEmails) != "" {
		if err := runConvexEnvSet(ctx, backendDir, "YAVER_EMAIL_PASSWORD_AUTH_ALLOWED_EMAILS", allowedEmails); err != nil {
			return err
		}
	}
	return nil
}

func runConvexEnvSet(ctx context.Context, backendDir, key, value string) error {
	cmd := exec.CommandContext(ctx, "npx", "convex", "env", "set", key, value)
	cmd.Dir = backendDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("convex env set %s failed: %w: %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func parseEmailOAuthSetOptions(args []string) (emailOAuthSetOptions, error) {
	fs := flag.NewFlagSet("emailOauth", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := emailOAuthSetOptions{ConvexURL: defaultConvexSiteURL}
	var emailTypo string
	fs.StringVar(&opts.Email, "email", "", "Email address")
	fs.StringVar(&emailTypo, "emaikl", "", "Email address")
	fs.StringVar(&opts.Password, "password", "", "Password")
	fs.StringVar(&opts.PasswordEnv, "password-env", "", "Environment variable holding the password")
	fs.BoolVar(&opts.PasswordStdin, "password-stdin", false, "Read password from stdin")
	fs.StringVar(&opts.ConvexURL, "convex-url", defaultConvexSiteURL, "Convex site URL")
	fs.StringVar(&opts.Machine, "machine", "", "Remote machine alias, name, or device id")
	fs.BoolVar(&opts.PrintToken, "print-token", false, "Print token to stdout")
	fs.BoolVar(&opts.RequireOwner, "require-owner", false, "Require owner account")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if strings.TrimSpace(opts.Email) == "" {
		opts.Email = emailTypo
	}
	opts.Email = strings.TrimSpace(opts.Email)
	opts.PasswordEnv = strings.TrimSpace(opts.PasswordEnv)
	opts.ConvexURL = strings.TrimRight(strings.TrimSpace(opts.ConvexURL), "/")
	opts.Machine = strings.TrimSpace(opts.Machine)
	if opts.ConvexURL == "" {
		opts.ConvexURL = defaultConvexSiteURL
	}
	if opts.Email == "" {
		return opts, fmt.Errorf("--email is required")
	}
	methods := 0
	if opts.Password != "" {
		methods++
	}
	if opts.PasswordEnv != "" {
		methods++
	}
	if opts.PasswordStdin {
		methods++
	}
	if methods != 1 {
		return opts, fmt.Errorf("provide exactly one of --password, --password-env, or --password-stdin")
	}
	if opts.Machine != "" && opts.PasswordEnv == "" {
		return opts, fmt.Errorf("--machine requires --password-env so the secret is read on the target machine")
	}
	if opts.Machine != "" && opts.PrintToken {
		return opts, fmt.Errorf("--print-token cannot be combined with --machine; the remote token stays on the target")
	}
	return opts, nil
}

func runSetEmailOAuthRemote(ctx context.Context, opts emailOAuthSetOptions) error {
	cfg := mustLoadAuthConfig()
	devices, err := listDevicesEnsuringAuth(cfg)
	if err != nil {
		return err
	}
	dev, err := resolveDevice(opts.Machine, devices)
	if err != nil {
		return err
	}
	if dev.IsGuest {
		return fmt.Errorf("refusing remote emailOauth setup on shared/guest device %s", dev.Name)
	}
	remoteArgs := []string{
		"yaver", "set", "emailOauth",
		"--email", opts.Email,
		"--password-env", opts.PasswordEnv,
		"--convex-url", opts.ConvexURL,
	}
	if opts.RequireOwner {
		remoteArgs = append(remoteArgs, "--require-owner")
	}
	quoted := make([]string, 0, len(remoteArgs))
	for _, arg := range remoteArgs {
		quoted = append(quoted, shellQuoteSingle(arg))
	}
	command := "command -v yaver >/dev/null 2>&1 && " + strings.Join(quoted, " ")
	snapshot, err := remoteExecAndWait(ctx, dev.DeviceID, command, "", 120)
	if err != nil {
		return err
	}
	stdout, _ := snapshot["stdout"].(string)
	stderr, _ := snapshot["stderr"].(string)
	exitCode := emailOAuthIntFromAny(snapshot["exitCode"])
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	if exitCode != 0 {
		return fmt.Errorf("remote emailOauth setup on %s exited with code %d", dev.Name, exitCode)
	}
	fmt.Printf("Remote emailOauth configured on %s (%s).\n", dev.Name, dev.DeviceID[:8])
	return nil
}

func remoteExecAndWait(ctx context.Context, deviceID, command, workDir string, timeout int) (map[string]any, error) {
	body := map[string]any{"command": command, "workDir": workDir, "timeout": timeout}
	status, raw, err := proxyToDevice(ctx, "exec_command", deviceID, http.MethodPost, "/exec", mustJSONBytes(body))
	if err != nil {
		return nil, fmt.Errorf("start remote exec: %w", err)
	}
	if status >= 300 {
		return nil, fmt.Errorf("start remote exec returned %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var started map[string]any
	if err := json.Unmarshal(raw, &started); err != nil {
		return nil, fmt.Errorf("decode remote exec start: %w", err)
	}
	execID, _ := started["execId"].(string)
	if execID == "" {
		return nil, fmt.Errorf("remote exec did not return execId")
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		status, raw, err = proxyToDevice(ctx, "exec_command", deviceID, http.MethodGet, "/exec/"+execID, nil)
		if err != nil {
			return nil, fmt.Errorf("poll remote exec: %w", err)
		}
		if status >= 300 {
			return nil, fmt.Errorf("poll remote exec returned %d: %s", status, strings.TrimSpace(string(raw)))
		}
		var wrapped map[string]any
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, fmt.Errorf("decode remote exec snapshot: %w", err)
		}
		snapshot, _ := wrapped["exec"].(map[string]any)
		if snapshot == nil {
			snapshot = wrapped
		}
		if fmt.Sprint(snapshot["status"]) != "running" {
			return snapshot, nil
		}
		if time.Now().After(deadline) {
			return snapshot, fmt.Errorf("remote exec timed out after %ds", timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func emailOAuthIntFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func resolveEmailOAuthPassword(opts emailOAuthSetOptions, stdin io.Reader) (string, error) {
	switch {
	case opts.Password != "":
		return opts.Password, nil
	case opts.PasswordEnv != "":
		value := os.Getenv(opts.PasswordEnv)
		if value == "" {
			return "", fmt.Errorf("environment variable %s is empty", opts.PasswordEnv)
		}
		return value, nil
	case opts.PasswordStdin:
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return "", fmt.Errorf("password from stdin is empty")
		}
		return password, nil
	default:
		return "", fmt.Errorf("missing password")
	}
}
