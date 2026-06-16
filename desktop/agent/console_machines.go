package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/yaver-io/agent/ghost"
)

// MachineInfo is the universal descriptor for anything that runs a Yaver agent.
// Spans: local Mac Mini, home Linux box, Hetzner VPS, Yaver Cloud instance, etc.
type MachineInfo struct {
	DeviceID       string               `json:"deviceId"`
	Name           string               `json:"name"`
	Platform       string               `json:"platform"`
	OS             string               `json:"os"`
	Arch           string               `json:"arch"`
	IsLocal        bool                 `json:"isLocal"`
	IsOnline       bool                 `json:"isOnline"`
	Provider       string               `json:"provider,omitempty"` // hetzner, digitalocean, local, yaver-cloud
	Cost           string               `json:"cost,omitempty"`     // best-effort monthly cost label
	Uptime         uint64               `json:"uptime,omitempty"`
	Hostname       string               `json:"hostname,omitempty"`
	QuicHost       string               `json:"quicHost,omitempty"`
	QuicPort       int                  `json:"quicPort,omitempty"`
	CurrentWorkDir string               `json:"currentWorkDir,omitempty"`
	Capabilities   *MachineCapabilities `json:"capabilities,omitempty"`
	GeoRegion      string               `json:"geoRegion,omitempty"` // coarse egress region only: eu|us|ap|...

	// Shared-infrastructure fields: when true, this machine is not owned by
	// the current user but was shared with them via guest access on another
	// host account. The mesh planner uses these to score placement and
	// enforce the host's resource policy.
	IsShared                  bool   `json:"isShared,omitempty"`
	HostName                  string `json:"hostName,omitempty"`
	HostEmail                 string `json:"hostEmail,omitempty"`
	AccessScope               string `json:"accessScope,omitempty"`  // owner | shared-scoped | shared-legacy
	PriorityMode              string `json:"priorityMode,omitempty"` // "", "spare-capacity", "always", "scheduled"
	UseHostAPIKeys            bool   `json:"useHostApiKeys,omitempty"`
	AllowGuestProvidedAPIKeys bool   `json:"allowGuestProvidedApiKeys,omitempty"`
}

type MachineRunnerCapability struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

type MachineCapabilities struct {
	Hardware             HardwareProfile           `json:"hardware"`
	Runners              []MachineRunnerCapability `json:"runners"`
	SupportsIOS          bool                      `json:"supportsIos"`
	SupportsAndroid      bool                      `json:"supportsAndroid"`
	SupportsDocker       bool                      `json:"supportsDocker"`
	SupportsLocalLLM     bool                      `json:"supportsLocalLlm"`
	SupportsTestFlight   bool                      `json:"supportsTestFlight"`
	SupportsPlayStore    bool                      `json:"supportsPlayStore"`
	SupportsGhostUI      bool                      `json:"supportsGhostUi"`      // native desktop ghost (Windows now; macOS w/ cgo)
	SupportsGhostWeb     bool                      `json:"supportsGhostWeb"`     // web ghost (chromedp) — cross-OS incl. RPi appliance
	SupportsMachineSniff bool                      `json:"supportsMachineSniff"` // machine/PLC hijack: Modbus TCP everywhere; serial sniff on Linux
	LowPower             bool                      `json:"lowPower"`
	MaxTaskSlots         int                       `json:"maxTaskSlots"`
	Profile              *MachineProfile           `json:"profile,omitempty"`
}

// listAllMachines returns every Yaver-managed machine this user owns — the
// agent running this call first, then every other registered device. Browsers
// already know how to re-target a specific device via the existing relay
// connection (`{relay}/d/{deviceId}`), so this list is what the machine picker
// in the Console renders.
func listAllMachines(ctx context.Context) []MachineInfo {
	var out []MachineInfo

	// Local first — we know this machine intimately.
	me := selfMachine(ctx)
	out = append(out, me)

	// Remote devices from Convex registry.
	cfg, err := LoadConfig()
	if err == nil && cfg.AuthToken != "" && cfg.ConvexSiteURL != "" {
		if remote, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			for _, d := range remote {
				// Skip the device that's us (matched by hostname — imperfect but fine).
				if d.Name == me.Name || d.DeviceID == me.DeviceID {
					continue
				}
				out = append(out, MachineInfo{
					DeviceID:                  d.DeviceID,
					Name:                      d.Name,
					Platform:                  d.Platform,
					IsOnline:                  d.IsOnline,
					QuicHost:                  d.QuicHost,
					QuicPort:                  d.QuicPort,
					GeoRegion:                 d.GeoRegion,
					Provider:                  providerFromHint(d.Platform, d.QuicHost),
					IsShared:                  d.IsGuest,
					HostName:                  d.HostName,
					HostEmail:                 d.HostEmail,
					AccessScope:               d.AccessScope,
					PriorityMode:              d.PriorityMode,
					UseHostAPIKeys:            d.UseHostAPIKeys,
					AllowGuestProvidedAPIKeys: d.AllowGuestProvidedAPIKeys,
				})
			}
		}
	}
	enrichMachinesWithCapabilities(ctx, out)
	return out
}

func selfMachine(ctx context.Context) MachineInfo {
	info, _ := host.InfoWithContext(ctx)
	name := "local"
	platform := runtime.GOOS
	uptime := uint64(0)
	hostname := ""
	if info != nil {
		name = info.Hostname
		hostname = info.Hostname
		platform = info.Platform + " " + info.PlatformVersion
		uptime = info.Uptime
	}
	return MachineInfo{
		DeviceID:       "local",
		Name:           name,
		Hostname:       hostname,
		Platform:       platform,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		IsLocal:        true,
		IsOnline:       true,
		Uptime:         uptime,
		Provider:       detectSelfProvider(),
		Cost:           "$0",
		CurrentWorkDir: localCurrentWorkDir(),
		Capabilities:   detectMachineCapabilities(localCurrentWorkDir()),
		GeoRegion:      cachedEgressRegion(),
	}
}

func localCurrentWorkDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// detectSelfProvider inspects the environment to guess whether this agent is
// running on user hardware or a known cloud host.
func detectSelfProvider() string {
	// Hetzner metadata: 169.254.169.254 returns data for Hetzner instances.
	client := &http.Client{Timeout: 300 * time.Millisecond}
	if res, err := client.Get("http://169.254.169.254/hetzner/v1/metadata/hostname"); err == nil {
		defer res.Body.Close()
		if res.StatusCode == 200 {
			return "hetzner"
		}
	}
	// AWS EC2 signals
	if res, err := client.Get("http://169.254.169.254/latest/meta-data/instance-id"); err == nil {
		defer res.Body.Close()
		if res.StatusCode == 200 {
			return "aws"
		}
	}
	// GCP
	if req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/id", nil); err == nil {
		req.Header.Set("Metadata-Flavor", "Google")
		if res, err := client.Do(req); err == nil {
			defer res.Body.Close()
			if res.StatusCode == 200 {
				return "gcp"
			}
		}
	}
	if runtime.GOOS == "darwin" {
		return "local-mac"
	}
	return "local"
}

// providerFromHint infers a provider label from a remote device's descriptor.
func providerFromHint(platform, quicHost string) string {
	if platform == "darwin" {
		return "local-mac"
	}
	hint := strings.ToLower(platform + " " + quicHost)
	if strings.Contains(hint, "hel1") || strings.Contains(hint, "fsn1") || strings.Contains(hint, "nbg1") || strings.Contains(hint, "hetzner") {
		return "hetzner"
	}
	// Hetzner IP ranges start with 5.xx, 116.xx, 159.xx, etc. — too broad to match.
	// We keep it vague unless the device explicitly reports its provider.
	return ""
}

func detectMachineCapabilities(workDir string) *MachineCapabilities {
	caps := &MachineCapabilities{
		Hardware: DetectHardware(),
		Profile:  loadMachineProfile(workDir),
	}
	for _, id := range []string{"claude", "codex", "aider", "aider-ollama", "ollama", "opencode"} {
		cfg := GetRunnerConfig(id)
		if cfg.RunnerID == "" || cfg.Command == "" {
			continue
		}
		status := DetectRunnerRuntimeStatus(cfg, workDir)
		_, lookErr := exec.LookPath(cfg.Command)
		caps.Runners = append(caps.Runners, MachineRunnerCapability{
			ID:             cfg.RunnerID,
			Name:           cfg.Name,
			Installed:      lookErr == nil,
			Ready:          lookErr == nil && status.Ready,
			AuthConfigured: status.AuthConfigured,
			AuthSource:     status.AuthSource,
			Warning:        status.Warning,
			Error:          status.Error,
		})
	}
	caps.SupportsDocker = caps.Hardware.DockerOK
	caps.SupportsLocalLLM = machineHasReadyRunner(caps.Runners, "ollama") || machineHasReadyRunner(caps.Runners, "aider-ollama")
	caps.SupportsTestFlight = runtime.GOOS == "darwin" && toolLooksInstalled("xcrun")
	caps.SupportsIOS = caps.SupportsTestFlight || runtime.GOOS == "darwin"
	caps.SupportsPlayStore = toolLooksInstalled("java") || toolLooksInstalled("javac") || toolLooksInstalled("gradle")
	caps.SupportsAndroid = caps.SupportsPlayStore || toolLooksInstalled("adb")
	// GUI ghost capability: the OS must have a screen+input implementation
	// (Phase 1: Windows). Actual invocation is still gated per-agent by the
	// --ghost opt-in at verb-call time; this only advertises platform support.
	caps.SupportsGhostUI = ghost.Supported()
	// Web ghost (chromedp/headless browser) is cross-platform — it runs on the
	// on-prem RPi appliance (ARM Linux) for web-UI ERPs. Chrome/Chromium is
	// resolved at runtime; the verbs report cleanly if it's missing.
	caps.SupportsGhostWeb = true
	// Machine/PLC hijack: Modbus-TCP works on every platform; serial (RTU) bus
	// sniffing needs the Linux termios path. Gated per-agent by --machine at
	// verb-call time; this only advertises platform support.
	caps.SupportsMachineSniff = true
	caps.LowPower = caps.Hardware.CPUCores <= 4 || strings.Contains(strings.ToLower(caps.Hardware.Arch), "arm")
	if caps.Profile != nil {
		if profileHasAny(caps.Profile, "testflight", "xcode", "ios") {
			caps.SupportsIOS = true
			caps.SupportsTestFlight = true
		}
		if profileHasAny(caps.Profile, "android", "playstore", "gradle") {
			caps.SupportsAndroid = true
			caps.SupportsPlayStore = true
		}
		if profileHasAny(caps.Profile, "ollama", "local-llm") {
			caps.SupportsLocalLLM = true
		}
	}
	caps.MaxTaskSlots = machineTaskCapacity(caps)
	return caps
}

func machineTaskCapacity(caps *MachineCapabilities) int {
	if caps == nil {
		return 1
	}
	if caps.LowPower {
		return 1
	}
	base := caps.Hardware.MaxParallel
	switch {
	case base >= 8:
		return 3
	case base >= 4:
		return 2
	default:
		return 1
	}
}

func profileHasAny(profile *MachineProfile, values ...string) bool {
	if profile == nil {
		return false
	}
	haystack := append([]string{}, profile.Tags...)
	haystack = append(haystack, profile.Signatures...)
	haystack = append(haystack, profile.PreferredFor...)
	joined := strings.Join(haystack, " ")
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if strings.Contains(joined, value) {
			return true
		}
	}
	return false
}

func machineHasReadyRunner(runners []MachineRunnerCapability, id string) bool {
	for _, r := range runners {
		if r.ID == id && r.Ready {
			return true
		}
	}
	return false
}

func toolLooksInstalled(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func enrichMachinesWithCapabilities(ctx context.Context, machines []MachineInfo) {
	if len(machines) == 0 {
		return
	}
	for i := range machines {
		if machines[i].IsLocal {
			continue
		}
		if !machines[i].IsOnline {
			continue
		}
		if info, err := fetchRemoteMachineCapabilities(ctx, machines[i]); err == nil {
			machines[i].Capabilities = info.Capabilities
			if info.CurrentWorkDir != "" {
				machines[i].CurrentWorkDir = info.CurrentWorkDir
			}
			if info.OS != "" {
				machines[i].OS = info.OS
			}
			if info.Arch != "" {
				machines[i].Arch = info.Arch
			}
		}
	}
}

func fetchRemoteMachineCapabilities(ctx context.Context, m MachineInfo) (*MachineInfo, error) {
	var out struct {
		OK      bool        `json:"ok"`
		Machine MachineInfo `json:"machine"`
	}
	if err := remoteAgentJSONForDevice(ctx, m.DeviceID, http.MethodGet, "/agent/capabilities", nil, &out); err != nil {
		return nil, err
	}
	return &out.Machine, nil
}

// ---- HTTP ----

func (s *HTTPServer) handleConsoleMachines(w http.ResponseWriter, r *http.Request) {
	list := listAllMachines(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{"machines": list})
}

func (s *HTTPServer) handleAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	info := selfMachine(r.Context())
	if s.taskMgr != nil {
		info.CurrentWorkDir = s.taskMgr.workDir
		info.Capabilities = detectMachineCapabilities(info.CurrentWorkDir)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"machine": info,
	})
}

// mcpConsoleMachines is the MCP tool for the agent-chat interface.
func mcpConsoleMachines() interface{} {
	return map[string]interface{}{"machines": listAllMachines(context.Background())}
}
