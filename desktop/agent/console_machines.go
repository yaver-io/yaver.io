package main

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/host"
)

// MachineInfo is the universal descriptor for anything that runs a Yaver agent.
// Spans: local Mac Mini, home Linux box, Hetzner VPS, Yaver Cloud instance, etc.
type MachineInfo struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	IsLocal  bool   `json:"isLocal"`
	IsOnline bool   `json:"isOnline"`
	Provider string `json:"provider,omitempty"` // hetzner, digitalocean, local, yaver-cloud
	Cost     string `json:"cost,omitempty"`     // best-effort monthly cost label
	Uptime   uint64 `json:"uptime,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	QuicHost string `json:"quicHost,omitempty"`
	QuicPort int    `json:"quicPort,omitempty"`
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
		DeviceID: "local",
		Name:     name,
		Hostname: hostname,
		Platform: platform,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		IsLocal:  true,
		IsOnline: true,
		Uptime:   uptime,
		Provider: detectSelfProvider(),
		Cost:     "$0",
	}
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

// ---- HTTP ----

func (s *HTTPServer) handleConsoleMachines(w http.ResponseWriter, r *http.Request) {
	list := listAllMachines(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{"machines": list})
}

// mcpConsoleMachines is the MCP tool for the agent-chat interface.
func mcpConsoleMachines() interface{} {
	return map[string]interface{}{"machines": listAllMachines(context.Background())}
}
