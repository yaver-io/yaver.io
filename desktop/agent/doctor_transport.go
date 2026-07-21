package main

// doctor_transport.go — GET /doctor/transport: a live transport self-diagnosis.
//
// This is the endpoint the out-of-band SSH channel's `doctor-transport` verb hits
// (ssh_session_cmd.go whitelist) so the agentic self-heal can, over the SURVIVING
// channel, learn WHY the primary data path is down and act on it. Per CLAUDE.md
// "probe the real capability, never the proxy": it does not just report that a
// relay is configured — it actually PROBES each relay's HTTP endpoint (bounded)
// and reports reachable/latency, so "the inventory says yes, the operation says
// no" cannot hide. Metadata only (never task data), so it is safe on the channel.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type transportRelayProbe struct {
	ID        string `json:"id"`
	HttpURL   string `json:"httpUrl,omitempty"`
	QuicAddr  string `json:"quicAddr,omitempty"`
	Region    string `json:"region,omitempty"`
	Reachable bool   `json:"reachable"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

type transportDoctorReport struct {
	DeviceID             string                `json:"deviceId"`
	RelayOnly            bool                  `json:"relayOnly"`
	RelayPasswordPresent bool                  `json:"relayPasswordPresent"`
	RelayCount           int                   `json:"relayCount"`
	AnyRelayReachable    bool                  `json:"anyRelayReachable"`
	Relays               []transportRelayProbe `json:"relays"`
	// Remedy names the specific next action the self-heal should take, not a
	// vague "check config" — carrying the WHY into the text (CLAUDE.md).
	Remedy string `json:"remedy,omitempty"`
}

// probeTransport builds the report. Exposed (ctx-taking) so it is unit-testable
// with a fake relay HTTP server on a random port — no mocks, real probe.
func probeTransport(ctx context.Context, deviceID string, relayOnly bool, relayPassword string, relays []RelayServerConfig, probe func(ctx context.Context, url string) (bool, int64, string)) transportDoctorReport {
	rep := transportDoctorReport{
		DeviceID:             deviceID,
		RelayOnly:            relayOnly,
		RelayPasswordPresent: relayPassword != "",
		RelayCount:           len(relays),
	}
	for _, r := range relays {
		p := transportRelayProbe{ID: r.ID, HttpURL: r.HttpURL, QuicAddr: r.QuicAddr, Region: r.Region}
		if r.HttpURL != "" {
			ok, ms, errStr := probe(ctx, r.HttpURL)
			p.Reachable, p.LatencyMs, p.Error = ok, ms, errStr
			if ok {
				rep.AnyRelayReachable = true
			}
		}
		rep.Relays = append(rep.Relays, p)
	}
	// Specific remedy, so the agentic self-heal knows exactly what to do next.
	switch {
	case rep.RelayCount == 0:
		rep.Remedy = "no relays configured — sign in again to fetch the relay catalog"
	case !rep.RelayPasswordPresent:
		rep.Remedy = "relay password missing — repair-relay (re-pull creds) then re-register the tunnel"
	case !rep.AnyRelayReachable:
		rep.Remedy = "no relay reachable from this box — relay may be down/overloaded; try another relay or wait+re-register"
	default:
		rep.Remedy = "relay reachable — if the phone still can't connect, the tunnel registration is stale: re-register (evict + redial)"
	}
	return rep
}

// httpRelayProbe is the real bounded HTTP reachability probe.
func httpRelayProbe(ctx context.Context, url string) (bool, int64, string) {
	pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
	if err != nil {
		return false, 0, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return false, ms, err.Error()
	}
	resp.Body.Close()
	// Any HTTP answer (even 401/404) means the relay is REACHABLE — the leg is
	// alive; auth is a separate axis the remedy handles.
	return true, ms, ""
}

func (s *HTTPServer) handleDoctorTransport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		jsonError(w, http.StatusInternalServerError, "cannot load config")
		return
	}
	rep := probeTransport(r.Context(), s.deviceID, s.relayOnly, cfg.RelayPassword, cfg.CachedRelayServers, httpRelayProbe)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rep)
}
