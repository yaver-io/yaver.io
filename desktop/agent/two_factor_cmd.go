package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// two_factor_cmd.go — CLI wrapper for the Convex TOTP endpoints.
//
// Design rules (see project instructions):
//   - Strictly OPTIONAL. Default off. Nothing changes for users who never
//     run `yaver 2fa enable`.
//   - Gate is at SESSION ISSUANCE only. Per-request traffic (QUIC / relay /
//     Tailscale / Cloudflare Tunnel) is never touched. The phone-at-beach
//     flow using an existing session token keeps working regardless.
//   - Every existing recovery path stays intact. The TOTP flow adds a
//     challenge on top of OAuth/email login; when 2FA is enabled, the
//     user can still complete the challenge with one of the recovery
//     codes emitted at enrollment. Device-code recovery, browser-based
//     OAuth, and the bootstrap pairing flow are all untouched.
//
// This file only knows how to drive the Convex endpoints — the actual
// TOTP algorithm lives in backend/convex/totp.ts. The Yaver agent never
// stores TOTP secrets or recovery codes; the user manages the secret in
// their authenticator app (Microsoft Authenticator, Google Authenticator,
// 1Password, Authy, etc.) and keeps the recovery codes themselves.

func runTwoFactor(args []string) {
	if len(args) == 0 {
		printTwoFactorHelp()
		return
	}
	switch args[0] {
	case "status":
		runTwoFactorStatus()
	case "enable", "setup":
		runTwoFactorEnable()
	case "disable", "off":
		runTwoFactorDisable(args[1:])
	case "help", "-h", "--help":
		printTwoFactorHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown 2fa subcommand: %s\n", args[0])
		printTwoFactorHelp()
		os.Exit(1)
	}
}

func printTwoFactorHelp() {
	fmt.Println(`yaver 2fa — two-factor authentication (optional)

Usage:
  yaver 2fa status         Show whether 2FA is enabled for your account
  yaver 2fa enable         Enroll a TOTP app (Microsoft/Google Authenticator, 1Password…)
  yaver 2fa disable        Remove 2FA from your account (requires a current code)

Notes:
  - 2FA is optional. If you never run 'enable', nothing changes.
  - Enrolling only adds a challenge at sign-in time. Existing relay/Tailscale
    sessions and the QUIC channel to your dev box are not affected.
  - At enrollment you receive 8 recovery codes. Keep them somewhere safe —
    each works once if you ever lose your authenticator device.
  - Disabling requires a current TOTP code from your authenticator, not a
    recovery code, to prevent an attacker with only a recovery-code leak from
    turning 2FA off on your behalf.`)
}

func runTwoFactorStatus() {
	cfg, err := requireTwoFactorAuth()
	if err != nil {
		fatalTwoFactor(err)
	}
	var out struct {
		Enabled                 bool `json:"enabled"`
		RecoveryCodesRemaining  int  `json:"recoveryCodesRemaining"`
	}
	if err := twoFactorConvexCall(cfg, http.MethodGet, "/auth/totp/status", nil, &out); err != nil {
		fatalTwoFactor(err)
	}
	if out.Enabled {
		fmt.Printf("2FA: enabled (%d recovery codes remaining)\n", out.RecoveryCodesRemaining)
	} else {
		fmt.Println("2FA: not enabled")
	}
}

func runTwoFactorEnable() {
	cfg, err := requireTwoFactorAuth()
	if err != nil {
		fatalTwoFactor(err)
	}

	var status struct {
		Enabled bool `json:"enabled"`
	}
	if err := twoFactorConvexCall(cfg, http.MethodGet, "/auth/totp/status", nil, &status); err == nil && status.Enabled {
		fmt.Println("2FA is already enabled. Run `yaver 2fa disable` first if you want to re-enroll.")
		return
	}

	var setup struct {
		Secret      string `json:"secret"`
		OtpAuthURL  string `json:"otpAuthUrl"`
	}
	if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/setup", nil, &setup); err != nil {
		fatalTwoFactor(err)
	}

	fmt.Println()
	fmt.Println("Scan the QR code with your authenticator app")
	fmt.Println("(Microsoft Authenticator, Google Authenticator, 1Password, Authy, …):")
	fmt.Println()
	qrterminal.GenerateHalfBlock(setup.OtpAuthURL, qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("Or enter this secret manually: %s\n", groupTwoFactorSecret(setup.Secret))
	fmt.Println()

	fmt.Print("Enter the 6-digit code from your authenticator: ")
	reader := bufio.NewReader(os.Stdin)
	codeLine, _ := reader.ReadString('\n')
	code := strings.TrimSpace(codeLine)
	if code == "" {
		fmt.Println("aborted — no code entered")
		os.Exit(1)
	}

	var enable struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	body := map[string]string{"code": code}
	if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/enable", body, &enable); err != nil {
		fatalTwoFactor(err)
	}

	fmt.Println()
	fmt.Println("✓ 2FA enabled")
	fmt.Println()
	fmt.Println("Recovery codes — write these down NOW. They will not be shown again.")
	fmt.Println("Each code works once if you lose access to your authenticator.")
	fmt.Println()
	for i, rc := range enable.RecoveryCodes {
		fmt.Printf("  %2d. %s\n", i+1, rc)
	}
	fmt.Println()
	fmt.Println("Your existing session token still works. Next sign-in on a new")
	fmt.Println("device will prompt for a TOTP code.")
}

func runTwoFactorDisable(args []string) {
	cfg, err := requireTwoFactorAuth()
	if err != nil {
		fatalTwoFactor(err)
	}

	var code string
	if len(args) > 0 {
		code = strings.TrimSpace(args[0])
	}
	if code == "" {
		fmt.Print("Enter a current 6-digit code from your authenticator to disable 2FA: ")
		reader := bufio.NewReader(os.Stdin)
		codeLine, _ := reader.ReadString('\n')
		code = strings.TrimSpace(codeLine)
	}
	if code == "" {
		fmt.Println("aborted — no code entered")
		os.Exit(1)
	}

	body := map[string]string{"code": code}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/disable", body, &out); err != nil {
		fatalTwoFactor(err)
	}
	fmt.Println("✓ 2FA disabled")
}

// ── Helpers ────────────────────────────────────────────────────────────

func requireTwoFactorAuth() (*Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not signed in — run `yaver auth` first")
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return nil, fmt.Errorf("missing Convex URL in config — run `yaver auth`")
	}
	return cfg, nil
}

// twoFactorConvexCall POSTs/GETs the Convex TOTP endpoints with the current
// user session token. The call is intentionally short-lived and never
// reaches the local agent — TOTP is a pure Convex concept.
func twoFactorConvexCall(cfg *Config, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, strings.TrimRight(cfg.ConvexSiteURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return parseTwoFactorError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func parseTwoFactorError(status int, raw []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &e); err == nil && e.Error != "" {
		if strings.Contains(e.Error, "INVALID_CODE") {
			return fmt.Errorf("the 6-digit code did not match — the code rotates every 30s, try the latest one")
		}
		return fmt.Errorf("%s", e.Error)
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

// groupTwoFactorSecret renders the base32 secret in 4-char groups so it's
// easier for humans to read off a screen.
func groupTwoFactorSecret(secret string) string {
	s := strings.ToUpper(strings.TrimSpace(secret))
	var out strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func fatalTwoFactor(err error) {
	fmt.Fprintf(os.Stderr, "yaver 2fa: %v\n", err)
	os.Exit(1)
}
