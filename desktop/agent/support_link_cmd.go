package main

// support_link_cmd.go — CLI for Yaver Support Links (docs/mesh-support-link.md).
//
// Distinct from the in-memory `support start` (which exposes THIS machine):
// a support LINK is a Convex-backed shareable URL that, when a FRIEND redeems
// it on their machine (`yaver join <code>`), grants the SENDER access to the
// friend's box over the mesh. The supporter then helps via their own AI-wrapped
// CLI (`yaver code --attach`, ghost, ssh).
//
//   yaver support link [--terminal] [--desktop] [--ttl 24h] [--label X] [--reusable]
//   yaver support connections
//   yaver support drop <grantId>
//   yaver support deny-all
//   yaver join <code>        # friend side: consent + redeem + mesh up

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func supportWebBaseURL(cfg *Config) string {
	if cfg != nil && strings.TrimSpace(cfg.WebBaseURL) != "" {
		return strings.TrimRight(cfg.WebBaseURL, "/")
	}
	return "https://yaver.io"
}

// runSupportLink mints a support link to send to a friend.
func runSupportLink(args []string) {
	fs := flag.NewFlagSet("support link", flag.ExitOnError)
	terminal := fs.Bool("terminal", false, "offer terminal/AI-task access (friend opts in at consent)")
	desktop := fs.Bool("desktop", false, "offer desktop control (friend opts in at consent)")
	ttl := fs.Int("ttl", 24, "suggested support session length in hours")
	label := fs.String("label", "", "label, e.g. \"mom's laptop\"")
	reusable := fs.Bool("reusable", false, "allow the link to be redeemed more than once")
	_ = fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		fmt.Fprintln(os.Stderr, "Error: not signed in. Run `yaver auth` first.")
		os.Exit(1)
	}
	raw, err := meshConvexCall(cfg, "mutation", "support_link:createSupportInvite", map[string]interface{}{
		"offerTerminal":       *terminal,
		"offerDesktopControl": *desktop,
		"defaultTtlHours":     *ttl,
		"label":               *label,
		"singleUse":           !*reusable,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var resp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &resp)
	url := fmt.Sprintf("%s/j/%s", supportWebBaseURL(cfg), resp.Code)
	fmt.Println("✓ Support link created — send this to your friend:")
	fmt.Println()
	fmt.Printf("    %s\n", url)
	fmt.Println()
	fmt.Println("  They open it → installs Yaver → signs in → approves access →")
	fmt.Println("  their machine joins your mesh. Then support them with:")
	fmt.Println("      yaver devices                 # see their box")
	fmt.Println("      yaver code --attach <device>  # your AI agent, their machine")
	offers := []string{"view + files"}
	if *terminal {
		offers = append(offers, "terminal (opt-in)")
	}
	if *desktop {
		offers = append(offers, "desktop control (opt-in)")
	}
	fmt.Printf("\n  Offers: %s · valid 24h · %s\n", strings.Join(offers, ", "),
		map[bool]string{true: "reusable", false: "single-use"}[*reusable])
}

func runSupportConnections() {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Error: not signed in.")
		os.Exit(1)
	}
	raw, err := meshConvexCall(cfg, "query", "support_link:listSupportConnections", map[string]interface{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var resp struct {
		Supporting  []supportConnRow `json:"supporting"`
		SupportedBy []supportConnRow `json:"supportedBy"`
	}
	_ = json.Unmarshal(raw, &resp)
	fmt.Println("Machines you can support:")
	if len(resp.Supporting) == 0 {
		fmt.Println("  (none — send a link with `yaver support link`)")
	}
	for _, c := range resp.Supporting {
		fmt.Printf("  - %s  device=%s  %s%s\n", c.CounterpartName, dashIfEmpty(c.DeviceID),
			expiryLabel(c.ExpiresAt), desktopLabel(c.AllowDesktopControl))
	}
	fmt.Println("\nWho can access THIS account's machines:")
	if len(resp.SupportedBy) == 0 {
		fmt.Println("  (nobody)")
	}
	for _, c := range resp.SupportedBy {
		fmt.Printf("  - %s  grant=%s  %s\n", c.CounterpartName, c.GrantID, expiryLabel(c.ExpiresAt))
	}
	if len(resp.SupportedBy) > 0 {
		fmt.Println("\n  Cut all access instantly: yaver support deny-all")
	}
}

type supportConnRow struct {
	GrantID             string `json:"grantId"`
	DeviceID            string `json:"deviceId"`
	CounterpartName     string `json:"counterpartName"`
	AllowDesktopControl bool   `json:"allowDesktopControl"`
	ExpiresAt           *int64 `json:"expiresAt"`
}

func runSupportDrop(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver support drop <grantId>")
		os.Exit(1)
	}
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Error: not signed in.")
		os.Exit(1)
	}
	if _, err := meshConvexCall(cfg, "mutation", "support_link:revokeSupportGrant", map[string]interface{}{
		"grantId": args[0],
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Support grant revoked. Access drops within ~20s (mesh reconcile).")
}

func runSupportDenyAll() {
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Error: not signed in.")
		os.Exit(1)
	}
	if _, err := meshConvexCall(cfg, "mutation", "support_link:denyAllSupport", map[string]interface{}{}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Revoked ALL access into your machines. Nobody can reach them now.")
}

// runJoin is the friend side: consent + redeem + mesh up.
func runJoin(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver join <code>   (from a support link yaver.io/j/<code>)")
		os.Exit(1)
	}
	code := strings.TrimSpace(args[0])
	// Accept a full URL too.
	if i := strings.LastIndex(code, "/j/"); i >= 0 {
		code = code[i+3:]
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		fmt.Println("You need to sign in first (one tap):")
		fmt.Println("    yaver auth")
		fmt.Println("then re-run:")
		fmt.Printf("    yaver join %s\n", code)
		os.Exit(1)
	}
	if cfg.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "Error: this device isn't registered yet. Run `yaver serve` once, then `yaver join`.")
		os.Exit(1)
	}

	// Fetch what the link offers so the consent prompt is accurate.
	info := fetchSupportInviteInfo(cfg, code)
	if info == nil || !info.Valid {
		fmt.Fprintln(os.Stderr, "This support link is invalid or expired. Ask your supporter for a new one.")
		os.Exit(1)
	}

	// Consent (CLI prompt; the local web console offers a GUI equivalent).
	fmt.Println()
	fmt.Printf("  %s wants to help you on this computer.\n", info.Inviter.Name)
	if info.Inviter.Email != "" {
		fmt.Printf("  (%s)\n", info.Inviter.Email)
	}
	fmt.Println()
	fmt.Println("  They will be able to: see status + read files on this machine.")
	allowTerminal := false
	allowDesktop := false
	if info.OfferTerminal {
		allowTerminal = askYesNo("  Also allow them to run commands / an AI agent here?", false)
	}
	if info.OfferDesktopControl {
		allowDesktop = askYesNo("  Also allow them to control your screen + keyboard?", false)
	}
	persistent := !askYesNo(fmt.Sprintf("  Limit access to %dh? (No = until you revoke)", info.DefaultTtlHours), true)
	if !askYesNo(fmt.Sprintf("  Allow %s now?", info.Inviter.Name), true) {
		fmt.Println("  Cancelled. No access was granted.")
		return
	}

	raw, err := meshConvexCall(cfg, "mutation", "support_link:redeemSupportInvite", map[string]interface{}{
		"code":                code,
		"deviceId":            cfg.DeviceID,
		"allowTerminal":       allowTerminal,
		"allowDesktopControl": allowDesktop,
		"persistent":          persistent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	var rr struct {
		InviterName string `json:"inviterName"`
		ExpiresAt   *int64 `json:"expiresAt"`
	}
	_ = json.Unmarshal(raw, &rr)

	// Join the mesh so the supporter can reach this device.
	if _, err := localAgentRequest("POST", "/mesh/up", nil); err != nil {
		// No running daemon — register control plane directly so the peer appears.
		runMeshUpDirect(nil)
	}

	fmt.Println()
	fmt.Printf("✓ %s can now help you.\n", orFallback(rr.InviterName, info.Inviter.Name))
	if persistent {
		fmt.Println("  Access stays until you revoke it.")
	} else {
		fmt.Printf("  Access expires in %dh.\n", info.DefaultTtlHours)
	}
	fmt.Println("  Keep Yaver running:  yaver serve")
	fmt.Println("  Cut access anytime:  yaver support deny-all")
}

// supportInviteInfo mirrors getSupportInviteInfo's response.
type supportInviteInfo struct {
	Valid               bool `json:"valid"`
	OfferTerminal       bool `json:"offerTerminal"`
	OfferDesktopControl bool `json:"offerDesktopControl"`
	DefaultTtlHours     int  `json:"defaultTtlHours"`
	Inviter             struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"inviter"`
}

func fetchSupportInviteInfo(cfg *Config, code string) *supportInviteInfo {
	raw, err := meshConvexCall(cfg, "query", "support_link:getSupportInviteInfo", map[string]interface{}{"code": code})
	if err != nil {
		return nil
	}
	var info supportInviteInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil
	}
	return &info
}

func askYesNo(prompt string, def bool) bool {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	fmt.Print(prompt + suffix)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return def
	}
	ans := strings.ToLower(strings.TrimSpace(sc.Text()))
	if ans == "" {
		return def
	}
	return ans == "y" || ans == "yes"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func expiryLabel(expiresAt *int64) string {
	if expiresAt == nil {
		return "(until revoked)"
	}
	left := time.Until(time.Unix(*expiresAt/1000, 0))
	if left <= 0 {
		return "(expired)"
	}
	return fmt.Sprintf("(%dh left)", int(left.Hours())+1)
}

func desktopLabel(on bool) string {
	if on {
		return "  [desktop control]"
	}
	return ""
}
