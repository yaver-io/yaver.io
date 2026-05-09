package main

// heartbeat_watcher.go — peer heartbeat monitoring for the
// "my Mac mini upstairs stopped reporting" alert. Solves the
// scary-quiet scenario where a headless box goes offline and
// the dev has no idea until they try to SSH in.
//
// How it works:
//
//   1. Every mobile/desktop/headless yaver agent already pings
//      Convex every 2 min to update `lastHeartbeat` on its
//      device record.
//   2. The local agent remembers the set of devices it's
//      previously connected to / discovered over the relay and
//      fetches their lastHeartbeat from the peer list.
//   3. Every minute, the watcher compares each device's
//      lastHeartbeat against a staleness threshold. When it
//      crosses the threshold (default 5 minutes) we emit a
//      one-shot notification + surface it on /machine/peers.
//   4. When the device starts reporting again, the watcher
//      clears the alert state so a flapping connection doesn't
//      spam the notification channel.
//
// No Convex mutation. We only READ device records (peer
// discovery is the one thing Convex is allowed to do) — the
// alert state itself lives in ~/.yaver/peers/state.json.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PeerState is one tracked peer's most recent observation.
type PeerState struct {
	DeviceID            string `json:"deviceId"`
	Name                string `json:"name,omitempty"`
	LastSeen            string `json:"lastSeen"`       // from Convex device.lastHeartbeat
	ObservedAt          string `json:"observedAt"`     // when we last saw the Convex record
	State               string `json:"state"`          // "online" | "stale" | "offline"
	AlertedAt           string `json:"alertedAt,omitempty"`
	StaleSince          string `json:"staleSince,omitempty"`
	LastRecoveryAt      string `json:"lastRecoveryAt,omitempty"`
	LastRecoveryOutcome string `json:"lastRecoveryOutcome,omitempty"`
}

// peerWatcher is the singleton that runs the loop.
type peerWatcher struct {
	mu     sync.Mutex
	states map[string]*PeerState
	path   string
}

var (
	peerWatcherOnce sync.Once
	peerWatcherInst *peerWatcher
)

// PeerStalenessThreshold is how long without a heartbeat we
// consider a peer "missing." Bus pings run every 60s; 5 minutes
// = four missed intervals — a comfortable margin for non-primary
// peers where false positives matter more than recovery latency.
const PeerStalenessThreshold = 5 * time.Minute

// PeerStalenessPrimary is the tighter threshold applied to the
// user's primary device, whichever device that happens to be.
// Primary going dark is the loudest possible signal, worth
// acting on at 90s instead of waiting the general 5-min interval.
const PeerStalenessPrimary = 90 * time.Second

// busQuietGuard is how long we tolerate "no bus traffic from anyone"
// before we suppress staleness alerts that round. If our own bus
// transport is dark, every peer looks stale simultaneously — that's
// our problem, not theirs. 90s is two missed peer pings on the
// general 60s cadence.
const busQuietGuard = 90 * time.Second

// globalPeerWatcher returns the singleton, lazily constructed.
func globalPeerWatcher() *peerWatcher {
	peerWatcherOnce.Do(func() {
		base, err := ConfigDir()
		if err != nil {
			peerWatcherInst = &peerWatcher{states: map[string]*PeerState{}}
			return
		}
		dir := filepath.Join(base, "peers")
		_ = os.MkdirAll(dir, 0700)
		pw := &peerWatcher{
			states: map[string]*PeerState{},
			path:   filepath.Join(dir, "state.json"),
		}
		_ = pw.loadLocked()
		peerWatcherInst = pw
	})
	return peerWatcherInst
}

func (w *peerWatcher) loadLocked() error {
	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		States map[string]*PeerState `json:"states"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.States != nil {
		w.states = payload.States
	}
	return nil
}

func (w *peerWatcher) saveLocked() error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"states":    w.states,
		"updatedAt": time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Snapshot returns a copy of the current peer state table for
// the HTTP / CLI surfaces.
func (w *peerWatcher) Snapshot() []*PeerState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*PeerState, 0, len(w.states))
	for _, s := range w.states {
		cp := *s
		out = append(out, &cp)
	}
	return out
}

// observe ingests a Convex device record snapshot and
// updates the in-memory state. Edge-triggered alert logic
// fires exactly once per online→offline transition.
//
// `isPrimary` tightens the staleness threshold for the user's
// primary device — primary going dark is the highest-priority
// signal, worth a 90s threshold instead of 5 min.
// Returns alertMsg (non-empty on any state transition) and
// transitionedToOffline (true on the online→offline edge, used
// by callers to fire a recovery actuator).
func (w *peerWatcher) observe(deviceID, name, lastSeen string, isPrimary bool) (alertMsg string, transitionedToOffline bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now().UTC()
	state := w.states[deviceID]
	if state == nil {
		state = &PeerState{DeviceID: deviceID, Name: name}
		w.states[deviceID] = state
	}
	state.Name = name
	state.LastSeen = lastSeen
	state.ObservedAt = now.Format(time.RFC3339)

	// Is the heartbeat stale?
	var hb time.Time
	if lastSeen != "" {
		if t, err := time.Parse(time.RFC3339, lastSeen); err == nil {
			hb = t
		}
	}
	threshold := PeerStalenessThreshold
	if isPrimary {
		threshold = PeerStalenessPrimary
	}
	stale := hb.IsZero() || now.Sub(hb) > threshold

	prevState := state.State
	if stale {
		if prevState != "offline" {
			state.State = "offline"
			if state.StaleSince == "" {
				state.StaleSince = now.Format(time.RFC3339)
			}
			transitionedToOffline = true
			// Alert once per transition. We re-arm the
			// alert on recovery so a flapping peer still
			// only pings us when it flips.
			if state.AlertedAt == "" || state.AlertedAt != state.StaleSince {
				state.AlertedAt = state.StaleSince
				alertMsg = fmt.Sprintf("peer %s (%s) has stopped responding (last seen %s)",
					nonEmpty(name, deviceID), deviceID, lastSeen)
			}
		}
	} else {
		if prevState == "offline" {
			alertMsg = fmt.Sprintf("peer %s recovered (heartbeat %s)", nonEmpty(name, deviceID), lastSeen)
		}
		state.State = "online"
		state.StaleSince = ""
		state.AlertedAt = ""
	}
	_ = w.saveLocked()
	return alertMsg, transitionedToOffline
}

// recordRecoveryAttempt persists the outcome of an SSH-recovery
// attempt against a peer so /peers/health can surface "we tried,
// here's what happened" instead of leaving the user guessing.
func (w *peerWatcher) recordRecoveryAttempt(deviceID, outcome string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	state := w.states[deviceID]
	if state == nil {
		state = &PeerState{DeviceID: deviceID}
		w.states[deviceID] = state
	}
	state.LastRecoveryAt = time.Now().UTC().Format(time.RFC3339)
	state.LastRecoveryOutcome = outcome
	_ = w.saveLocked()
}

// nonEmpty picks the first non-empty string. Tiny helper so
// the alert copy reads naturally.
func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- loop ----------------------------------------------------------------

// StartHeartbeatWatcher starts the background loop. Called
// from runServe. Idempotent — duplicate calls are harmless.
func StartHeartbeatWatcher(ctx context.Context) {
	// Small delay so other boot goroutines don't race.
	go func() {
		time.Sleep(5 * time.Second)
		SupervisedGo("peer-heartbeat-watch", time.Minute, false,
			func(ctx context.Context) error {
				pollPeerHeartbeats()
				return nil
			})
	}()
}

// pollPeerHeartbeats drains the local LeaderTracker cache — which
// is itself populated by the P2P bus (relay / direct / future LAN
// multicast) — and feeds each known peer through the watcher. Used
// to cost a Convex GET /devices call per tick (60/hour/user); now
// costs zero. Convex is only hit at cold-boot bootstrap when the
// bus cache is empty (first 5 min after `yaver serve` starts).
//
// The Convex fallback is deliberate: when an agent first boots, it
// hasn't yet heard anyone's `peer/*/online` event, so it needs some
// way to populate the list. After the bus converges (~1 min), this
// falls back path is a no-op.
func pollPeerHeartbeats() {
	hostname, _ := os.Hostname()
	watcher := globalPeerWatcher()

	// Primary path: bus cache via the process-global LeaderTracker.
	// Zero Convex calls in the steady state.
	if lt := globalLeader; lt != nil {
		peers := lt.Peers()
		if len(peers) > 0 {
			cfg, _ := LoadConfig()
			selfID := ""
			if cfg != nil {
				selfID = cfg.DeviceID
			}

			// Bus-down guard. If our own bus transport saw
			// no traffic from anyone in `busQuietGuard`,
			// every peer would look stale at once — that's
			// us, not them. Skip alerts/recovery this round.
			now := time.Now()
			busQuiet := true
			for _, p := range peers {
				if p.DeviceID == selfID {
					continue
				}
				if now.Sub(time.UnixMilli(p.LastSeenAt)) <= busQuietGuard {
					busQuiet = false
					break
				}
			}
			if busQuiet {
				return
			}

			primaryID := lookupPrimaryDeviceID()
			watchdogEnabled := os.Getenv("YAVER_PEER_WATCHDOG") == "1"
			for _, p := range peers {
				if p.DeviceID == selfID {
					continue
				}
				lastHB := time.UnixMilli(p.LastSeenAt).UTC().Format(time.RFC3339)
				isPrimary := p.DeviceID == primaryID && primaryID != ""
				msg, fellOffline := watcher.observe(p.DeviceID, p.Hostname, lastHB, isPrimary)
				if msg != "" {
					fmt.Fprintf(os.Stderr, "[heartbeat] %s\n", msg)
					if nm := globalMonitorNotifier; nm != nil {
						nm("peer-heartbeat", hostname, msg, 0)
					}
				}
				// Auto-recovery: any owned peer that just fell
				// offline, when the user has opted in via env
				// var. Symmetric — every box configured as a
				// watchdog can heal any other owned box, no
				// hardcoded pairings. The recovery script is
				// idempotent, so simultaneous attempts from
				// multiple watchdogs are safe (last writer
				// wins on the systemctl/launchctl restart).
				// Runs in a goroutine so the poll loop doesn't
				// block on SSH timeouts.
				if fellOffline && watchdogEnabled {
					go attemptPeerRecovery(p.DeviceID, p.Hostname)
				}
			}
			return
		}
	}

	// Cold-boot fallback: bus hasn't converged yet. Ask Convex for
	// the registry exactly once. Runs maybe once or twice after
	// `yaver serve` starts, then never again for the life of the
	// process.
	convexBootstrapPeers(hostname, watcher)
}

// lookupPrimaryDeviceID returns the user's primary device ID, or
// empty string if it's unset / Convex is unreachable. Best-effort:
// the watcher will fall back to the loose threshold when in doubt.
func lookupPrimaryDeviceID() string {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := primaryGetCurrent(ctx, cfg.AuthToken, cfg.ConvexSiteURL)
	if err != nil {
		return ""
	}
	return id
}

// attemptPeerRecovery SSHes into a peer that just fell offline and
// tries idempotent service-restart commands. Non-interactive — auth
// recovery (which needs an OTP from the user) is intentionally NOT
// attempted here; that's a separate manual flow.
//
// Returns a one-line outcome string for /peers/health surfacing.
func attemptPeerRecovery(deviceID, hostname string) string {
	target := resolvePeerSSHTarget(deviceID)
	if target == "" {
		outcome := "skipped: no ssh target"
		globalPeerWatcher().recordRecoveryAttempt(deviceID, outcome)
		return outcome
	}

	// Try systemd (user unit, then system unit if passwordless sudo
	// is configured) then launchd (macOS) then a process-restart
	// fallback. Each path is wrapped in a subshell so a missing
	// command (e.g. systemctl on macOS, launchctl on Linux) just
	// falls through. POSIX sh — Alpine / BusyBox safe. The unit
	// name `yaver` and launchd label `io.yaver.agent` match what
	// `yaver serve` installs (see binary_paths.go and
	// machine_remove.go for the canonical names).
	script := strings.TrimSpace(`
( command -v systemctl >/dev/null 2>&1 && systemctl --user restart yaver 2>/dev/null && echo recovered:systemd-user ) ||
( command -v sudo >/dev/null 2>&1 && command -v systemctl >/dev/null 2>&1 && sudo -n systemctl restart yaver 2>/dev/null && echo recovered:systemd-system ) ||
( command -v launchctl >/dev/null 2>&1 && launchctl kickstart -k "gui/$(id -u)/io.yaver.agent" 2>/dev/null && echo recovered:launchd ) ||
( command -v yaver >/dev/null 2>&1 && (pkill -x yaver 2>/dev/null; sleep 1; nohup yaver serve >/dev/null 2>&1 & echo recovered:nohup) )
`)

	yaverPath := findYaverBinary()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := osexec.CommandContext(ctx, yaverPath, "ssh", target, "--", "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	var outcome string
	switch {
	case err != nil:
		outcome = fmt.Sprintf("failed: %s", strings.TrimSpace(err.Error()))
		if outStr != "" {
			outcome += " (" + truncateForLog(outStr, 200) + ")"
		}
	case strings.Contains(outStr, "recovered:"):
		outcome = "ok: " + extractRecoveryTag(outStr)
	default:
		outcome = "no-op (no service manager matched)"
	}

	globalPeerWatcher().recordRecoveryAttempt(deviceID, outcome)
	hostLog, _ := os.Hostname()
	fmt.Fprintf(os.Stderr, "[heartbeat] recovery %s → %s: %s\n", nonEmpty(hostname, deviceID), target, outcome)
	if nm := globalMonitorNotifier; nm != nil {
		nm("peer-recovery", hostLog, fmt.Sprintf("recovery on %s: %s", nonEmpty(hostname, deviceID), outcome), 0)
	}
	return outcome
}

// resolvePeerSSHTarget picks the friendliest handle to hand `yaver
// ssh`: alias if set (so users with `Host test 1.2.3.4` in their
// ssh config get a hit), then registered name, else empty.
func resolvePeerSSHTarget(deviceID string) string {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return ""
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return ""
	}
	for _, d := range devices {
		if d.DeviceID == deviceID {
			if alias := strings.TrimSpace(d.Alias); alias != "" {
				return alias
			}
			if name := strings.TrimSpace(d.Name); name != "" {
				return name
			}
			return ""
		}
	}
	return ""
}

func extractRecoveryTag(out string) string {
	idx := strings.Index(out, "recovered:")
	if idx < 0 {
		return out
	}
	tail := out[idx+len("recovered:"):]
	if nl := strings.IndexAny(tail, " \n\r\t"); nl >= 0 {
		tail = tail[:nl]
	}
	return tail
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// convexBootstrapPeers is the legacy Convex-polling path, kept
// behind a bus-empty gate so a fresh agent still has a peer list
// within seconds of booting (before the first `peer/*/ping` arrives
// over the bus). Inner contents identical to the pre-bus implementation.
func convexBootstrapPeers(hostname string, watcher *peerWatcher) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return
	}
	req, err := newBearerRequest("GET", cfg.ConvexSiteURL+"/devices", cfg.AuthToken, nil)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var payload struct {
		Devices []struct {
			DeviceID      string `json:"deviceId"`
			Name          string `json:"name"`
			LastHeartbeat string `json:"lastHeartbeat"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	primaryID := lookupPrimaryDeviceID()
	for _, d := range payload.Devices {
		if d.DeviceID == cfg.DeviceID {
			continue
		}
		isPrimary := d.DeviceID == primaryID && primaryID != ""
		msg, _ := watcher.observe(d.DeviceID, d.Name, d.LastHeartbeat, isPrimary)
		if msg != "" {
			fmt.Fprintf(os.Stderr, "[heartbeat] %s\n", msg)
			if nm := globalMonitorNotifier; nm != nil {
				nm("peer-heartbeat", hostname, msg, 0)
			}
		}
	}
}

// --- HTTP ----------------------------------------------------------------

// handlePeerHealth serves the peer state table for the mobile
// Monitor > Machines sub-tab (future tab) and for `yaver
// machine peers`. Includes watchdog-enabled flag so callers can
// distinguish "no recovery yet because watchdog is off" from
// "watchdog ran and failed."
func (s *HTTPServer) handlePeerHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":              true,
		"peers":           globalPeerWatcher().Snapshot(),
		"watchdogEnabled": os.Getenv("YAVER_PEER_WATCHDOG") == "1",
	})
}

// handlePeerRecover triggers an SSH recovery attempt against a
// peer on demand. The caller is expected to be authenticated as
// an owner; the body specifies the deviceId to recover. Any of
// the user's devices can be the watchdog — picked at runtime by
// the mobile UI based on which device the user is currently
// connected to.
//
// Owner-token auth via s.auth wrapper. The recovery body is
// idempotent (systemctl restart / launchctl kickstart / nohup),
// so retries are safe.
func (s *HTTPServer) handlePeerRecover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	if body.DeviceID == "" {
		jsonError(w, http.StatusBadRequest, "deviceId required")
		return
	}
	// Look up the friendly hostname for the response. Best-effort.
	hostname := ""
	for _, p := range globalPeerWatcher().Snapshot() {
		if p.DeviceID == body.DeviceID {
			hostname = p.Name
			break
		}
	}
	outcome := attemptPeerRecovery(body.DeviceID, hostname)
	status := http.StatusOK
	if strings.HasPrefix(outcome, "failed") || strings.HasPrefix(outcome, "skipped") {
		status = http.StatusBadGateway
	}
	jsonReply(w, status, map[string]interface{}{
		"ok":       status == http.StatusOK,
		"deviceId": body.DeviceID,
		"outcome":  outcome,
	})
}
