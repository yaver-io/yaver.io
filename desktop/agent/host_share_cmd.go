package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

func boolPtr(v bool) *bool { return &v }

func prettyPrintJSONObject(v map[string]any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(v)
		return
	}
	fmt.Println(string(data))
}

func hostShareJoinURL(cfg *Config, code string) string {
	base := "https://yaver.io"
	if cfg != nil && strings.TrimSpace(cfg.WebBaseURL) != "" {
		base = strings.TrimRight(strings.TrimSpace(cfg.WebBaseURL), "/")
	}
	return base + "/host-share/join?code=" + strings.ToUpper(strings.TrimSpace(code))
}

func defaultHostShareRunners(cfg *Config) []string {
	if cfg != nil && cfg.HostShare != nil && cfg.HostShare.CapabilityManifest != nil {
		runners := append([]string(nil), cfg.HostShare.CapabilityManifest.Runtime.InstalledCodingRunners...)
		sort.Strings(runners)
		return runners
	}
	var out []string
	if hostShareCommandExists("claude") {
		out = append(out, "claude")
	}
	if hostShareCommandExists("codex") {
		out = append(out, "codex")
	}
	if hostShareCommandExists("opencode") {
		out = append(out, "opencode")
	}
	sort.Strings(out)
	return out
}

func resolveHostShareToolPreset(toolsOnly, infraOnly bool, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	switch {
	case toolsOnly:
		return "brokered-coding-tools"
	case infraOnly:
		return "infra-only"
	default:
		return "all-coding-tools"
	}
}

func runHostShareCreate(args []string) {
	fs := flag.NewFlagSet("host-share create", flag.ExitOnError)
	email := fs.String("email", "", "Target guest email (optional)")
	userID := fs.String("user-id", "", "Target guest public user id (optional)")
	label := fs.String("label", "", "Human label for this share")
	deviceID := fs.String("device", "", "Pin the session to a specific host device id")
	inviteTTL := fs.Int("invite-ttl-min", 24*60, "Invite expiry in minutes")
	sessionTTL := fs.Int("session-ttl-min", 8*60, "Session expiry in minutes after join")
	idleTTL := fs.Int("idle-timeout-min", 30, "Idle timeout in minutes")
	toolPreset := fs.String("tool-preset", "", "Tool preset label to store in policy")
	resourcePreset := fs.String("resource-preset", "balanced", "Resource preset label to store in policy")
	runners := fs.String("runners", "", "Comma-separated allowed runners (default: installed host runners)")
	projects := fs.String("projects", "", "Comma-separated allowed projects")
	allowTunnel := fs.Bool("allow-tunnel", false, "Allow tunnel forwarding in this lease")
	noTerminal := fs.Bool("no-terminal", false, "Disable terminal access in this lease")
	toolsOnly := fs.Bool("tools-only", false, "Brokered tools only; no raw infra exposure")
	infraOnly := fs.Bool("infra-only", false, "Expose infra only; do not expose host coding tools")
	fs.Parse(args)

	if *toolsOnly && *infraOnly {
		fmt.Fprintln(os.Stderr, "choose either --tools-only or --infra-only, not both")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	allowedRunners := splitComma(*runners)
	if len(allowedRunners) == 0 && !*infraOnly {
		allowedRunners = defaultHostShareRunners(cfg)
	}
	allowInfra := !*toolsOnly
	useHostAgentTools := !*infraOnly
	useHostInfra := true
	allowTerminal := !*noTerminal

	opts := HostShareCreateOpts{
		GuestEmail:         strings.TrimSpace(*email),
		GuestUserID:        strings.TrimSpace(*userID),
		Label:              strings.TrimSpace(*label),
		HostDeviceID:       strings.TrimSpace(*deviceID),
		InviteTTLMinutes:   *inviteTTL,
		SessionTTLMinutes:  *sessionTTL,
		IdleTimeoutMinutes: *idleTTL,
		ToolingPreset:      resolveHostShareToolPreset(*toolsOnly, *infraOnly, *toolPreset),
		ResourcePreset:     strings.TrimSpace(*resourcePreset),
		AllowInfra:         boolPtr(allowInfra),
		AllowTerminal:      boolPtr(allowTerminal),
		AllowTunnel:        boolPtr(*allowTunnel),
		UseHostAgentTools:  boolPtr(useHostAgentTools),
		UseHostInfra:       boolPtr(useHostInfra),
		AllowedRunners:     allowedRunners,
		AllowedProjects:    splitComma(*projects),
	}

	result, err := CreateHostShareInvite(convexURL, cfg.AuthToken, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create host-share invite: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Host-share invite created\n")
	fmt.Printf("Invite code: %s\n", result.InviteCode)
	fmt.Printf("Join URL:    %s\n", hostShareJoinURL(cfg, result.InviteCode))
	fmt.Printf("Expires:     %s\n", time.UnixMilli(result.InviteExpiresAt).Local().Format(time.RFC1123))
	fmt.Printf("Policy:      infra=%t terminal=%t tunnel=%t host-tools=%t host-infra=%t preset=%s resource=%s\n",
		result.Policy.AllowInfra,
		result.Policy.AllowTerminal,
		result.Policy.AllowTunnel,
		result.Policy.UseHostAgentTools,
		result.Policy.UseHostInfra,
		result.Policy.ToolingPreset,
		result.Policy.ResourcePreset,
	)
	if len(result.Policy.AllowedRunners) > 0 {
		fmt.Printf("Runners:     %s\n", strings.Join(result.Policy.AllowedRunners, ", "))
	}
	if len(result.Policy.AllowedProjects) > 0 {
		fmt.Printf("Projects:    %s\n", strings.Join(result.Policy.AllowedProjects, ", "))
	}
	fmt.Printf("Guest join:  yaver host-share join %s\n", result.InviteCode)
}

func runHostShareJoin(args []string) {
	fs := flag.NewFlagSet("host-share join", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share join <invite-code>")
		os.Exit(1)
	}
	code := strings.ToUpper(strings.TrimSpace(fs.Arg(0)))

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	preview, err := FindHostShareInvite(convexURL, cfg.AuthToken, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load host-share invite: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Host:        %s <%s>\n", preview.HostName, preview.HostEmail)
	fmt.Printf("Expires:     %s\n", time.UnixMilli(preview.InviteExpiresAt).Local().Format(time.RFC1123))
	fmt.Printf("Policy:      infra=%t terminal=%t tunnel=%t host-tools=%t host-infra=%t preset=%s resource=%s\n",
		preview.AllowInfra,
		preview.AllowTerminal,
		preview.AllowTunnel,
		preview.UseHostAgentTools,
		preview.UseHostInfra,
		preview.ToolingPreset,
		preview.ResourcePreset,
	)
	if len(preview.AllowedRunners) > 0 {
		fmt.Printf("Runners:     %s\n", strings.Join(preview.AllowedRunners, ", "))
	}
	if len(preview.AllowedProjects) > 0 {
		fmt.Printf("Projects:    %s\n", strings.Join(preview.AllowedProjects, ", "))
	}

	result, err := JoinHostShareByCode(convexURL, cfg.AuthToken, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "join host-share invite: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Printf("Joined host-share session %s\n", result.SessionID)
	fmt.Printf("Session expires: %s\n", time.UnixMilli(result.ExpiresAt).Local().Format(time.RFC1123))
	fmt.Printf("Next: run `yaver host-share attach-repo --session %s --path <repo>` to seed the borrowed workspace from this machine.\n", result.SessionID)
}

func runHostShareList(args []string) {
	fs := flag.NewFlagSet("host-share list", flag.ExitOnError)
	role := fs.String("role", "host", "host or guest")
	sessionsOnly := fs.Bool("sessions", false, "List active sessions instead of invites")
	fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	if *sessionsOnly {
		sessions, err := FetchHostShareSessions(convexURL, cfg.AuthToken, *role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch host-share sessions: %v\n", err)
			os.Exit(1)
		}
		if len(sessions) == 0 {
			fmt.Println("No active host-share sessions.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SESSION\tHOST\tGUEST\tEXPIRES\tPRESET")
		for _, s := range sessions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				s.SessionID,
				s.HostEmail,
				s.GuestEmail,
				time.UnixMilli(s.ExpiresAt).Local().Format("2006-01-02 15:04"),
				s.Policy.ToolingPreset,
			)
		}
		_ = w.Flush()
		return
	}

	invites, err := FetchHostShareInvites(convexURL, cfg.AuthToken, *role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch host-share invites: %v\n", err)
		os.Exit(1)
	}
	if len(invites) == 0 {
		fmt.Println("No host-share invites.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if *role == "guest" {
		fmt.Fprintln(w, "CODE\tHOST\tSTATUS\tEXPIRES\tPRESET")
		for _, inv := range invites {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				inv.InviteCode,
				inv.HostEmail,
				inv.Status,
				time.UnixMilli(inv.InviteExpiresAt).Local().Format("2006-01-02 15:04"),
				inv.ToolingPreset,
			)
		}
	} else {
		fmt.Fprintln(w, "CODE\tGUEST\tSTATUS\tEXPIRES\tPRESET")
		for _, inv := range invites {
			guest := strings.TrimSpace(inv.GuestEmail)
			if guest == "" {
				guest = strings.TrimSpace(inv.GuestUserID)
			}
			if guest == "" {
				guest = "<open invite>"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				inv.InviteCode,
				guest,
				inv.Status,
				time.UnixMilli(inv.InviteExpiresAt).Local().Format("2006-01-02 15:04"),
				inv.ToolingPreset,
			)
		}
	}
	_ = w.Flush()
}

func runHostShareRevoke(args []string) {
	fs := flag.NewFlagSet("host-share revoke", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share revoke <invite-code>")
		os.Exit(1)
	}
	code := strings.ToUpper(strings.TrimSpace(fs.Arg(0)))

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	if err := RevokeHostShareInvite(convexURL, cfg.AuthToken, code); err != nil {
		fmt.Fprintf(os.Stderr, "revoke host-share invite: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Revoked host-share invite %s\n", code)
}

func runHostShareEnd(args []string) {
	fs := flag.NewFlagSet("host-share end", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share end <session-id>")
		os.Exit(1)
	}
	sessionID := strings.TrimSpace(fs.Arg(0))

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}
	if err := EndHostShareSession(convexURL, cfg.AuthToken, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "end host-share session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Ended host-share session %s\n", sessionID)
}

func runHostShareWorkspaceStatus(args []string) {
	fs := flag.NewFlagSet("host-share workspace-status", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id")
	fs.Parse(args)
	if strings.TrimSpace(*sessionID) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share workspace-status --session <session-id>")
		os.Exit(1)
	}
	mgr, err := NewHostShareWorkspaceManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace manager: %v\n", err)
		os.Exit(1)
	}
	ws, err := mgr.EnsureWorkspace(*sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ensure workspace: %v\n", err)
		os.Exit(1)
	}
	if refreshed, err := mgr.RefreshCounts(*sessionID); err == nil && refreshed != nil {
		ws = refreshed
	}
	fmt.Printf("Session:   %s\n", ws.SessionID)
	fmt.Printf("State:     %s\n", ws.State)
	fmt.Printf("Root:      %s\n", ws.RootDir)
	fmt.Printf("Repo:      %s\n", ws.RepoDir)
	fmt.Printf("Files:     %d\n", ws.FileCount)
	fmt.Printf("Dirs:      %d\n", ws.DirCount)
	if ws.SourceDir != "" {
		fmt.Printf("Source:    %s\n", ws.SourceDir)
	}
	if ws.GuestDeviceID != "" {
		fmt.Printf("Guest:     %s\n", ws.GuestDeviceID)
	}
	if ws.GuestRootID != "" {
		fmt.Printf("GuestRoot: %s\n", ws.GuestRootID)
	}
	if ws.LastError != "" {
		fmt.Printf("LastError: %s\n", ws.LastError)
	}
}

func runHostShareWorkspaceBootstrap(args []string) {
	fs := flag.NewFlagSet("host-share workspace-bootstrap", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id")
	sourceDir := fs.String("source-dir", "", "Host-side source directory to mirror into the session workspace")
	fs.Parse(args)
	if strings.TrimSpace(*sessionID) == "" || strings.TrimSpace(*sourceDir) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share workspace-bootstrap --session <session-id> --source-dir <path>")
		os.Exit(1)
	}
	mgr, err := NewHostShareWorkspaceManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace manager: %v\n", err)
		os.Exit(1)
	}
	ws, err := mgr.BootstrapFromDir(*sessionID, *sourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap workspace: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bootstrapped host-share workspace for %s\n", ws.SessionID)
	fmt.Printf("Repo:   %s\n", ws.RepoDir)
	fmt.Printf("State:  %s\n", ws.State)
	fmt.Printf("Files:  %d\n", ws.FileCount)
	fmt.Printf("Dirs:   %d\n", ws.DirCount)
}

func runHostShareGuestRoots(args []string) {
	fs := flag.NewFlagSet("host-share guest-roots", flag.ExitOnError)
	deviceID := fs.String("device", "", "Guest device id")
	fs.Parse(args)
	if strings.TrimSpace(*deviceID) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share guest-roots --device <guest-device-id>")
		os.Exit(1)
	}
	out, err := proxyToDeviceJSON(context.Background(), "host-share-guest-roots", *deviceID, "GET", "/files/roots", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guest roots: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSONObject(out)
}

func runHostShareGuestRead(args []string) {
	fs := flag.NewFlagSet("host-share guest-read", flag.ExitOnError)
	deviceID := fs.String("device", "", "Guest device id")
	root := fs.String("root", "", "Guest file root id")
	path := fs.String("path", "", "Path inside root")
	fs.Parse(args)
	if strings.TrimSpace(*deviceID) == "" || strings.TrimSpace(*root) == "" || strings.TrimSpace(*path) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share guest-read --device <guest-device-id> --root <root-id> --path <file>")
		os.Exit(1)
	}
	out, err := proxyToDeviceJSON(context.Background(), "host-share-guest-read", *deviceID, "GET", "/files/read?root="+urlQueryEscape(*root)+"&path="+urlQueryEscape(*path), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guest read: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSONObject(out)
}

func runHostShareGuestWrite(args []string) {
	fs := flag.NewFlagSet("host-share guest-write", flag.ExitOnError)
	deviceID := fs.String("device", "", "Guest device id")
	root := fs.String("root", "", "Guest file root id")
	path := fs.String("path", "", "Path inside root")
	content := fs.String("content", "", "New file content")
	fs.Parse(args)
	if strings.TrimSpace(*deviceID) == "" || strings.TrimSpace(*root) == "" || strings.TrimSpace(*path) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share guest-write --device <guest-device-id> --root <root-id> --path <file> --content <text>")
		os.Exit(1)
	}
	out, err := proxyToDeviceJSON(context.Background(), "host-share-guest-write", *deviceID, "POST", "/host-share/fs/write", map[string]any{
		"root":    *root,
		"path":    *path,
		"content": *content,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "guest write: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSONObject(out)
}

func hostShareWorkspaceSelection(sessionID string) (*HostShareWorkspaceManager, *HostShareWorkspace, error) {
	mgr, err := NewHostShareWorkspaceManager()
	if err != nil {
		return nil, nil, err
	}
	ws, err := mgr.EnsureWorkspace(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return mgr, ws, nil
}

func resolveGuestHostShareSession(sessionHint string) (*HostShareSessionInfo, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not signed in; run 'yaver auth' first")
	}
	convexURL := cfg.ConvexSiteURL
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}
	sessions, err := FetchHostShareSessions(convexURL, cfg.AuthToken, "guest")
	if err != nil {
		return nil, fmt.Errorf("fetch host-share sessions: %w", err)
	}
	hint := strings.TrimSpace(sessionHint)
	if hint != "" {
		for i := range sessions {
			if sessions[i].SessionID == hint || strings.HasPrefix(sessions[i].SessionID, hint) {
				return &sessions[i], nil
			}
		}
		return nil, fmt.Errorf("host-share session %q not found", hint)
	}
	if len(sessions) == 1 {
		return &sessions[0], nil
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no active host-share sessions")
	}
	return nil, fmt.Errorf("multiple host-share sessions active; pass --session explicitly")
}

func localHostShareRoots() []FileRoot {
	projects := listDiscoveredProjects()
	out := make([]FileRoot, 0, len(projects))
	seen := map[string]bool{}
	for _, p := range projects {
		path := strings.TrimSpace(p.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, FileRoot{
			ID:   projectFSID(path),
			Name: filepath.Base(path),
			Path: path,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) > len(out[j].Path)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func resolveLocalHostShareRoot(rootHint, pathHint string) (FileRoot, error) {
	roots := localHostShareRoots()
	if root := strings.TrimSpace(rootHint); root != "" {
		for _, candidate := range roots {
			if candidate.ID == root {
				return candidate, nil
			}
		}
		return FileRoot{}, fmt.Errorf("local repo root %q not found", root)
	}
	target := strings.TrimSpace(pathHint)
	if target == "" {
		target = "."
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return FileRoot{}, fmt.Errorf("resolve repo path: %w", err)
	}
	absTarget = filepath.Clean(absTarget)
	for _, candidate := range roots {
		rootPath := filepath.Clean(candidate.Path)
		if absTarget == rootPath {
			return candidate, nil
		}
		if rel, err := filepath.Rel(rootPath, absTarget); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return candidate, nil
		}
	}
	info, err := os.Stat(absTarget)
	if err != nil {
		return FileRoot{}, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return FileRoot{}, fmt.Errorf("path %s is not a directory", absTarget)
	}
	return FileRoot{
		Name: filepath.Base(absTarget),
		Path: absTarget,
	}, nil
}

func proxyHostShareGuestRoots(deviceID string) ([]FileRoot, error) {
	out, err := proxyToDeviceJSON(context.Background(), "host-share-guest-roots", deviceID, http.MethodGet, "/files/roots", nil)
	if err != nil {
		return nil, err
	}
	rawRoots, _ := out["roots"].([]any)
	roots := make([]FileRoot, 0, len(rawRoots))
	for _, item := range rawRoots {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		root := FileRoot{
			ID:   fmt.Sprint(m["id"]),
			Name: fmt.Sprint(m["name"]),
			Path: fmt.Sprint(m["path"]),
		}
		if strings.TrimSpace(root.ID) == "" {
			continue
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func proxyHostShareGuestList(deviceID string, selector hostShareGuestRootSelector, subpath string) ([]FileEntry, error) {
	query := hostShareRootQuery(selector)
	if query == "" {
		return nil, fmt.Errorf("guest root selector required")
	}
	path := "/files/list?" + query + "&path=" + urlQueryEscape(strings.TrimPrefix(subpath, "/"))
	out, err := proxyToDeviceJSON(context.Background(), "host-share-guest-list", deviceID, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	rawEntries, _ := out["entries"].([]any)
	entries := make([]FileEntry, 0, len(rawEntries))
	for _, item := range rawEntries {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := FileEntry{
			Name:  fmt.Sprint(m["name"]),
			Path:  fmt.Sprint(m["path"]),
			IsDir: false,
			Size:  0,
			MTime: 0,
		}
		if v, ok := m["isDir"].(bool); ok {
			entry.IsDir = v
		}
		if v, ok := m["size"].(float64); ok {
			entry.Size = int64(v)
		}
		if v, ok := m["mtime"].(float64); ok {
			entry.MTime = int64(v)
		}
		if strings.TrimSpace(entry.Name) == "" {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

func proxyHostShareGuestRaw(deviceID string, selector hostShareGuestRootSelector, subpath string) ([]byte, error) {
	query := hostShareRootQuery(selector)
	if query == "" {
		return nil, fmt.Errorf("guest root selector required")
	}
	path := "/files/raw?" + query + "&path=" + urlQueryEscape(strings.TrimPrefix(subpath, "/"))
	status, raw, err := proxyToDevice(context.Background(), "host-share-guest-raw", deviceID, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(status)
		}
		return nil, fmt.Errorf("remote GET %s: HTTP %d: %s", path, status, msg)
	}
	return raw, nil
}

func resolveHostShareGuestRoot(deviceID, explicitRoot, explicitPath string) (FileRoot, hostShareGuestRootSelector, error) {
	if strings.TrimSpace(explicitPath) != "" {
		root := strings.TrimSpace(explicitPath)
		if !filepath.IsAbs(root) {
			return FileRoot{}, hostShareGuestRootSelector{}, fmt.Errorf("guest root path must be absolute")
		}
		return FileRoot{
			ID:   projectFSID(root),
			Name: filepath.Base(root),
			Path: root,
		}, hostShareGuestRootSelector{Path: root}, nil
	}
	if strings.TrimSpace(explicitRoot) != "" {
		roots, err := proxyHostShareGuestRoots(deviceID)
		if err != nil {
			return FileRoot{}, hostShareGuestRootSelector{}, err
		}
		for _, root := range roots {
			if root.ID == strings.TrimSpace(explicitRoot) {
				return root, hostShareGuestRootSelector{ID: root.ID, Path: root.Path}, nil
			}
		}
		return FileRoot{}, hostShareGuestRootSelector{}, fmt.Errorf("guest root %q not found on device %s", explicitRoot, deviceID)
	}
	roots, err := proxyHostShareGuestRoots(deviceID)
	if err != nil {
		return FileRoot{}, hostShareGuestRootSelector{}, err
	}
	if len(roots) == 0 {
		return FileRoot{}, hostShareGuestRootSelector{}, fmt.Errorf("no guest roots available on device %s", deviceID)
	}
	if len(roots) > 1 {
		return FileRoot{}, hostShareGuestRootSelector{}, fmt.Errorf("multiple guest roots available; pass --root explicitly")
	}
	return roots[0], hostShareGuestRootSelector{ID: roots[0].ID, Path: roots[0].Path}, nil
}

func hostShareListGuestTree(deviceID string, selector hostShareGuestRootSelector, subpath string) ([]FileEntry, error) {
	entries, err := proxyHostShareGuestList(deviceID, selector, subpath)
	if err != nil {
		return nil, err
	}
	all := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		all = append(all, entry)
		if !entry.IsDir {
			continue
		}
		rel := strings.TrimPrefix(entry.Path, "/")
		children, err := hostShareListGuestTree(deviceID, selector, rel)
		if err != nil {
			return nil, err
		}
		all = append(all, children...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		pi := strings.TrimPrefix(all[i].Path, "/")
		pj := strings.TrimPrefix(all[j].Path, "/")
		if strings.Count(pi, "/") != strings.Count(pj, "/") {
			return strings.Count(pi, "/") < strings.Count(pj, "/")
		}
		if all[i].IsDir != all[j].IsDir {
			return !all[i].IsDir
		}
		return pi < pj
	})
	return all, nil
}

type hostSharePullStats struct {
	Files int
	Dirs  int
}

type hostShareGuestRootSelector struct {
	ID   string
	Path string
}

func (s hostShareGuestRootSelector) displayPath() string {
	if strings.TrimSpace(s.Path) != "" {
		return strings.TrimSpace(s.Path)
	}
	return strings.TrimSpace(s.ID)
}

func (s hostShareGuestRootSelector) requestMap() map[string]any {
	body := map[string]any{}
	if strings.TrimSpace(s.ID) != "" {
		body["root"] = strings.TrimSpace(s.ID)
	}
	if strings.TrimSpace(s.Path) != "" {
		body["rootPath"] = strings.TrimSpace(s.Path)
	}
	return body
}

func hostShareRootQuery(selector hostShareGuestRootSelector) string {
	values := []string{}
	if strings.TrimSpace(selector.ID) != "" {
		values = append(values, "root="+urlQueryEscape(strings.TrimSpace(selector.ID)))
	}
	if strings.TrimSpace(selector.Path) != "" {
		values = append(values, "rootPath="+urlQueryEscape(strings.TrimSpace(selector.Path)))
	}
	return strings.Join(values, "&")
}

func hostShareGuestRootFromPath(pathHint string) (hostShareGuestRootSelector, error) {
	target := strings.TrimSpace(pathHint)
	if target == "" {
		target = "."
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return hostShareGuestRootSelector{}, fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(absTarget)
	if err != nil {
		return hostShareGuestRootSelector{}, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return hostShareGuestRootSelector{}, fmt.Errorf("path %s is not a directory", absTarget)
	}
	return hostShareGuestRootSelector{Path: filepath.Clean(absTarget)}, nil
}

func hostShareGuestRootSelectorForAttach(rootHint, pathHint string) (hostShareGuestRootSelector, error) {
	if root := strings.TrimSpace(rootHint); root != "" {
		resolved, err := resolveLocalHostShareRoot(root, pathHint)
		if err != nil {
			return hostShareGuestRootSelector{}, err
		}
		if strings.TrimSpace(resolved.ID) != "" {
			return hostShareGuestRootSelector{ID: resolved.ID, Path: resolved.Path}, nil
		}
	}
	if resolved, err := resolveLocalHostShareRoot("", pathHint); err == nil {
		if strings.TrimSpace(resolved.ID) != "" {
			return hostShareGuestRootSelector{ID: resolved.ID, Path: resolved.Path}, nil
		}
	}
	return hostShareGuestRootFromPath(pathHint)
}

func hostShareImportGuestRootIntoWorkspace(sessionID, deviceID, rootID, rootPath string) (*HostShareWorkspace, FileRoot, *hostSharePullStats, error) {
	root, selector, err := resolveHostShareGuestRoot(deviceID, rootID, rootPath)
	if err != nil {
		return nil, FileRoot{}, nil, err
	}
	mgr, ws, err := hostShareWorkspaceSelection(sessionID)
	if err != nil {
		return nil, FileRoot{}, nil, err
	}
	if err := cleanWorkspaceRepoDir(ws.RepoDir); err != nil {
		return nil, FileRoot{}, nil, fmt.Errorf("clean workspace repo: %w", err)
	}
	stats := &hostSharePullStats{}
	if err := hostSharePullGuestTree(deviceID, selector, "", ws.RepoDir, stats); err != nil {
		return nil, FileRoot{}, nil, fmt.Errorf("pull guest tree: %w", err)
	}
	if _, err := mgr.BindGuestRoot(sessionID, deviceID, root.ID, selector.Path); err != nil {
		return nil, FileRoot{}, nil, fmt.Errorf("bind guest root: %w", err)
	}
	if refreshed, err := mgr.RefreshCounts(sessionID); err == nil && refreshed != nil {
		ws = refreshed
	}
	return ws, root, stats, nil
}

func hostSharePullGuestTree(deviceID string, selector hostShareGuestRootSelector, subpath, targetDir string, stats *hostSharePullStats) error {
	entries, err := proxyHostShareGuestList(deviceID, selector, subpath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := strings.TrimPrefix(entry.Path, "/")
		dstPath := filepath.Join(targetDir, filepath.FromSlash(rel))
		if entry.IsDir {
			if err := os.MkdirAll(dstPath, 0700); err != nil {
				return fmt.Errorf("mkdir %s: %w", dstPath, err)
			}
			stats.Dirs++
			if err := hostSharePullGuestTree(deviceID, selector, rel, targetDir, stats); err != nil {
				return err
			}
			continue
		}
		raw, err := proxyHostShareGuestRaw(deviceID, selector, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0700); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", dstPath, err)
		}
		if err := os.WriteFile(dstPath, raw, 0644); err != nil {
			return fmt.Errorf("write %s: %w", dstPath, err)
		}
		stats.Files++
	}
	return nil
}

type hostSharePushStats struct {
	Files   int
	Dirs    int
	Deleted int
}

func hostShareExportWorkspaceToGuest(sessionID, deviceID, rootID string) (*HostShareWorkspace, *hostSharePushStats, error) {
	mgr, ws, err := hostShareWorkspaceSelection(sessionID)
	if err != nil {
		return nil, nil, err
	}
	device := strings.TrimSpace(deviceID)
	if device == "" {
		device = strings.TrimSpace(ws.GuestDeviceID)
	}
	root := strings.TrimSpace(rootID)
	if root == "" {
		root = strings.TrimSpace(ws.GuestRootID)
	}
	rootPath := strings.TrimSpace(ws.GuestRootPath)
	if device == "" || (root == "" && rootPath == "") {
		return nil, nil, fmt.Errorf("guest device/root not bound; run attach-repo first or pass explicit ids")
	}
	if _, err := mgr.BindGuestRoot(sessionID, device, root, rootPath); err != nil {
		return nil, nil, fmt.Errorf("bind guest root: %w", err)
	}
	stats := &hostSharePushStats{}
	if err := hostSharePushWorkspaceTree(device, hostShareGuestRootSelector{ID: root, Path: rootPath}, ws.RepoDir, stats); err != nil {
		return nil, nil, fmt.Errorf("push workspace tree: %w", err)
	}
	if refreshed, err := mgr.RefreshCounts(sessionID); err == nil && refreshed != nil {
		ws = refreshed
	}
	return ws, stats, nil
}

func hostSharePushWorkspaceTree(deviceID string, selector hostShareGuestRootSelector, repoDir string, stats *hostSharePushStats) error {
	remoteEntries, err := hostShareListGuestTree(deviceID, selector, "")
	if err != nil {
		return err
	}
	localPaths := map[string]bool{}
	err = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == repoDir {
			return nil
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".yaver/") || rel == ".yaver" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		localPaths[rel] = true
		if d.IsDir() {
			body := selector.requestMap()
			body["path"] = rel
			info, err := d.Info()
			if err == nil {
				body["mode"] = int(info.Mode().Perm())
			}
			_, err = proxyToDeviceJSON(context.Background(), "host-share-guest-mkdir", deviceID, http.MethodPost, "/host-share/fs/mkdir", body)
			if err != nil {
				return err
			}
			stats.Dirs++
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := selector.requestMap()
		body["path"] = rel
		body["contentBase64"] = base64.StdEncoding.EncodeToString(raw)
		info, err := d.Info()
		if err == nil {
			body["mode"] = int(info.Mode().Perm())
		}
		_, err = proxyToDeviceJSON(context.Background(), "host-share-guest-write", deviceID, http.MethodPost, "/host-share/fs/write", body)
		if err != nil {
			return err
		}
		stats.Files++
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(remoteEntries) - 1; i >= 0; i-- {
		entry := remoteEntries[i]
		rel := strings.TrimPrefix(entry.Path, "/")
		if rel == "" || localPaths[rel] {
			continue
		}
		body := selector.requestMap()
		body["path"] = rel
		if _, err := proxyToDeviceJSON(context.Background(), "host-share-guest-delete", deviceID, http.MethodPost, "/host-share/fs/delete", body); err != nil {
			return err
		}
		stats.Deleted++
	}
	return nil
}

func runHostShareGuestPull(args []string) {
	fs := flag.NewFlagSet("host-share guest-pull", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id")
	deviceID := fs.String("device", "", "Guest device id")
	rootID := fs.String("root", "", "Guest file root id")
	fs.Parse(args)
	if strings.TrimSpace(*sessionID) == "" || strings.TrimSpace(*deviceID) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share guest-pull --session <session-id> --device <guest-device-id> [--root <root-id>]")
		os.Exit(1)
	}

	ws, root, stats, err := hostShareImportGuestRootIntoWorkspace(*sessionID, *deviceID, *rootID, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pulled guest root %s into workspace %s\n", root.ID, ws.SessionID)
	fmt.Printf("Guest:  %s\n", *deviceID)
	fmt.Printf("Root:   %s\n", root.Path)
	fmt.Printf("Repo:   %s\n", ws.RepoDir)
	fmt.Printf("Files:  %d\n", stats.Files)
	fmt.Printf("Dirs:   %d\n", stats.Dirs)
}

func runHostShareGuestPush(args []string) {
	fs := flag.NewFlagSet("host-share guest-push", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id")
	deviceID := fs.String("device", "", "Guest device id (defaults to workspace binding)")
	rootID := fs.String("root", "", "Guest file root id (defaults to workspace binding)")
	fs.Parse(args)
	if strings.TrimSpace(*sessionID) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver host-share guest-push --session <session-id> [--device <guest-device-id>] [--root <root-id>]")
		os.Exit(1)
	}

	ws, stats, err := hostShareExportWorkspaceToGuest(*sessionID, *deviceID, *rootID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pushed workspace %s back to guest root %s\n", ws.SessionID, ws.GuestRootID)
	fmt.Printf("Guest:   %s\n", ws.GuestDeviceID)
	fmt.Printf("Repo:    %s\n", ws.RepoDir)
	fmt.Printf("Files:   %d\n", stats.Files)
	fmt.Printf("Dirs:    %d\n", stats.Dirs)
	fmt.Printf("Deleted: %d stale guest paths\n", stats.Deleted)
}

func runHostShareAttachRepo(args []string) {
	fs := flag.NewFlagSet("host-share attach-repo", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id (optional if only one guest session is active)")
	pathHint := fs.String("path", "", "Local repo path (default: current directory)")
	rootID := fs.String("root", "", "Local repo root id")
	fs.Parse(args)

	session, err := resolveGuestHostShareSession(*sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(session.HostDeviceID) == "" {
		fmt.Fprintln(os.Stderr, "host-share session does not expose a host device id")
		os.Exit(1)
	}
	selector, err := hostShareGuestRootSelectorForAttach(*rootID, *pathHint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	body := map[string]any{}
	if strings.TrimSpace(selector.ID) != "" {
		body["rootId"] = selector.ID
	}
	if strings.TrimSpace(selector.Path) != "" {
		body["rootPath"] = selector.Path
	}
	out, err := proxyToDeviceJSON(context.Background(), "host-share-attach-repo", session.HostDeviceID, http.MethodPost, "/host-share/workspace/attach-repo", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attach repo: %v\n", err)
		os.Exit(1)
	}
	workspace, _ := out["workspace"].(map[string]any)
	stats, _ := out["stats"].(map[string]any)
	rootPath := selector.Path
	if rootPath == "" {
		rootPath = selector.ID
	}
	fmt.Printf("Attached repo %s to host-share session %s\n", rootPath, session.SessionID)
	fmt.Printf("Host:      %s\n", session.HostEmail)
	fmt.Printf("Host box:  %s\n", session.HostDeviceID)
	if selector.ID != "" {
		fmt.Printf("Root:      %s\n", selector.ID)
	}
	if repoDir, ok := workspace["repoDir"].(string); ok && strings.TrimSpace(repoDir) != "" {
		fmt.Printf("Workspace: %s\n", repoDir)
	}
	if files, ok := stats["files"].(float64); ok {
		fmt.Printf("Files:     %d\n", int(files))
	}
	if dirs, ok := stats["dirs"].(float64); ok {
		fmt.Printf("Dirs:      %d\n", int(dirs))
	}
}

func runHostShareSyncRepo(args []string) {
	fs := flag.NewFlagSet("host-share sync-repo", flag.ExitOnError)
	sessionID := fs.String("session", "", "Host-share session id (optional if only one guest session is active)")
	toHost := fs.Bool("to-host", false, "Sync the local repo up to the borrowed workspace")
	fromHost := fs.Bool("from-host", false, "Sync the borrowed workspace back down to the local repo")
	fs.Parse(args)

	if (*toHost && *fromHost) || (!*toHost && !*fromHost) {
		fmt.Fprintln(os.Stderr, "choose exactly one of --to-host or --from-host")
		os.Exit(1)
	}
	session, err := resolveGuestHostShareSession(*sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(session.HostDeviceID) == "" {
		fmt.Fprintln(os.Stderr, "host-share session does not expose a host device id")
		os.Exit(1)
	}
	path := "/host-share/workspace/push-to-guest"
	action := "Pulled borrowed workspace changes into local repo"
	if *toHost {
		path = "/host-share/workspace/pull-from-guest"
		action = "Pushed local repo changes into borrowed workspace"
	}
	out, err := proxyToDeviceJSON(context.Background(), "host-share-sync-repo", session.HostDeviceID, http.MethodPost, path, map[string]any{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync repo: %v\n", err)
		os.Exit(1)
	}
	workspace, _ := out["workspace"].(map[string]any)
	stats, _ := out["stats"].(map[string]any)
	fmt.Printf("%s for session %s\n", action, session.SessionID)
	if repoDir, ok := workspace["repoDir"].(string); ok && strings.TrimSpace(repoDir) != "" {
		fmt.Printf("Workspace: %s\n", repoDir)
	}
	if files, ok := stats["files"].(float64); ok {
		fmt.Printf("Files:     %d\n", int(files))
	}
	if dirs, ok := stats["dirs"].(float64); ok {
		fmt.Printf("Dirs:      %d\n", int(dirs))
	}
	if deleted, ok := stats["deleted"].(float64); ok {
		fmt.Printf("Deleted:   %d stale guest paths\n", int(deleted))
	}
}
