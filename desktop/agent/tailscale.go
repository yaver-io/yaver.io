package main

// tailscale.go — detect a locally-running tailscaled, pull the
// node's Tailscale IPs, and surface them as preferred relay
// candidates. Solo-dev benefit: when the Mac mini and the
// laptop are both on the dev's Tailnet, connections skip the
// public relay entirely and just use the Tailscale overlay.
//
// Everything is read-only — we never run `tailscale up` or
// touch the Tailscale config. We just ask the local daemon
// what its current state is, and if it answers, the reported
// IPs get used.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// TailscaleStatus is the trimmed `tailscale status --json`
// view we actually use.
type TailscaleStatus struct {
	Running     bool     `json:"running"`
	BackendState string  `json:"backendState,omitempty"`
	Self        *tailscaleSelf `json:"self,omitempty"`
}

type tailscaleSelf struct {
	HostName string   `json:"hostName"`
	TailAddr string   `json:"tailAddr"`
	Tags     []string `json:"tags,omitempty"`
	Addrs    []string `json:"addrs,omitempty"`
}

// DetectTailscale runs `tailscale status --json` if the
// binary exists and returns a trimmed snapshot. Fast-fails
// when tailscaled isn't running so the function is safe to
// call on every startup without hanging.
func DetectTailscale() *TailscaleStatus {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return &TailscaleStatus{Running: false}
	}
	cmd := exec.Command(bin, "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return &TailscaleStatus{Running: false}
	}
	var payload struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Tags         []string `json:"Tags,omitempty"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return &TailscaleStatus{Running: false}
	}
	status := &TailscaleStatus{
		Running:      payload.BackendState == "Running",
		BackendState: payload.BackendState,
	}
	if len(payload.Self.TailscaleIPs) > 0 {
		self := &tailscaleSelf{
			HostName: payload.Self.HostName,
			TailAddr: payload.Self.TailscaleIPs[0],
			Tags:     payload.Self.Tags,
			Addrs:    payload.Self.TailscaleIPs,
		}
		status.Self = self
	}
	return status
}

// tailscaleIPCandidates returns every Tailscale IP the local
// node has, suitable for embedding in the pairing QR as a
// preferred relay URL (laptop and Mac mini on the same Tailnet
// can reach each other directly without going through the
// yaver relay at all).
func tailscaleIPCandidates(httpPort int) []string {
	st := DetectTailscale()
	if st == nil || !st.Running || st.Self == nil {
		return nil
	}
	out := make([]string, 0, len(st.Self.Addrs))
	for _, addr := range st.Self.Addrs {
		// IPv6 Tailscale addresses need square-bracketing.
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			out = append(out, fmt.Sprintf("http://%s:%d", addr, httpPort))
		} else {
			out = append(out, fmt.Sprintf("http://[%s]:%d", addr, httpPort))
		}
	}
	return out
}

// --- HTTP ---------------------------------------------------------------

// handleTailscaleStatus is a read-only endpoint the mobile app
// + `yaver doctor` + the pairing QR generator all call. We
// cache for 10s so a noisy client can't peg `tailscale status`.
var (
	tailscaleCache     *TailscaleStatus
	tailscaleCacheAt   time.Time
	tailscaleCacheTTL  = 10 * time.Second
)

func (s *HTTPServer) handleTailscaleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if tailscaleCache == nil || time.Since(tailscaleCacheAt) > tailscaleCacheTTL {
		tailscaleCache = DetectTailscale()
		tailscaleCacheAt = time.Now()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"status": tailscaleCache,
	})
}

// fold Tailscale IPs into the candidate set the pairing QR
// advertises. Hook into candidatePairingURLs via a local
// wrapper so tunnel_forward.go and auth_pair.go share the
// same logic.
func augmentCandidatesWithTailscale(urls []string, httpPort int) []string {
	ts := tailscaleIPCandidates(httpPort)
	if len(ts) == 0 {
		return urls
	}
	// Tailscale IPs are cheaper + more reliable than the
	// public relay, so we prepend them.
	out := make([]string, 0, len(ts)+len(urls))
	out = append(out, ts...)
	for _, u := range urls {
		if !strings.Contains(u, "tailscale") { // avoid dup
			out = append(out, u)
		}
	}
	return out
}
