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
)

// MachineInfo is the universal descriptor for anything that runs a Yaver agent.
// Spans: local Mac Mini, home Linux box, Hetzner VPS, Yaver Cloud instance, etc.
type MachineInfo struct {
	DeviceID      string               `json:"deviceId"`
	Name          string               `json:"name"`
	Platform      string               `json:"platform"`
	OS            string               `json:"os"`
	Arch          string               `json:"arch"`
	IsLocal       bool                 `json:"isLocal"`
	IsOnline      bool                 `json:"isOnline"`
	Provider      string               `json:"provider,omitempty"` // hetzner, digitalocean, local, yaver-cloud
	Cost          string               `json:"cost,omitempty"`     // best-effort monthly cost label
	Uptime        uint64               `json:"uptime,omitempty"`
	Hostname      string               `json:"hostname,omitempty"`
	QuicHost      string               `json:"quicHost,omitempty"`
	QuicPort      int                  `json:"quicPort,omitempty"`
	CurrentWorkDir string              `json:"currentWorkDir,omitempty"`
	Capabilities  *MachineCapabilities `json:"capabilities,omitempty"`
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
	Hardware           HardwareProfile          `json:"hardware"`
	Runners            []MachineRunnerCapability `json:"runners"`
	SupportsIOS        bool                     `json:"supportsIos"`
	SupportsAndroid    bool                     `json:"supportsAndroid"`
	SupportsDocker     bool                     `json:"supportsDocker"`
	SupportsLocalLLM   bool                     `json:"supportsLocalLlm"`
	SupportsTestFlight bool                  `json:"supportsTestFlight"`
	SupportsPlayStore bool                     `json:"supportsPlayStore"`
	LowPower          bool                     `json:"lowPower"`
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
					DeviceID: d.DeviceID, Name: d.Name, Platform: d.Platform,
					IsOnline: d.IsOnline, QuicHost: d.QuicHost, QuicPort: d.QuicPort,
					Provider: providerFromHint(d.Platform, d.QuicHost),
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
	// Hetzner IP ranges start with 5.xx, 116.xx, 159.xx, etc. — too broad to match.
	// We keep it vague unless the device explicitly reports its provider.
	return ""
}

func detectMachineCapabilities(workDir string) *MachineCapabilities {
	caps := &MachineCapabilities{
		Hardware: DetectHardware(),
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
	caps.LowPower = caps.Hardware.CPUCores <= 4 || strings.Contains(strings.ToLower(caps.Hardware.Arch), "arm")
	return caps
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
	base, token, err := remoteAgentBaseAndToken(m.DeviceID)
	if err != nil {
		return nil, err
	}
	var out struct {
		OK      bool        `json:"ok"`
		Machine MachineInfo `json:"machine"`
	}
	if err := remoteAgentJSON(ctx, base, token, http.MethodGet, "/agent/capabilities", nil, &out); err != nil {
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
