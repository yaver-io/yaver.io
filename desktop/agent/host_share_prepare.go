package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// host_share_prepare.go — host-side readiness audit for future host-backed
// guest coding sessions. This is the first implementation slice of the
// borrowed-runner product: a machine should know what it can actually promise
// before it ever emits a share code.

type HostShareConfig struct {
	PreparePromptDone  bool                         `json:"prepare_prompt_done,omitempty"`
	LastPreparedAt     string                       `json:"last_prepared_at,omitempty"`
	CapabilityManifest *HostShareCapabilityManifest `json:"capability_manifest,omitempty"`
}

type HostShareCapabilityManifest struct {
	GeneratedAt string                   `json:"generated_at"`
	Hostname    string                   `json:"hostname,omitempty"`
	Platform    string                   `json:"platform"`
	Arch        string                   `json:"arch"`
	WSL         string                   `json:"wsl,omitempty"`
	Permissions HostSharePermissionAudit `json:"permissions"`
	Runtime     HostShareRuntimeAudit    `json:"runtime"`
	Transport   HostShareTransportAudit  `json:"transport"`
	Service     HostShareServiceAudit    `json:"service"`
	Summary     HostShareSummary         `json:"summary"`
}

type HostSharePermissionAudit struct {
	Interactive             bool     `json:"interactive"`
	MacOSChecklistCompleted bool     `json:"macos_checklist_completed,omitempty"`
	RequestedCapabilities   []string `json:"requested_capabilities,omitempty"`
	MissingCapabilities     []string `json:"missing_capabilities,omitempty"`
}

type HostShareRuntimeAudit struct {
	GitInstalled           bool     `json:"git_installed"`
	NodeInstalled          bool     `json:"node_installed"`
	DockerAvailable        bool     `json:"docker_available"`
	TmuxInstalled          bool     `json:"tmux_installed"`
	InstalledCodingRunners []string `json:"installed_coding_runners,omitempty"`
	CloudflaredInstalled   bool     `json:"cloudflared_installed"`
	TailscaleInstalled     bool     `json:"tailscale_installed"`
}

type HostShareTransportAudit struct {
	SameLANLikely         bool     `json:"same_lan_likely"`
	TailscaleConnected    bool     `json:"tailscale_connected"`
	CloudflareConfigured  bool     `json:"cloudflare_configured"`
	CustomRelayConfigured bool     `json:"custom_relay_configured"`
	CustomRelayHealthy    bool     `json:"custom_relay_healthy"`
	FreeRelayLikely       bool     `json:"free_relay_likely"`
	TransportOrder        []string `json:"transport_order,omitempty"`
	ReachableModes        []string `json:"reachable_modes,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

type HostShareServiceAudit struct {
	Authenticated      bool `json:"authenticated"`
	AgentRunning       bool `json:"agent_running"`
	AutoStartInstalled bool `json:"auto_start_installed"`
}

type HostShareSummary struct {
	BorrowedRunnerReady bool     `json:"borrowed_runner_ready"`
	TerminalReady       bool     `json:"terminal_ready"`
	GuestFriendlyReady  bool     `json:"guest_friendly_ready"`
	ReadyRunners        []string `json:"ready_runners,omitempty"`
	Missing             []string `json:"missing,omitempty"`
}

func ensureHostShareConfig(cfg *Config) *HostShareConfig {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.HostShare == nil {
		cfg.HostShare = &HostShareConfig{}
	}
	return cfg.HostShare
}

func runHostShare(args []string) {
	if len(args) == 0 {
		printHostShareUsage()
		return
	}
	switch args[0] {
	case "prepare":
		runHostSharePrepare(args[1:])
	case "create":
		runHostShareCreate(args[1:])
	case "join":
		runHostShareJoin(args[1:])
	case "list", "ls":
		runHostShareList(args[1:])
	case "sessions":
		runHostShareList(append([]string{"--sessions"}, args[1:]...))
	case "workspace-status":
		runHostShareWorkspaceStatus(args[1:])
	case "workspace-bootstrap":
		runHostShareWorkspaceBootstrap(args[1:])
	case "attach-repo":
		runHostShareAttachRepo(args[1:])
	case "sync-repo":
		runHostShareSyncRepo(args[1:])
	case "guest-roots":
		runHostShareGuestRoots(args[1:])
	case "guest-read":
		runHostShareGuestRead(args[1:])
	case "guest-write":
		runHostShareGuestWrite(args[1:])
	case "guest-pull":
		runHostShareGuestPull(args[1:])
	case "guest-push":
		runHostShareGuestPush(args[1:])
	case "revoke", "rm":
		runHostShareRevoke(args[1:])
	case "end":
		runHostShareEnd(args[1:])
	case "status", "show":
		runHostShareStatus(args[1:])
	case "help", "--help", "-h":
		printHostShareUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown host-share subcommand: %s\n\n", args[0])
		printHostShareUsage()
		os.Exit(1)
	}
}

func printHostShareUsage() {
	fmt.Print(`Usage: yaver host-share <command>

Prepare this machine for future host-backed guest coding sessions.

Commands:
  prepare   Audit permissions, tools, transport, and service readiness
  create    Create a brokered host-share invite code/link
  join      Redeem a host-share invite code
  list      List host-share invites (use --sessions for active sessions)
	workspace-status    Show the local borrowed workspace for a session
	workspace-bootstrap Seed a session workspace from a host-side directory
  attach-repo         On the guest, attach a local repo to a borrowed workspace
  sync-repo           Sync an attached repo to or from the borrowed workspace
  guest-roots         List guest-side repo roots through the host-share bus
  guest-read          Read a guest-side file through the host-share bus
  guest-write         Write a guest-side file through the host-share bus
  guest-pull          Mirror a guest repo root into the local borrowed workspace
  guest-push          Push the local borrowed workspace back through the guest bus
  end       End an active host-share session immediately
  revoke    Revoke an invite code and any active session created from it
  status    Show the last saved capability manifest (or a live one with --live)
`)
}

func runHostSharePrepare(args []string) {
	fs := flag.NewFlagSet("host-share prepare", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Print the manifest as JSON")
	noSave := fs.Bool("no-save", false, "Do not persist the generated manifest")
	fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	manifest, changedCfg := runHostSharePrepareFlow(cfg, "manual", true)
	if manifest == nil {
		os.Exit(1)
	}
	if !*noSave {
		hs := ensureHostShareConfig(changedCfg)
		hs.CapabilityManifest = manifest
		hs.LastPreparedAt = manifest.GeneratedAt
		hs.PreparePromptDone = true
		if err := SaveConfig(changedCfg); err != nil {
			fmt.Fprintf(os.Stderr, "save config: %v\n", err)
			os.Exit(1)
		}
	}
	if *jsonOut {
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
	}
}

func runHostShareStatus(args []string) {
	fs := flag.NewFlagSet("host-share status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Print the manifest as JSON")
	live := fs.Bool("live", false, "Run a fresh audit instead of showing the saved manifest")
	fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	var manifest *HostShareCapabilityManifest
	if *live {
		manifest = auditHostShareCapabilities(cfg)
	} else if cfg != nil && cfg.HostShare != nil && cfg.HostShare.CapabilityManifest != nil {
		manifest = cfg.HostShare.CapabilityManifest
	} else {
		fmt.Println("No saved host-share capability manifest yet.")
		fmt.Println("Run `yaver host-share prepare` first.")
		return
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
		return
	}
	printHostShareManifest(manifest)
}

func maybeRunHostSharePrepareOnboarding(source string) {
	if !stdinLooksInteractive() {
		return
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return
	}
	hs := ensureHostShareConfig(cfg)
	if hs.PreparePromptDone {
		return
	}
	r := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("Host Share Preparation")
	fmt.Println("----------------------")
	fmt.Println("Yaver can audit this machine now for future host-backed guest coding sessions.")
	fmt.Println("That catches missing runners, Docker, transport, auto-start, and OS permissions")
	fmt.Println("before you generate a share code for someone else.")
	fmt.Println()
	if !promptYes(r, "Run `yaver host-share prepare` now?", true) {
		hs.PreparePromptDone = true
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not persist host-share prompt flag: %v\n", err)
		}
		fmt.Println("Skipped. Reopen it any time with `yaver host-share prepare`.")
		fmt.Println()
		return
	}
	runHostSharePrepareFlow(cfg, source, true)
	hs = ensureHostShareConfig(cfg)
	hs.PreparePromptDone = true
	if hs.CapabilityManifest != nil {
		hs.LastPreparedAt = hs.CapabilityManifest.GeneratedAt
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save host-share capability manifest: %v\n", err)
	}
}

func runHostSharePrepareFlow(cfg *Config, source string, allowPrompt bool) (*HostShareCapabilityManifest, *Config) {
	if cfg == nil {
		cfg = &Config{}
	}
	if allowPrompt && runtime.GOOS == "darwin" && stdinLooksInteractive() && !cfg.MacOSPermissionOnboardingDone {
		r := bufio.NewReader(os.Stdin)
		fmt.Println()
		fmt.Println("Before Yaver can share this host deeply, macOS permissions should be front-loaded.")
		if promptYes(r, "Run the macOS permission checklist now?", true) {
			runMacOSPermissionOnboarding(cfg, source, false)
		}
	}
	manifest := auditHostShareCapabilities(cfg)
	ensureHostShareConfig(cfg).CapabilityManifest = manifest
	printHostShareManifest(manifest)
	return manifest, cfg
}

func auditHostShareCapabilities(cfg *Config) *HostShareCapabilityManifest {
	if cfg == nil {
		cfg = &Config{}
	}
	hostname, _ := os.Hostname()
	manifest := &HostShareCapabilityManifest{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Hostname:    hostname,
		Platform:    runtime.GOOS,
		Arch:        runtime.GOARCH,
	}
	if rt := detectWSLRuntime(); rt.IsWSL {
		manifest.WSL = fmt.Sprintf("wsl%d", rt.Version)
	}
	manifest.Permissions = auditHostSharePermissions(cfg)
	manifest.Runtime = auditHostShareRuntime()
	manifest.Transport = auditHostShareTransport(cfg)
	manifest.Service = auditHostShareService(cfg)
	manifest.Summary = summarizeHostShareCapabilities(manifest)
	return manifest
}

func auditHostSharePermissions(cfg *Config) HostSharePermissionAudit {
	out := HostSharePermissionAudit{
		Interactive: stdinLooksInteractive(),
		RequestedCapabilities: []string{
			"terminal", "screen-capture", "desktop-control", "browser-automation",
			"voice", "background-service", "relay-networking",
		},
	}
	if runtime.GOOS == "darwin" {
		out.MacOSChecklistCompleted = cfg.MacOSPermissionOnboardingDone
		if !out.MacOSChecklistCompleted {
			out.MissingCapabilities = append(out.MissingCapabilities,
				"Accessibility", "Screen Recording", "Automation", "Microphone")
		}
	}
	return out
}

func auditHostShareRuntime() HostShareRuntimeAudit {
	out := HostShareRuntimeAudit{
		GitInstalled:         hostShareCommandExists("git"),
		NodeInstalled:        hostShareCommandExists("node"),
		TmuxInstalled:        tmuxAvailable(),
		CloudflaredInstalled: hostShareCommandExists("cloudflared"),
		TailscaleInstalled:   hostShareCommandExists("tailscale"),
	}
	cr := NewContainerRunner()
	out.DockerAvailable = cr.IsAvailable()
	if hostShareCommandExists("claude") {
		out.InstalledCodingRunners = append(out.InstalledCodingRunners, "claude")
	}
	if hostShareCommandExists("codex") {
		out.InstalledCodingRunners = append(out.InstalledCodingRunners, "codex")
	}
	if hostShareCommandExists("aider") {
		out.InstalledCodingRunners = append(out.InstalledCodingRunners, "aider")
	}
	sort.Strings(out.InstalledCodingRunners)
	return out
}

func auditHostShareTransport(cfg *Config) HostShareTransportAudit {
	out := HostShareTransportAudit{}
	out.SameLANLikely = localNonLoopbackIPExists()
	out.TailscaleConnected = tailscaleConnected()
	out.CloudflareConfigured = cfg != nil && len(cfg.CloudflareTunnels) > 0
	out.CustomRelayConfigured = cfg != nil && len(cfg.RelayServers) > 0
	out.CustomRelayHealthy = customRelayHealthy(cfg)
	out.FreeRelayLikely = false
	if cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" && strings.TrimSpace(cfg.ConvexSiteURL) != "" {
		out.FreeRelayLikely = true
		if relays, err := FetchRelayServers(cfg.ConvexSiteURL); err == nil && len(relays) == 0 {
			out.Warnings = append(out.Warnings, "no relay servers returned by backend")
		} else if err != nil {
			out.Warnings = append(out.Warnings, "could not verify backend relay list: "+err.Error())
		}
	}
	out.TransportOrder = deriveHostShareTransportOrder(out)
	for _, mode := range out.TransportOrder {
		switch mode {
		case "same-lan":
			if out.SameLANLikely {
				out.ReachableModes = append(out.ReachableModes, mode)
			}
		case "tailscale":
			if out.TailscaleConnected {
				out.ReachableModes = append(out.ReachableModes, mode)
			}
		case "cloudflare-tunnel":
			if out.CloudflareConfigured {
				out.ReachableModes = append(out.ReachableModes, mode)
			}
		case "custom-relay":
			if out.CustomRelayConfigured {
				out.ReachableModes = append(out.ReachableModes, mode)
			}
		case "free-relay":
			if out.FreeRelayLikely {
				out.ReachableModes = append(out.ReachableModes, mode)
			}
		}
	}
	rt := detectWSLRuntime()
	if rt.IsWSL && rt.Version == 2 && !out.TailscaleConnected && !out.CustomRelayHealthy && !out.FreeRelayLikely {
		out.Warnings = append(out.Warnings, "WSL2 host without a healthy overlay/relay path may be LAN-only")
	}
	return out
}

func auditHostShareService(cfg *Config) HostShareServiceAudit {
	out := HostShareServiceAudit{
		Authenticated:      cfg != nil && strings.TrimSpace(cfg.AuthToken) != "",
		AutoStartInstalled: isAutoStartInstalled(),
	}
	out.AgentRunning = probeLocalAgentHealthInfo(18080) != nil
	if !out.AgentRunning {
		if _, running := isAgentRunning(); running {
			out.AgentRunning = true
		}
	}
	return out
}

func summarizeHostShareCapabilities(m *HostShareCapabilityManifest) HostShareSummary {
	var missing []string
	readyRunners := append([]string{}, m.Runtime.InstalledCodingRunners...)
	if len(readyRunners) == 0 {
		missing = append(missing, "no coding runners installed (claude/codex/aider)")
	}
	if !m.Service.Authenticated {
		missing = append(missing, "not signed in")
	}
	if !m.Service.AutoStartInstalled {
		missing = append(missing, "auto-start not installed")
	}
	if len(m.Transport.ReachableModes) == 0 {
		missing = append(missing, "no viable transport path detected")
	}
	if runtime.GOOS == "darwin" && !m.Permissions.MacOSChecklistCompleted {
		missing = append(missing, "macOS permission checklist not completed")
	}
	terminalReady := len(readyRunners) > 0 && m.Service.Authenticated
	borrowedReady := terminalReady && len(m.Transport.ReachableModes) > 0
	guestFriendly := borrowedReady && m.Service.AutoStartInstalled
	return HostShareSummary{
		BorrowedRunnerReady: borrowedReady,
		TerminalReady:       terminalReady,
		GuestFriendlyReady:  guestFriendly,
		ReadyRunners:        readyRunners,
		Missing:             missing,
	}
}

func deriveHostShareTransportOrder(a HostShareTransportAudit) []string {
	order := []string{}
	if a.SameLANLikely {
		order = append(order, "same-lan")
	}
	if a.TailscaleConnected {
		order = append(order, "tailscale")
	}
	if a.CloudflareConfigured {
		order = append(order, "cloudflare-tunnel")
	}
	if a.CustomRelayConfigured {
		order = append(order, "custom-relay")
	}
	if a.FreeRelayLikely {
		order = append(order, "free-relay")
	}
	if len(order) == 0 {
		order = append(order, "same-lan", "tailscale", "cloudflare-tunnel", "custom-relay", "free-relay")
	}
	return order
}

func printHostShareManifest(m *HostShareCapabilityManifest) {
	if m == nil {
		return
	}
	fmt.Println()
	fmt.Println("Host Share Preparation")
	fmt.Println("----------------------")
	fmt.Printf("Host:       %s\n", firstNonEmpty(strings.TrimSpace(m.Hostname), "unknown"))
	fmt.Printf("Platform:   %s/%s\n", m.Platform, m.Arch)
	if m.WSL != "" {
		fmt.Printf("Runtime:    %s\n", m.WSL)
	}
	fmt.Printf("Generated:  %s\n", m.GeneratedAt)
	fmt.Println()

	fmt.Println("Permissions")
	if runtime.GOOS == "darwin" {
		state := "pending"
		if m.Permissions.MacOSChecklistCompleted {
			state = "completed"
		}
		fmt.Printf("  macOS checklist: %s\n", state)
	} else {
		fmt.Println("  OS prompts: no dedicated checklist on this platform yet")
	}
	if len(m.Permissions.MissingCapabilities) > 0 {
		fmt.Printf("  Missing:  %s\n", strings.Join(m.Permissions.MissingCapabilities, ", "))
	}
	fmt.Println()

	fmt.Println("Runtime")
	fmt.Printf("  git:      %s\n", yesNo(m.Runtime.GitInstalled))
	fmt.Printf("  node:     %s\n", yesNo(m.Runtime.NodeInstalled))
	fmt.Printf("  docker:   %s\n", yesNo(m.Runtime.DockerAvailable))
	fmt.Printf("  tmux:     %s\n", yesNo(m.Runtime.TmuxInstalled))
	fmt.Printf("  cloudflared: %s\n", yesNo(m.Runtime.CloudflaredInstalled))
	fmt.Printf("  tailscale:   %s\n", yesNo(m.Runtime.TailscaleInstalled))
	if len(m.Runtime.InstalledCodingRunners) > 0 {
		fmt.Printf("  runners:  %s\n", strings.Join(m.Runtime.InstalledCodingRunners, ", "))
	} else {
		fmt.Println("  runners:  none detected")
	}
	fmt.Println()

	fmt.Println("Transport")
	fmt.Printf("  same LAN:         %s\n", yesNo(m.Transport.SameLANLikely))
	fmt.Printf("  tailscale:        %s\n", yesNo(m.Transport.TailscaleConnected))
	fmt.Printf("  cloudflare:       %s\n", yesNo(m.Transport.CloudflareConfigured))
	fmt.Printf("  custom relay:     %s\n", yesNo(m.Transport.CustomRelayConfigured))
	if m.Transport.CustomRelayConfigured {
		fmt.Printf("  relay healthy:    %s\n", yesNo(m.Transport.CustomRelayHealthy))
	}
	fmt.Printf("  free relay:       %s\n", yesNo(m.Transport.FreeRelayLikely))
	fmt.Printf("  order:            %s\n", strings.Join(m.Transport.TransportOrder, " -> "))
	if len(m.Transport.Warnings) > 0 {
		for _, w := range m.Transport.Warnings {
			fmt.Printf("  warning:          %s\n", w)
		}
	}
	fmt.Println()

	fmt.Println("Service")
	fmt.Printf("  signed in:   %s\n", yesNo(m.Service.Authenticated))
	fmt.Printf("  agent up:    %s\n", yesNo(m.Service.AgentRunning))
	fmt.Printf("  auto-start:  %s\n", yesNo(m.Service.AutoStartInstalled))
	fmt.Println()

	fmt.Println("Result")
	fmt.Printf("  Borrowed runner sessions: %s\n", readinessLabel(m.Summary.BorrowedRunnerReady))
	fmt.Printf("  Terminal-backed sessions: %s\n", readinessLabel(m.Summary.TerminalReady))
	fmt.Printf("  Guest-friendly host mode: %s\n", readinessLabel(m.Summary.GuestFriendlyReady))
	if len(m.Summary.Missing) > 0 {
		fmt.Println("  Missing:")
		for _, item := range m.Summary.Missing {
			fmt.Printf("    - %s\n", item)
		}
	}
	if len(m.Summary.ReadyRunners) > 0 {
		fmt.Printf("  Ready runners: %s\n", strings.Join(m.Summary.ReadyRunners, ", "))
	}
	fmt.Println()
	fmt.Println("Next steps")
	for _, step := range describeHostShareNextSteps(m) {
		fmt.Printf("  - %s\n", step)
	}
	fmt.Println()
}

func describeHostShareNextSteps(m *HostShareCapabilityManifest) []string {
	steps := []string{}
	if !m.Service.Authenticated {
		steps = append(steps, "Run `yaver auth` so this host can use the included free relay and be shareable off-LAN.")
	}
	if runtime.GOOS == "darwin" && !m.Permissions.MacOSChecklistCompleted {
		steps = append(steps, "Run `yaver permissions` to front-load Accessibility, Screen Recording, Automation, and Microphone prompts.")
	}
	if !m.Service.AutoStartInstalled {
		steps = append(steps, "Run `yaver serve` once interactively so Yaver installs its background auto-start service.")
	}
	if len(m.Runtime.InstalledCodingRunners) == 0 {
		steps = append(steps, "Install at least one coding runner on the host machine: `claude`, `codex`, or `aider`.")
	}
	if !m.Runtime.DockerAvailable {
		steps = append(steps, "Install Docker if you want isolated borrowed-runner sessions and safer guest task execution.")
	}
	if !m.Transport.CloudflareConfigured && !m.Transport.CustomRelayConfigured && !m.Transport.TailscaleConnected && !m.Transport.FreeRelayLikely {
		steps = append(steps, "Configure at least one off-LAN path: sign in for Yaver free relay, or add Tailscale / Cloudflare Tunnel / a custom relay.")
	}
	if len(steps) == 0 {
		steps = append(steps, "This host is in good shape for the upcoming host-backed guest coding flow.")
	}
	return steps
}

func hostShareCommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func readinessLabel(v bool) string {
	if v {
		return "ready"
	}
	return "not ready"
}

func localNonLoopbackIPExists() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.To4() != nil || ip.To16() != nil {
				return true
			}
		}
	}
	return false
}

func tailscaleConnected() bool {
	if !hostShareCommandExists("tailscale") {
		return false
	}
	cmd := exec.Command("tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var parsed struct {
		BackendState string `json:"BackendState"`
		Self         *struct {
			Online bool `json:"Online"`
		} `json:"Self"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return false
	}
	if strings.EqualFold(parsed.BackendState, "running") {
		return parsed.Self == nil || parsed.Self.Online
	}
	return false
}

func customRelayHealthy(cfg *Config) bool {
	if cfg == nil || len(cfg.RelayServers) == 0 {
		return false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, rs := range cfg.RelayServers {
		u := strings.TrimRight(strings.TrimSpace(rs.HttpURL), "/")
		if u == "" {
			continue
		}
		resp, err := client.Get(u + "/health")
		if err == nil && resp != nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
	}
	return false
}
