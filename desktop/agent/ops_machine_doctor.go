package main

// machine_doctor — answer "what is actually wrong with that machine?" in ONE
// call, for a REMOTE box, without a Convex round-trip.
//
// Why this exists. Debugging a box you can't reach used to mean reading a wall
// of six identical lines:
//
//   remote GET /agent/runners failed across 6 candidate(s):
//     http://100.89.155.25:18080: context deadline exceeded |
//     http://192.168.111.25:18080: context deadline exceeded | … ×6
//
// That output is worse than useless — it looks like six independent findings but
// it is really ONE. doRemoteAgentRequest walks candidates SEQUENTIALLY, each
// with the full timeout, so the parent context expires partway down the list and
// every remaining leg reports "context deadline exceeded" without ever having
// been dialled. You cannot tell a firewalled port from a dead relay from a leg
// that was never tried.
//
// So this verb probes every leg CONCURRENTLY, each with its own short deadline,
// and reports what each one actually did: connection refused (nothing is
// listening), timeout (packets vanished — firewall/NAT/wrong subnet), DNS
// failure, TLS failure, or an HTTP status (it answered! auth may still be wrong).
// A refused leg and a black-holed leg are different bugs and must not collapse
// into the same sentence.
//
// COST CONTRACT: this verb performs ZERO Convex calls. It talks only to the
// agent's own HTTP surface. Diagnosing a sick machine must never add to the
// bill, and must keep working when Convex is exactly the thing that's down.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_doctor",
		Description: "Diagnose a remote machine in one call: probe every transport leg CONCURRENTLY with honest per-leg verdicts (refused vs timeout vs TLS vs HTTP status + latency), then report coding-agent readiness (claude/codex/opencode: installed + authenticated) and resources (CPU, RAM, disk). Zero Convex calls. Use when a box says 'online' but nothing can reach it, or a task dies with a bare timeout.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "deviceId, alias (e.g. 'primary', 'mac-mini'), or name. Omit for this machine."},
			"timeoutMs": map[string]interface{}{
				"type":        "integer",
				"description": "Per-leg dial+response budget. Default 4000. Legs run in parallel, so total ≈ this, not N×this.",
			},
		}),
		Handler:    machineDoctorHandler,
		AllowGuest: false,
	})
}

// legVerdict is one transport leg's honest outcome. `Class` is the machine-
// readable verdict; `Detail` is the raw error for a human.
type legVerdict struct {
	BaseURL   string          `json:"baseUrl"`
	Host      string          `json:"host,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Label     string          `json:"label,omitempty"`
	IPLayer   string          `json:"ipLayer,omitempty"` // same-lan|tailscale|mesh|relay-gateway|public|hostname
	OK        bool            `json:"ok"`
	Class     string          `json:"class"` // reachable|refused|timeout|dns|tls|http_error|error
	Status    int             `json:"status,omitempty"`
	LatencyMs int64           `json:"latencyMs"`
	Detail    string          `json:"detail,omitempty"`
	RawPing   *rawPingVerdict `json:"rawPing,omitempty"`
}

type runnerVerdict struct {
	ID        string `json:"id"`
	Installed bool   `json:"installed"`
	// Authenticated mirrors the agent's authConfigured. Ready mirrors its ready.
	// They are reported SEPARATELY on purpose: "installed but signed out" and
	// "signed in but broken config" are different problems with different fixes,
	// and collapsing them into one boolean is what let a dead runner advertise
	// itself as usable.
	Authenticated bool   `json:"authenticated"`
	Ready         bool   `json:"ready"`
	Verified      bool   `json:"verified"`
	Note          string `json:"note,omitempty"`
}

type rawPingVerdict struct {
	OK        bool   `json:"ok"`
	Class     string `json:"class"` // reachable|timeout|unavailable|error
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type heartbeatVerdict struct {
	Online          bool   `json:"online"`
	NeedsAuth       bool   `json:"needsAuth,omitempty"`
	RelayConnected  bool   `json:"relayConnected,omitempty"`
	LastHeartbeatMs int64  `json:"lastHeartbeatMs,omitempty"`
	AgeSeconds      int64  `json:"ageSeconds,omitempty"`
	Fresh           bool   `json:"fresh"`
	AgentVersion    string `json:"agentVersion,omitempty"`
	Source          string `json:"source"`
}

type tailscaleVerdict struct {
	LocalRunning bool     `json:"localRunning"`
	Backend      string   `json:"backend,omitempty"`
	LocalIPs     []string `json:"localIps,omitempty"`
	TargetIPs    []string `json:"targetIps,omitempty"`
}

type targetVerdict struct {
	DeviceID        string   `json:"deviceId,omitempty"`
	Name            string   `json:"name,omitempty"`
	Alias           string   `json:"alias,omitempty"`
	Platform        string   `json:"platform,omitempty"`
	Hosting         string   `json:"hosting,omitempty"` // managed|byo|self-hosted|shared
	Managed         bool     `json:"managed,omitempty"`
	MachineStatus   string   `json:"machineStatus,omitempty"`
	AccessScope     string   `json:"accessScope,omitempty"`
	QuicHost        string   `json:"quicHost,omitempty"`
	QuicPort        int      `json:"quicPort,omitempty"`
	LocalIPs        []string `json:"localIps,omitempty"`
	PublicEndpoints []string `json:"publicEndpoints,omitempty"`
	ExpectedPath    string   `json:"expectedPath,omitempty"`
}

type machineDoctorReport struct {
	Device string `json:"device"`
	// Reachable is the ONLY field that answers "can I use this box right now".
	// "online" (a heartbeat landed in Convex) is NOT reachability — a box can
	// heartbeat happily while every transport to it is black-holed.
	Reachable bool         `json:"reachable"`
	Via       string       `json:"via,omitempty"`
	Legs      []legVerdict `json:"legs"`

	Target    *targetVerdict    `json:"target,omitempty"`
	Heartbeat *heartbeatVerdict `json:"heartbeat,omitempty"`
	Tailscale *tailscaleVerdict `json:"tailscale,omitempty"`
	Runners   []runnerVerdict   `json:"runners,omitempty"`
	Resources *doctorReso       `json:"resources,omitempty"`

	// Summary is a one-line plain-English verdict — the thing to show a user
	// who does not want to read six legs.
	Summary string `json:"summary"`
	// Advice names the next concrete action, not a platitude.
	Advice string `json:"advice,omitempty"`
}

type doctorReso struct {
	Hostname     string  `json:"hostname,omitempty"`
	Platform     string  `json:"platform,omitempty"`
	Arch         string  `json:"arch,omitempty"`
	AgentVersion string  `json:"agentVersion,omitempty"`
	CPUPercent   float64 `json:"cpuPercent,omitempty"`
	MemTotalMB   int64   `json:"memTotalMB,omitempty"`
	MemUsedMB    int64   `json:"memUsedMB,omitempty"`
	DiskFreeGB   int64   `json:"diskFreeGB,omitempty"`
}

func machineDoctorHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var in struct {
		Device     string `json:"device"`
		DeviceID   string `json:"deviceId"`
		DeviceID2  string `json:"device_id"`
		TimeoutMs  int    `json:"timeoutMs"`
		TimeoutMs2 int    `json:"timeout_ms"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &in); err != nil {
			return OpsResult{Error: "bad payload: " + err.Error(), Code: "bad_payload"}
		}
	}
	perLeg := time.Duration(in.TimeoutMs) * time.Millisecond
	if perLeg <= 0 && in.TimeoutMs2 > 0 {
		perLeg = time.Duration(in.TimeoutMs2) * time.Millisecond
	}
	if perLeg <= 0 {
		perLeg = 4 * time.Second
	}

	device := strings.TrimSpace(in.Device)
	if device == "" {
		device = strings.TrimSpace(in.DeviceID)
	}
	if device == "" {
		device = strings.TrimSpace(in.DeviceID2)
	}
	if device == "" || device == "local" {
		return OpsResult{OK: true, Initial: localMachineDoctor()}
	}

	cfg, target, err := findOwnedDeviceForHint(device)
	if err != nil {
		return OpsResult{
			Error: fmt.Sprintf("cannot resolve %q: %v", device, err),
			Code:  "not_found",
		}
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return OpsResult{Error: fmt.Sprintf("cannot build transport candidates for %q: %v", device, err), Code: "not_found"}
	}

	rep := machineDoctorReport{Device: device}
	rep.Target = targetFromDevice(target)
	rep.Heartbeat = heartbeatFromDevice(target)
	rep.Tailscale = tailscaleFromDevice(target)
	rep.Legs = probeLegsConcurrently(c.Ctx, candidates, cfg.AuthToken, perLeg)

	// First leg that actually answered wins. Sorted by latency so the fastest
	// working transport is the one we then use for the follow-up calls.
	for _, l := range rep.Legs {
		if l.OK {
			rep.Reachable = true
			rep.Via = l.BaseURL
			break
		}
	}

	if !rep.Reachable {
		rep.Summary, rep.Advice = summarizeUnreachable(rep.Legs)
		return OpsResult{OK: true, Initial: rep}
	}

	// Reachable: now ask it the two questions that actually matter.
	rep.Runners = fetchRunnerVerdicts(c.Ctx, rep.Via, cfg.AuthToken, perLeg)
	rep.Resources = fetchResources(c.Ctx, rep.Via, cfg.AuthToken, perLeg)
	rep.Summary, rep.Advice = summarizeReachable(&rep)
	return OpsResult{OK: true, Initial: rep}
}

// probeLegsConcurrently dials every candidate at once. This is the whole point:
// sequential probing with a shared deadline cannot distinguish "refused" from
// "never tried", and that ambiguity is what makes the old error message a dead
// end. Each leg gets its OWN deadline, so one black hole cannot starve the rest.
func probeLegsConcurrently(ctx context.Context, candidates []RemoteAgentCandidate, token string, perLeg time.Duration) []legVerdict {
	out := make([]legVerdict, len(candidates))
	var wg sync.WaitGroup
	for i, cand := range candidates {
		wg.Add(1)
		go func(i int, cand RemoteAgentCandidate) {
			defer wg.Done()
			out[i] = probeLeg(ctx, cand, token, perLeg)
		}(i, cand)
	}
	wg.Wait()
	// Working legs first, then by latency: the caller reads top-down and the
	// first line is the transport they should actually be using.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].OK != out[b].OK {
			return out[a].OK
		}
		return out[a].LatencyMs < out[b].LatencyMs
	})
	return out
}

func probeLeg(ctx context.Context, cand RemoteAgentCandidate, token string, perLeg time.Duration) legVerdict {
	v := legVerdict{BaseURL: cand.BaseURL, Kind: cand.Kind, Label: cand.Label}
	v.Host = hostFromBaseURL(cand.BaseURL)
	v.IPLayer = classifyDoctorIPLayer(v.Host, cand.Kind)
	v.RawPing = rawPingHost(ctx, v.Host, perLeg)
	legCtx, cancel := context.WithTimeout(ctx, perLeg)
	defer cancel()

	// /info is the cheapest authenticated endpoint the agent serves. A 401 here
	// is still a REACHABLE leg — the packets got through and something answered.
	// Conflating "wrong credentials" with "cannot reach" sends you hunting a
	// network bug that doesn't exist.
	url := strings.TrimRight(cand.BaseURL, "/") + "/info"
	req, err := http.NewRequestWithContext(legCtx, http.MethodGet, url, nil)
	if err != nil {
		v.Class, v.Detail = "error", err.Error()
		return v
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, val := range cand.Headers {
		if strings.TrimSpace(val) != "" {
			req.Header.Set(k, val)
		}
	}

	start := time.Now()
	resp, err := remoteHTTPClient(perLeg).Do(req)
	v.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		v.Class, v.Detail = classifyDialError(err)
		return v
	}
	defer resp.Body.Close()
	v.Status = resp.StatusCode
	switch {
	case resp.StatusCode < 400:
		v.OK, v.Class = true, "reachable"
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// Reachable, but this token isn't welcome. Not a transport bug.
		v.Class = "http_error"
		v.Detail = "answered but rejected the token (agent is alive; auth is the problem)"
	default:
		v.Class = "http_error"
		v.Detail = http.StatusText(resp.StatusCode)
	}
	return v
}

func heartbeatFromDevice(target *DeviceInfo) *heartbeatVerdict {
	if target == nil {
		return nil
	}
	h := &heartbeatVerdict{
		Online:          target.IsOnline,
		NeedsAuth:       target.NeedsAuth,
		RelayConnected:  target.RelayConnected,
		LastHeartbeatMs: target.LastHeartbeat,
		AgentVersion:    target.AgentVersion,
		Source:          "convex-device-registry",
	}
	if target.LastHeartbeat > 0 {
		age := time.Since(time.UnixMilli(target.LastHeartbeat))
		if age < 0 {
			age = 0
		}
		h.AgeSeconds = int64(age.Seconds())
		h.Fresh = age < 5*time.Minute
	} else {
		h.Fresh = target.IsOnline
	}
	return h
}

func targetFromDevice(target *DeviceInfo) *targetVerdict {
	if target == nil {
		return nil
	}
	hosting := deviceHostingLabel(*target)
	if target.IsGuest && !strings.HasPrefix(hosting, "shared") {
		hosting = "shared"
	}
	t := &targetVerdict{
		DeviceID:        target.DeviceID,
		Name:            target.Name,
		Alias:           target.Alias,
		Platform:        target.Platform,
		Hosting:         hosting,
		Managed:         target.Managed,
		MachineStatus:   target.MachineStatus,
		AccessScope:     target.AccessScope,
		QuicHost:        target.QuicHost,
		QuicPort:        target.QuicPort,
		LocalIPs:        append([]string(nil), target.LocalIps...),
		PublicEndpoints: append([]string(nil), target.PublicEndpoints...),
	}
	t.ExpectedPath = expectedConnectivityPath(target, hosting)
	return t
}

func expectedConnectivityPath(target *DeviceInfo, hosting string) string {
	if target == nil {
		return "unknown"
	}
	hosting = strings.ToLower(strings.TrimSpace(hosting))
	if strings.Contains(hosting, "managed") || strings.Contains(hosting, "byo") {
		if len(target.PublicEndpoints) > 0 {
			return "public-endpoint-or-relay"
		}
		return "relay"
	}
	for _, ip := range target.LocalIps {
		if isCGNATTailscaleIP(ip) {
			return "tailscale-or-relay"
		}
	}
	if len(target.PublicEndpoints) > 0 {
		return "public-endpoint-or-relay"
	}
	return "lan-or-relay"
}

func tailscaleFromDevice(target *DeviceInfo) *tailscaleVerdict {
	t := &tailscaleVerdict{}
	if st := DetectTailscale(); st != nil {
		t.LocalRunning = st.Running
		t.Backend = st.BackendState
		if st.Self != nil {
			t.LocalIPs = append(t.LocalIPs, st.Self.Addrs...)
		}
	}
	if target != nil {
		for _, ip := range target.LocalIps {
			if isCGNATTailscaleIP(ip) {
				t.TargetIPs = append(t.TargetIPs, ip)
			}
		}
	}
	return t
}

func hostFromBaseURL(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func classifyDoctorIPLayer(host, kind string) string {
	if kind == "relay" {
		return "relay-gateway"
	}
	if kind != "" && kind != "unknown" {
		return kind
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return "hostname"
	}
	switch {
	case isMeshOverlayIPv4(host):
		return "mesh"
	case isCGNATTailscaleIP(host):
		return "tailscale"
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return "same-lan"
	default:
		return "public"
	}
}

func rawPingHost(ctx context.Context, host string, timeout time.Duration) *rawPingVerdict {
	host = strings.TrimSpace(host)
	if host == "" || strings.Contains(host, ":") {
		return nil
	}
	bin, err := exec.LookPath("ping")
	if err != nil {
		return &rawPingVerdict{Class: "unavailable", Detail: "ping binary not found"}
	}
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 2 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-c", "1"}
	switch runtime.GOOS {
	case "darwin":
		args = append(args, "-W", "1000")
	case "linux":
		args = append(args, "-W", "1")
	}
	args = append(args, host)
	start := time.Now()
	out, err := exec.CommandContext(pingCtx, bin, args...).CombinedOutput()
	lat := time.Since(start)
	if err == nil {
		ms := parsePingLatencyMs(string(out))
		if ms <= 0 {
			ms = lat.Milliseconds()
		}
		return &rawPingVerdict{OK: true, Class: "reachable", LatencyMs: ms}
	}
	if errors.Is(pingCtx.Err(), context.DeadlineExceeded) {
		return &rawPingVerdict{Class: "timeout", Detail: "ICMP ping timed out"}
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	if strings.Contains(strings.ToLower(msg), "100.0% packet loss") || strings.Contains(strings.ToLower(msg), "request timeout") {
		return &rawPingVerdict{Class: "timeout", Detail: "ICMP ping got no reply"}
	}
	return &rawPingVerdict{Class: "error", Detail: msg}
}

func parsePingLatencyMs(out string) int64 {
	idx := strings.Index(out, "time=")
	if idx < 0 {
		return 0
	}
	rest := out[idx+len("time="):]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0
	}
	raw := strings.TrimSuffix(fields[0], "ms")
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return int64(f + 0.5)
}

// classifyDialError turns a Go network error into a verdict a human can act on.
// "context deadline exceeded" tells you nothing; "refused" vs "timeout" tells
// you whether to restart a process or open a firewall.
func classifyDialError(err error) (class, detail string) {
	msg := err.Error()
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", "hostname does not resolve: " + dnsErr.Name
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout"):
		return "timeout", "no reply — packets are being dropped (firewall, NAT, or wrong network)"
	case strings.Contains(msg, "connection refused"):
		return "refused", "nothing is listening on that port (agent down, or bound elsewhere)"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return "timeout", "no route to host — this leg's network is not reachable from here"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "certificate"), strings.Contains(msg, "x509"):
		return "tls", msg
	}
	return "error", msg
}

func summarizeUnreachable(legs []legVerdict) (summary, advice string) {
	counts := map[string]int{}
	for _, l := range legs {
		counts[l.Class]++
	}
	// An answered-but-rejected leg means the box is FINE and the token isn't.
	// Say so, loudly — it's the single most misdiagnosed case.
	if counts["http_error"] > 0 {
		return "Machine is reachable but rejected the token — this is an AUTH problem, not a network one.",
			"Re-authenticate: `yaver auth` on that box, or check it's signed in as the same user."
	}
	switch {
	case counts["refused"] > 0 && counts["timeout"] == 0:
		return "Every leg was refused — the agent is not listening.",
			"The box is up but `yaver serve` isn't running (or is bound to another port). Start the agent."
	case counts["timeout"] > 0 && counts["refused"] == 0:
		return "Every leg timed out — packets are being dropped, nothing refused them.",
			"The box is on a network you can't reach: wrong subnet, firewall, or a relay tunnel that's registered but dead. Bring up the shared network (e.g. Tailscale) or restart the agent to rebuild its relay tunnel."
	case counts["dns"] > 0:
		return "Hostnames didn't resolve.", "Check DNS / the device's advertised address."
	}
	return "No transport answered.", "Check the box is powered on and `yaver serve` is running."
}

func summarizeReachable(rep *machineDoctorReport) (summary, advice string) {
	var ready, installed []string
	for _, r := range rep.Runners {
		if r.Authenticated {
			ready = append(ready, r.ID)
		} else if r.Installed {
			installed = append(installed, r.ID)
		}
	}
	base := fmt.Sprintf("Reachable via %s", rep.Via)
	if rep.Resources != nil && rep.Resources.MemTotalMB > 0 {
		// A box at 95% RAM will accept your connection and then fail to answer
		// a task in time — which surfaces as a bare client-side timeout and
		// looks exactly like a network fault. Surface load next to reachability
		// so that mistake can't be made.
		usedPct := float64(rep.Resources.MemUsedMB) / float64(rep.Resources.MemTotalMB) * 100
		if usedPct >= 90 || rep.Resources.CPUPercent >= 90 {
			return base + " but SATURATED (CPU " +
					fmt.Sprintf("%.0f%%", rep.Resources.CPUPercent) +
					fmt.Sprintf(", RAM %.0f%%)", usedPct),
				"It will accept connections and still time out on real work. Stop whatever is hogging it before blaming the network."
		}
	}
	switch {
	case len(ready) > 0:
		return base + ". Coding agents ready: " + strings.Join(ready, ", "), ""
	case len(installed) > 0:
		return base + ", but no coding agent is authenticated (installed: " + strings.Join(installed, ", ") + ").",
			"Run `yaver runner auth` on that box — tasks will fail until a runner is signed in."
	}
	return base + ", but no coding agent is installed.", "Install claude / codex / opencode on that box."
}

// fetchRunnerVerdicts + fetchResources reuse the agent's OWN endpoints. Both are
// best-effort: a reachable box with a broken runner endpoint should still report
// its resources, and vice versa. Partial truth beats a blank page.
// fetchRunnerVerdicts reads the agent's REAL field names. The first cut of this
// file invented a field called "authenticated", which the agent has never sent —
// so every runner decoded as authenticated:false and the verdict was right only
// by accident. Mirror the wire contract (runnerInfoRow) exactly; a diagnostic
// that guesses at field names is a diagnostic that lies with confidence.
func fetchRunnerVerdicts(ctx context.Context, baseURL, token string, timeout time.Duration) []runnerVerdict {
	var raw struct {
		Runners []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Installed      bool   `json:"installed"`
			Ready          bool   `json:"ready"`
			AuthConfigured bool   `json:"authConfigured"`
			AuthVerified   bool   `json:"authVerified"`
			AuthSource     string `json:"authSource"`
			Warning        string `json:"warning"`
			Error          string `json:"error"`
		} `json:"runners"`
	}
	if err := getJSONFrom(ctx, baseURL, "/agent/runners", token, timeout, &raw); err != nil {
		return nil
	}
	out := make([]runnerVerdict, 0, len(raw.Runners))
	for _, r := range raw.Runners {
		note := strings.TrimSpace(r.Error)
		if note == "" {
			note = strings.TrimSpace(r.Warning)
		}
		if note == "" && r.AuthConfigured && !r.AuthVerified {
			// Presence of a credentials file is NOT proof of a live session.
			note = "credentials file found but not verified with the runner itself"
		}
		if note == "" && r.AuthSource != "" {
			note = "auth via " + r.AuthSource
		}
		out = append(out, runnerVerdict{
			ID:            r.ID,
			Installed:     r.Installed,
			Authenticated: r.AuthConfigured,
			Ready:         r.Ready,
			Verified:      r.AuthVerified,
			Note:          note,
		})
	}
	return out
}

func fetchResources(ctx context.Context, baseURL, token string, timeout time.Duration) *doctorReso {
	var raw struct {
		Hostname     string  `json:"hostname"`
		Platform     string  `json:"platform"`
		Arch         string  `json:"arch"`
		AgentVersion string  `json:"agentVersion"`
		Version      string  `json:"version"`
		CPUPercent   float64 `json:"cpuPercent"`
		MemTotalMB   int64   `json:"memTotalMB"`
		MemUsedMB    int64   `json:"memUsedMB"`
		DiskFreeGB   int64   `json:"diskFreeGB"`
	}
	if err := getJSONFrom(ctx, baseURL, "/info", token, timeout, &raw); err != nil {
		return nil
	}
	ver := raw.AgentVersion
	if ver == "" {
		ver = raw.Version
	}
	return &doctorReso{
		Hostname:     raw.Hostname,
		Platform:     raw.Platform,
		Arch:         raw.Arch,
		AgentVersion: ver,
		CPUPercent:   raw.CPUPercent,
		MemTotalMB:   raw.MemTotalMB,
		MemUsedMB:    raw.MemUsedMB,
		DiskFreeGB:   raw.DiskFreeGB,
	}
}

func getJSONFrom(ctx context.Context, baseURL, path, token string, timeout time.Duration, out any) error {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := remoteHTTPClient(timeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// localMachineDoctor answers for THIS agent without any network hop at all.
func localMachineDoctor() machineDoctorReport {
	rep := machineDoctorReport{Device: "local", Reachable: true, Via: "local"}
	res := &doctorReso{}
	if pct, err := getCPUPercent(); err == nil {
		res.CPUPercent = pct
	}
	if mb, err := getSystemMemoryMB(); err == nil {
		res.MemTotalMB = mb
	}
	if free := detectDiskFree(); free > 0 {
		res.DiskFreeGB = free / (1024 * 1024 * 1024)
	}
	rep.Resources = res
	rep.Summary = "This machine."
	return rep
}
