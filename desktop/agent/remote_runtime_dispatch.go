package main

// remote_runtime_dispatch.go — Linux→Mac signaling proxy. The
// closer for Phase 5: when a Swift / iOS session is created on a
// non-darwin host and a paired Mac builder is configured, this
// agent forwards every HTTP signaling call to the builder. The
// browser still hits localhost:18080 (or whatever the Linux agent
// listens on) for `/remote-runtime/sessions/<id>/...`; the agent
// transparently re-issues the request against the builder's URL
// and returns the builder's response verbatim.
//
// CRITICAL: this proxy only carries SIGNALING — SDP offer/answer
// JSON, control-channel events, lifecycle calls. The actual RTP
// media (H.264 video + DTLS-SRTP) flows direct viewer↔Mac once ICE
// has negotiated a path. The Linux box never decodes a frame, never
// holds a Pion TrackLocal, and never appears as an ICE candidate.
//
// Privacy: the builder's URL + token live on disk under
// ~/.yaver/builders.json (mode 0600). They never appear in any
// payload returned to the browser, the dashboard, or Convex. The
// session payload only carries the public-safe alias
// (RemoteBuilderId).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// proxiedSession is the dispatch record for one session served by
// a remote builder. localID (the key in m.proxied) is the ID the
// browser sees; RemoteID is the ID the builder issued and that
// every forwarded URL is rewritten to reference.
type proxiedSession struct {
	BuilderAlias string
	BuilderURL   string
	BuilderToken string
	RemoteID     string
}

// proxiedFor returns the dispatch record for sessionID, or nil
// when the session is local-only. Cheap (single map lookup under
// the existing read lock).
func (m *RemoteRuntimeManager) proxiedFor(sessionID string) *proxiedSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.proxied[strings.TrimSpace(sessionID)]
}

// splitSessionRoutePath splits a path like
//
//	rr_proxy_173/webrtc/offer    →  ("rr_proxy_173", "/webrtc/offer")
//	rr_proxy_173/control         →  ("rr_proxy_173", "/control")
//	rr_proxy_173                 →  ("rr_proxy_173", "")
//
// into the session ID and the suffix the proxy needs to append
// after the remote ID. Used by the proxy short-circuit at the top
// of handleRemoteRuntimeSessionRoute.
func splitSessionRoutePath(path string) (sessionID, suffix string) {
	path = strings.Trim(path, "/")
	if path == "" {
		return "", ""
	}
	for _, s := range []string{"/webrtc/offer", "/command", "/control", "/frame"} {
		if strings.HasSuffix(path, s) {
			return strings.TrimSuffix(path, s), s
		}
	}
	return path, ""
}

// hostClassForDispatch is a package-level var (not a plain call to
// detectRuntimeHostClass) so tests can simulate "running on Linux"
// without having to ship a Linux test runner. Production keeps the
// real detector.
var hostClassForDispatch = detectRuntimeHostClass

// pickBuilderForFramework returns the builder that should serve
// (framework, targetID) on the current host, or nil + a reason
// string suitable for surfacing in a session note. Today the rule
// is: Swift / iOS-simulator on a non-darwin host dispatches to the
// default iOS builder. Everything else stays local.
//
// Future expansion (Flutter on Linux for iOS, kotlin on macOS
// dispatched to a Linux builder for parallel Android-emulator
// fan-out, etc.) plugs into this single function.
func pickBuilderForFramework(framework, targetID string) (*BuilderEntry, string) {
	framework = strings.ToLower(strings.TrimSpace(framework))
	targetID = strings.ToLower(strings.TrimSpace(targetID))
	needsIOS := false
	switch {
	case framework == "swift":
		needsIOS = true
	case framework == "flutter" && (targetID == "ios-simulator" || targetID == "ios-simulator-remote"):
		needsIOS = true
	}
	if !needsIOS {
		return nil, ""
	}
	if hostClassForDispatch() == "macos-ios" {
		// Local Mac can serve iOS itself — no need to round-trip.
		return nil, ""
	}
	reg, err := LoadBuilders()
	if err != nil || reg == nil || len(reg.Builders) == 0 {
		return nil, "no Mac builder paired (run `yaver builder add <alias> <url>` on a Mac running `yaver serve --builder-platforms=ios`)"
	}
	alias := reg.Default
	if alias == "" {
		return nil, "paired builders exist but no default is set (`yaver builder use <alias>`)"
	}
	entry, ok := reg.Builders[alias]
	if !ok {
		return nil, fmt.Sprintf("default builder %q not in registry — re-pair or pick another", alias)
	}
	if !platformsContain(entry.Platforms, "ios") {
		return nil, fmt.Sprintf("default builder %q does not advertise ios in its platform list", alias)
	}
	return &entry, ""
}

// dispatchCreateToBuilder forwards a session-create call to the
// builder and stores the proxy mapping. Returns a local view of the
// session whose ID is freshly minted (so a viewer can't accidentally
// double-track the same session through both ends) and whose
// RemoteBuilderId is the alias.
func (m *RemoteRuntimeManager) dispatchCreateToBuilder(entry BuilderEntry, workDir, framework, targetID, transportMode string) (RemoteRuntimeSession, error) {
	body, err := json.Marshal(map[string]string{
		"workDir":       workDir,
		"framework":     framework,
		"targetId":      targetID,
		"transportMode": transportMode,
	})
	if err != nil {
		return RemoteRuntimeSession{}, fmt.Errorf("marshal create body: %w", err)
	}
	target := strings.TrimRight(entry.URL, "/") + "/remote-runtime/sessions"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return RemoteRuntimeSession{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if entry.Token != "" {
		req.Header.Set("Authorization", "Bearer "+entry.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return RemoteRuntimeSession{}, fmt.Errorf("dispatch to builder %q: %w", entry.Alias, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return RemoteRuntimeSession{}, fmt.Errorf("builder %q returned %s: %s", entry.Alias, resp.Status, strings.TrimSpace(string(raw)))
	}
	var remote RemoteRuntimeSession
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return RemoteRuntimeSession{}, fmt.Errorf("parse builder response: %w", err)
	}

	// Mint a fresh local ID. Two reasons: (1) we want every
	// `/remote-runtime/sessions/<id>` lookup to land in m.proxied
	// before reaching local manager state, which means using a
	// distinct prefix; (2) it keeps a viewer from accidentally
	// tracking a builder's session ID and a local stub of it as
	// two separate sessions.
	localID := fmt.Sprintf("rr_proxy_%d", time.Now().UTC().UnixNano())
	local := remote
	local.ID = localID
	local.RemoteBuilderId = entry.Alias
	if local.Note == "" {
		local.Note = fmt.Sprintf("Dispatched to builder %s (remote session %s).", entry.Alias, remote.ID)
	} else {
		local.Note = fmt.Sprintf("Dispatched to builder %s. %s", entry.Alias, local.Note)
	}

	m.mu.Lock()
	m.sessions[localID] = local
	m.proxied[localID] = &proxiedSession{
		BuilderAlias: entry.Alias,
		BuilderURL:   entry.URL,
		BuilderToken: entry.Token,
		RemoteID:     remote.ID,
	}
	// We deliberately do NOT register a remoteRuntimeLiveState for
	// proxied sessions — the local manager doesn't run a Pion peer
	// connection or a JPEG pump for them. Forwarded HTTP handlers
	// short-circuit before consulting m.live.
	m.mu.Unlock()
	return local, nil
}

// forwardSessionRequest proxies an HTTP request that targets a
// session ID. Method, body, and a small safe-list of headers are
// copied to the builder; the response body, status code, and
// response headers in returnedHeaders are streamed back to the
// caller verbatim. Used by every session-scoped handler:
// /remote-runtime/sessions/<id>, /webrtc/offer, /control,
// /command, /frame, DELETE.
//
// suffix is the path remainder to append AFTER the remote session
// ID, e.g. "/webrtc/offer", "/control", "" (for the bare GET +
// DELETE on the session ID).
func forwardSessionRequest(w http.ResponseWriter, r *http.Request, p *proxiedSession, suffix string) {
	target := strings.TrimRight(p.BuilderURL, "/") + "/remote-runtime/sessions/" + p.RemoteID + suffix

	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusBadGateway, fmt.Sprintf("read upstream body: %v", err))
		return
	}
	_ = r.Body.Close()

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	// Forward only the minimum set of request headers the builder
	// needs. We deliberately do NOT pass the user's Authorization
	// from the inbound request — the builder has its own auth
	// (the token we stored at pairing time). This keeps cross-host
	// auth boundaries explicit instead of relying on bearer-token
	// reuse.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	if p.BuilderToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.BuilderToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		jsonError(w, http.StatusBadGateway, fmt.Sprintf("dispatch to builder %s: %v", p.BuilderAlias, err))
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{
		"Content-Type", "Cache-Control",
		"X-Yaver-Remote-Session", "X-Yaver-Remote-Transport",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
