package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"
)

func waitForRecoverySession(fetch func() (map[string]interface{}, error), timeoutSeconds, pollIntervalSeconds int) map[string]interface{} {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	if timeoutSeconds > 300 {
		timeoutSeconds = 300
	}
	if pollIntervalSeconds <= 0 {
		pollIntervalSeconds = 3
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	var last map[string]interface{}
	for {
		out, err := fetch()
		if err != nil {
			last = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			last = out
			state := strings.TrimSpace(anyString(out["state"]))
			switch state {
			case "recovered":
				out["ready"] = true
				return out
			case "failed", "expired", "cancelled":
				out["ok"] = false
				out["ready"] = false
				if strings.TrimSpace(anyString(out["error"])) == "" {
					out["error"] = "recovery session did not complete successfully"
				}
				return out
			}
		}
		if time.Now().After(deadline) {
			if last == nil {
				last = map[string]interface{}{}
			}
			last["ok"] = false
			last["timedOut"] = true
			last["ready"] = false
			if strings.TrimSpace(anyString(last["error"])) == "" {
				last["error"] = "timed out waiting for recovery session"
			}
			return last
		}
		time.Sleep(time.Duration(pollIntervalSeconds) * time.Second)
	}
}

func fetchRecoverySessionTarget(targetURL, recoveryID, waitToken, relayPassword string) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, normalizeRecoveryTargetURL(targetURL)+"/auth/recover/session?id="+urlQueryEscape(strings.TrimSpace(recoveryID))+"&wait_token="+urlQueryEscape(strings.TrimSpace(waitToken)), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range targetRecoveryHeaders(relayPassword, "") {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := remoteHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]interface{}{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", extractRemoteError(resp.StatusCode, raw))
	}
	return out, nil
}

type deviceReauthProbe struct {
	State       string                 `json:"state"`
	Reachable   bool                   `json:"reachable"`
	Bootstrap   bool                   `json:"bootstrap"`
	AuthExpired bool                   `json:"authExpired"`
	Transport   string                 `json:"transport,omitempty"`
	BaseURL     string                 `json:"baseUrl,omitempty"`
	HTTPStatus  int                    `json:"httpStatus,omitempty"`
	Info        map[string]interface{} `json:"info,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

func findOwnedDeviceForHint(deviceHint string) (*Config, *DeviceInfo, error) {
	deviceHint = normalizeDeviceHint(deviceHint)
	if deviceHint == "" {
		return nil, nil, fmt.Errorf("device_id is required")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, nil, fmt.Errorf("not signed in — run yaver auth first")
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, nil, fmt.Errorf("list devices: %w", err)
	}
	for i := range devices {
		d := &devices[i]
		if strings.HasPrefix(d.DeviceID, deviceHint) ||
			strings.EqualFold(d.Name, deviceHint) ||
			strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(deviceHint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.EqualFold(d.Alias, deviceHint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.HasPrefix(strings.ToLower(d.Alias), strings.ToLower(deviceHint))) {
			return cfg, d, nil
		}
	}
	return nil, nil, fmt.Errorf("device %q not found", deviceHint)
}

func parseDeviceReauthInfo(info map[string]interface{}) (state string, bootstrap bool, authExpired bool) {
	lifecycleState := strings.ToLower(strings.TrimSpace(anyString(info["lifecycleState"])))
	if lifecycle, ok := info["lifecycle"].(map[string]interface{}); ok {
		if stateVal := strings.ToLower(strings.TrimSpace(anyString(lifecycle["state"]))); stateVal != "" {
			lifecycleState = stateVal
		}
	}
	mode := strings.ToLower(strings.TrimSpace(anyString(info["mode"])))
	needsAuth, _ := info["needsAuth"].(bool)
	authExpired, _ = info["authExpired"].(bool)
	bootstrap = lifecycleState == "bootstrap" || mode == "bootstrap" || needsAuth
	if bootstrap {
		return "bootstrap", true, false
	}
	if lifecycleState == "yaver-auth-expired" || authExpired {
		return "yaver-auth-expired", false, true
	}
	if lifecycleState == "ready-to-connect" {
		return "ready-to-connect", false, false
	}
	return "healthy", false, false
}

func anyString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(v)
	}
}

func probeOwnedDeviceReauth(cfg *Config, target *DeviceInfo) deviceReauthProbe {
	if cfg == nil || target == nil {
		return deviceReauthProbe{State: "offline", Error: "missing device context"}
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil || len(candidates) == 0 {
		state := "offline"
		if target.IsOnline {
			state = "unreachable"
		}
		msg := "device has no reachable transport candidates"
		if err != nil {
			msg = err.Error()
		}
		return deviceReauthProbe{State: state, Error: msg}
	}
	chosen, status, raw, reqErr := doRemoteAgentRequest(context.Background(), candidates, cfg.AuthToken, http.MethodGet, "/info", nil, 8*time.Second)
	if reqErr != nil {
		state := "offline"
		if target.IsOnline {
			state = "unreachable"
		}
		return deviceReauthProbe{State: state, Error: reqErr.Error()}
	}
	probe := deviceReauthProbe{
		Reachable:  true,
		Transport:  firstNonEmpty(strings.TrimSpace(chosen.Label), chosen.Kind),
		BaseURL:    chosen.BaseURL,
		HTTPStatus: status,
	}
	if len(raw) == 0 {
		probe.State = "healthy"
		return probe
	}
	var info map[string]interface{}
	if err := json.Unmarshal(raw, &info); err != nil {
		probe.State = "healthy"
		return probe
	}
	probe.Info = info
	probe.State, probe.Bootstrap, probe.AuthExpired = parseDeviceReauthInfo(info)
	return probe
}

func recommendedReauthMode(state string) string {
	switch state {
	case "bootstrap":
		return "pair"
	case "yaver-auth-expired":
		return "direct"
	case "healthy":
		return "none"
	case "ready-to-connect":
		return "direct"
	case "offline":
		return "wait-for-device"
	default:
		return "check-transport"
	}
}

func normalizeRecoveryTargetURL(targetURL string) string {
	return strings.TrimRight(strings.TrimSpace(targetURL), "/")
}

func targetRecoveryHeaders(relayPassword, bearerToken string) map[string]string {
	headers := map[string]string{}
	if v := strings.TrimSpace(relayPassword); v != "" {
		headers["X-Relay-Password"] = v
	}
	if v := strings.TrimSpace(bearerToken); v != "" {
		headers["Authorization"] = "Bearer " + v
	}
	return headers
}

func isPrivateRecoveryTarget(targetURL string) (bool, string) {
	u, err := urlpkg.Parse(normalizeRecoveryTargetURL(targetURL))
	if err != nil || u == nil {
		return false, "invalid target_url"
	}
	host := strings.TrimSpace(u.Hostname())
	switch {
	case strings.EqualFold(u.Scheme, "https"):
		return true, "https"
	case host == "localhost":
		return true, "localhost"
	}
	ip := net.ParseIP(host)
	if ip != nil {
		switch {
		case ip.IsLoopback():
			return true, "loopback"
		case ip.IsPrivate():
			return true, "private-ip"
		case ip.IsLinkLocalUnicast():
			return true, "link-local"
		case tailscaleCGNAT.Contains(ip):
			return true, "tailscale"
		}
	}
	return false, "public-http"
}

func fetchRecoveryTargetInfo(targetURL string, headers map[string]string, timeout time.Duration) (int, map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, normalizeRecoveryTargetURL(targetURL)+"/info", nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := remoteHTTPClient(timeout).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, nil, nil
	}
	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, info, nil
}

func fetchRecoveryTargetHealth(targetURL string, headers map[string]string, timeout time.Duration) (int, error) {
	req, err := http.NewRequest(http.MethodGet, normalizeRecoveryTargetURL(targetURL)+"/health", nil)
	if err != nil {
		return 0, err
	}
	for k, v := range headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := remoteHTTPClient(timeout).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func mcpRecoveryTransportStatus() map[string]interface{} {
	cfg, _ := LoadConfig()
	posture := computeRecoveryTransportPosture(cfg)
	return map[string]interface{}{
		"ok":                        true,
		"posture":                   posture,
		"bootstrapSecretConfigured": cfg != nil && strings.TrimSpace(cfg.BootstrapSecretHash) != "",
	}
}

func mcpRecoveryTargetStatus(targetURL, relayPassword string, allowPublicDirectHTTP bool) map[string]interface{} {
	targetURL = normalizeRecoveryTargetURL(targetURL)
	if targetURL == "" {
		return map[string]interface{}{"ok": false, "error": "target_url is required"}
	}
	privateOK, reason := isPrivateRecoveryTarget(targetURL)
	if !privateOK && strings.TrimSpace(relayPassword) == "" && !allowPublicDirectHTTP {
		return map[string]interface{}{
			"ok":     false,
			"error":  "refusing direct public HTTP recovery probe without relay_password or allow_public_direct_http=true",
			"target": targetURL,
		}
	}
	headers := targetRecoveryHeaders(relayPassword, "")
	healthStatus, healthErr := fetchRecoveryTargetHealth(targetURL, headers, 6*time.Second)
	infoStatus, info, infoErr := fetchRecoveryTargetInfo(targetURL, headers, 6*time.Second)
	result := map[string]interface{}{
		"ok":                 true,
		"target":             targetURL,
		"transportSecurity":  reason,
		"healthStatus":       healthStatus,
		"infoStatus":         infoStatus,
		"reachable":          healthErr == nil || infoErr == nil,
		"bootstrapReachable": false,
	}
	if healthErr != nil {
		result["healthError"] = healthErr.Error()
	}
	if infoErr != nil {
		result["infoError"] = infoErr.Error()
	}
	if info != nil {
		state, bootstrap, authExpired := parseDeviceReauthInfo(info)
		result["state"] = state
		result["bootstrapReachable"] = bootstrap
		result["authExpired"] = authExpired
		result["info"] = info
		return result
	}
	switch {
	case healthErr != nil && infoErr != nil:
		result["state"] = "offline"
	case infoStatus == http.StatusUnauthorized || infoStatus == http.StatusForbidden:
		result["state"] = "auth-required"
	default:
		result["state"] = "reachable"
	}
	return result
}

func postRecoveryTargetRecover(targetURL string, headers map[string]string, body map[string]interface{}) (int, []byte, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, normalizeRecoveryTargetURL(targetURL)+"/auth/recover", strings.NewReader(string(raw)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := remoteHTTPClient(20 * time.Second).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

func mcpRecoveryTargetStart(targetURL, mode, bootstrapSecret, bearerToken, relayPassword string, allowPublicDirectHTTP bool) map[string]interface{} {
	targetURL = normalizeRecoveryTargetURL(targetURL)
	if targetURL == "" {
		return map[string]interface{}{"ok": false, "error": "target_url is required"}
	}
	privateOK, reason := isPrivateRecoveryTarget(targetURL)
	if !privateOK && strings.TrimSpace(relayPassword) == "" && !allowPublicDirectHTTP {
		return map[string]interface{}{
			"ok":     false,
			"error":  "refusing direct public HTTP recovery without relay_password or allow_public_direct_http=true",
			"target": targetURL,
		}
	}
	if strings.TrimSpace(bootstrapSecret) == "" && strings.TrimSpace(bearerToken) == "" {
		return map[string]interface{}{"ok": false, "error": "bootstrap_secret or bearer_token is required"}
	}
	selectedMode := strings.ToLower(strings.TrimSpace(mode))
	if selectedMode == "" || selectedMode == "auto" {
		if strings.TrimSpace(bearerToken) != "" {
			selectedMode = "direct"
		} else {
			selectedMode = "pair"
		}
	}
	if strings.TrimSpace(bearerToken) == "" && (selectedMode == "direct" || selectedMode == "device-code") {
		return map[string]interface{}{"ok": false, "error": "direct and device-code recovery require bearer_token"}
	}
	headers := targetRecoveryHeaders(relayPassword, bearerToken)
	body := map[string]interface{}{"mode": selectedMode}
	if secret := strings.TrimSpace(bootstrapSecret); secret != "" {
		body["secret"] = secret
	}
	status, raw, err := postRecoveryTargetRecover(targetURL, headers, body)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error(), "target": targetURL}
	}
	if status >= 300 {
		return map[string]interface{}{
			"ok":                false,
			"error":             extractRemoteError(status, raw),
			"target":            targetURL,
			"httpStatus":        status,
			"transportSecurity": reason,
			"requestedMode":     selectedMode,
		}
	}
	var payload map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["target"] = targetURL
	payload["requestedMode"] = selectedMode
	payload["transportSecurity"] = reason
	return payload
}

func mcpRecoveryTargetWait(targetURL, recoveryID, waitToken, relayPassword string, timeoutSeconds, pollIntervalSeconds int, allowPublicDirectHTTP bool) map[string]interface{} {
	targetURL = normalizeRecoveryTargetURL(targetURL)
	if targetURL == "" {
		return map[string]interface{}{"ok": false, "error": "target_url is required"}
	}
	if strings.TrimSpace(recoveryID) == "" || strings.TrimSpace(waitToken) == "" {
		return map[string]interface{}{"ok": false, "error": "recovery_id and wait_token are required"}
	}
	privateOK, _ := isPrivateRecoveryTarget(targetURL)
	if !privateOK && strings.TrimSpace(relayPassword) == "" && !allowPublicDirectHTTP {
		return map[string]interface{}{
			"ok":     false,
			"error":  "refusing direct public HTTP recovery wait without relay_password or allow_public_direct_http=true",
			"target": targetURL,
		}
	}
	out := waitForRecoverySession(func() (map[string]interface{}, error) {
		return fetchRecoverySessionTarget(targetURL, recoveryID, waitToken, relayPassword)
	}, timeoutSeconds, pollIntervalSeconds)
	out["target"] = targetURL
	return out
}

func mcpDeviceReauthStatus(deviceHint string) map[string]interface{} {
	cfg, target, err := findOwnedDeviceForHint(deviceHint)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	probe := probeOwnedDeviceReauth(cfg, target)
	return map[string]interface{}{
		"ok":                true,
		"deviceId":          target.DeviceID,
		"name":              target.Name,
		"platform":          target.Platform,
		"hostName":          target.HostName,
		"isOnline":          target.IsOnline,
		"state":             probe.State,
		"reachable":         probe.Reachable,
		"recommendedMode":   recommendedReauthMode(probe.State),
		"canRecoverNow":     probe.State == "bootstrap" || probe.State == "yaver-auth-expired" || probe.State == "ready-to-connect",
		"recoveryTransport": probe.Transport,
		"probe":             probe,
	}
}

func doOwnedDeviceRecover(cfg *Config, target *DeviceInfo, mode, bootstrapSecret string) (RemoteAgentCandidate, int, []byte, error) {
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return RemoteAgentCandidate{}, 0, nil, err
	}
	if len(candidates) == 0 {
		return RemoteAgentCandidate{}, 0, nil, fmt.Errorf("device has no transport candidates")
	}
	body := map[string]interface{}{"mode": mode}
	if strings.TrimSpace(bootstrapSecret) != "" {
		body["secret"] = strings.TrimSpace(bootstrapSecret)
	}
	raw, _ := json.Marshal(body)
	return doRemoteAgentRequest(context.Background(), candidates, cfg.AuthToken, http.MethodPost, "/auth/recover", raw, 20*time.Second)
}

func fetchOwnedDeviceRecoverySession(cfg *Config, target *DeviceInfo, recoveryID, waitToken string) (map[string]interface{}, error) {
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("device has no transport candidates")
	}
	path := "/auth/recover/session?id=" + urlQueryEscape(strings.TrimSpace(recoveryID)) + "&wait_token=" + urlQueryEscape(strings.TrimSpace(waitToken))
	_, status, raw, reqErr := doRemoteAgentRequest(context.Background(), candidates, cfg.AuthToken, http.MethodGet, path, nil, 10*time.Second)
	if reqErr != nil {
		return nil, reqErr
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("%s", extractRemoteError(status, raw))
	}
	var payload map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return payload, nil
}

func extractRemoteError(status int, raw []byte) string {
	message := strings.TrimSpace(string(raw))
	if len(raw) > 0 {
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err == nil {
			if msg := strings.TrimSpace(anyString(body["error"])); msg != "" && msg != "<nil>" {
				message = msg
			}
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return message
}

// trySSHDeviceRecovery is the last-resort transport for device_reauth_start.
// Reports ok=false only by returning handled=false, so the caller keeps its
// own (more specific) HTTP error when SSH isn't a usable path either —
// "ssh: Permission denied" would be a worse answer than "relay rejected".
//
// Bounded at 45s: this runs inside an MCP call, and a hung ssh (host key
// prompt, dead route) must not wedge the tool.
func trySSHDeviceRecovery(cfg *Config, target *DeviceInfo, probe deviceReauthProbe) (map[string]interface{}, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	transport, err := recoverDeviceAuthOverSSH(ctx, cfg, target)
	if err != nil {
		return nil, false
	}
	return sshRecoveryResult(cfg, target, probe, transport), true
}

func sshRecoveryResult(cfg *Config, target *DeviceInfo, probe deviceReauthProbe, transport string) map[string]interface{} {
	after := probeOwnedDeviceReauth(cfg, target)
	return map[string]interface{}{
		"ok":                true,
		"deviceId":          target.DeviceID,
		"mode":              "pair",
		"recoveryTransport": transport,
		"state":             after.State,
		"stateBefore":       probe.State,
		"probe":             after,
		"note":              "signed in by pushing this machine's session into the target's pair window",
	}
}

// tryPairWindowRecovery handles the bootstrap case over HTTP. mode "pair"
// via /auth/recover only OPENS a window and hands back a session id for
// someone else to submit into — for an owner-driven fix, we ARE that
// someone else, so drive the window to completion instead of returning a
// half-finished handshake the caller has to babysit.
func tryPairWindowRecovery(cfg *Config, target *DeviceInfo, probe deviceReauthProbe) (map[string]interface{}, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	transport, err := recoverDeviceAuthViaPairWindow(ctx, cfg, target)
	if err != nil {
		return nil, false
	}
	return sshRecoveryResult(cfg, target, probe, transport), true
}

func mcpDeviceReauthStart(deviceHint, mode, bootstrapSecret string) map[string]interface{} {
	cfg, target, err := findOwnedDeviceForHint(deviceHint)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	probe := probeOwnedDeviceReauth(cfg, target)
	selectedMode := strings.ToLower(strings.TrimSpace(mode))
	if selectedMode == "" || selectedMode == "auto" {
		selectedMode = recommendedReauthMode(probe.State)
	}
	switch selectedMode {
	case "none":
		return map[string]interface{}{
			"ok":       false,
			"error":    "device auth is already healthy",
			"deviceId": target.DeviceID,
			"state":    probe.State,
			"probe":    probe,
		}
	case "wait-for-device", "check-transport":
		// No HTTP transport reaches this box — which is the NORMAL state
		// for auth loss, not an edge case: an unauthenticated agent gets
		// its relay registration rejected, so the one transport that
		// survives NAT is the first one auth loss removes. Refusing here
		// left the box unrecoverable from anywhere but its own LAN. SSH
		// doesn't depend on Yaver's auth state, so try it before giving up.
		if res, ok := trySSHDeviceRecovery(cfg, target, probe); ok {
			return res
		}
		return map[string]interface{}{
			"ok":       false,
			"error":    "device is not reachable over HTTP or SSH yet",
			"deviceId": target.DeviceID,
			"state":    probe.State,
			"probe":    probe,
		}
	case "direct", "pair", "device-code":
	default:
		return map[string]interface{}{"ok": false, "error": "mode must be auto, direct, pair, or device-code"}
	}

	// A reachable bootstrap box can be finished off right here.
	if selectedMode == "pair" && probe.Reachable {
		if res, ok := tryPairWindowRecovery(cfg, target, probe); ok {
			return res
		}
	}

	chosen, status, raw, reqErr := doOwnedDeviceRecover(cfg, target, selectedMode, bootstrapSecret)
	if reqErr != nil {
		// HTTP found a transport during the probe but lost it mid-recovery
		// (relay drop, tunnel flap). Same reasoning as above — fall back.
		if res, ok := trySSHDeviceRecovery(cfg, target, probe); ok {
			return res
		}
		return map[string]interface{}{
			"ok":       false,
			"error":    reqErr.Error(),
			"deviceId": target.DeviceID,
			"state":    probe.State,
			"probe":    probe,
		}
	}
	if status >= 300 {
		return map[string]interface{}{
			"ok":                false,
			"error":             extractRemoteError(status, raw),
			"deviceId":          target.DeviceID,
			"name":              target.Name,
			"state":             probe.State,
			"requestedMode":     selectedMode,
			"recoveryTransport": firstNonEmpty(strings.TrimSpace(chosen.Label), chosen.Kind),
			"httpStatus":        status,
			"probe":             probe,
		}
	}
	var payload map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["deviceId"] = target.DeviceID
	payload["name"] = target.Name
	payload["requestedMode"] = selectedMode
	payload["recoveryTransport"] = firstNonEmpty(strings.TrimSpace(chosen.Label), chosen.Kind)
	payload["probe"] = probe
	return payload
}

func mcpDeviceReauthWait(deviceHint, recoveryID, waitToken string, timeoutSeconds, pollIntervalSeconds int) map[string]interface{} {
	if strings.TrimSpace(recoveryID) != "" && strings.TrimSpace(waitToken) != "" {
		if strings.TrimSpace(deviceHint) != "" {
			cfg, target, err := findOwnedDeviceForHint(deviceHint)
			if err != nil {
				return map[string]interface{}{"ok": false, "error": err.Error()}
			}
			out := waitForRecoverySession(func() (map[string]interface{}, error) {
				return fetchOwnedDeviceRecoverySession(cfg, target, recoveryID, waitToken)
			}, timeoutSeconds, pollIntervalSeconds)
			out["deviceId"] = target.DeviceID
			out["name"] = target.Name
			return out
		}
		out := waitForRecoverySession(func() (map[string]interface{}, error) {
			sess, err := recoverySessionStatus(strings.TrimSpace(recoveryID), strings.TrimSpace(waitToken))
			if err != nil {
				return nil, err
			}
			return recoverySessionPayload(sess), nil
		}, timeoutSeconds, pollIntervalSeconds)
		if strings.TrimSpace(deviceHint) != "" {
			out["deviceHint"] = deviceHint
		}
		return out
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	if timeoutSeconds > 300 {
		timeoutSeconds = 300
	}
	if pollIntervalSeconds <= 0 {
		pollIntervalSeconds = 3
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	var last map[string]interface{}
	for {
		last = mcpDeviceReauthStatus(deviceHint)
		if ok, _ := last["ok"].(bool); ok {
			if state := strings.TrimSpace(anyString(last["state"])); state == "healthy" || state == "ready-to-connect" {
				last["ready"] = true
				return last
			}
		}
		if time.Now().After(deadline) {
			if last == nil {
				last = map[string]interface{}{}
			}
			last["ok"] = false
			last["timedOut"] = true
			last["ready"] = false
			if strings.TrimSpace(anyString(last["error"])) == "" {
				last["error"] = "timed out waiting for device auth recovery"
			}
			return last
		}
		time.Sleep(time.Duration(pollIntervalSeconds) * time.Second)
	}
}

func decorateRunnerBrowserAuthMCP(out map[string]interface{}) map[string]interface{} {
	if out == nil {
		return out
	}
	sess, _ := out["session"].(map[string]interface{})
	if sess == nil {
		return out
	}
	status := strings.TrimSpace(anyString(sess["status"]))
	openURL := strings.TrimSpace(anyString(sess["openUrl"]))
	code := strings.TrimSpace(anyString(sess["code"]))
	authConfigured, _ := sess["authConfigured"].(bool)
	var next string
	switch {
	case authConfigured || status == "completed":
		next = "done: runner subscription OAuth is configured; call runner_auth_status to verify if needed."
	case status == "failed":
		next = "failed: surface session.error/detail and restart runner_auth_browser_start if the user wants to retry."
	case status == "cancelled":
		next = "cancelled: restart runner_auth_browser_start when the user is ready."
	case status == "verifying":
		next = "wait: call runner_auth_browser_status until completed or failed."
	case openURL != "":
		next = "open_url: show session.openUrl to the user, ask them to finish provider login, then submit the copied code with runner_auth_browser_submit_code."
	case code != "":
		next = "show_code: show session.code to the user and ask them to complete the provider device-auth page, then poll runner_auth_browser_status."
	default:
		next = "wait_for_url: call runner_auth_browser_status shortly; the runner process is still printing its OAuth instructions."
	}
	out["next_action"] = next
	out["subscription_oauth_only"] = true
	out["never_api_key"] = true
	return out
}

func mcpRunnerBrowserAuthStart(deviceID, runner string, waitSeconds int) map[string]interface{} {
	body := map[string]interface{}{"runner": runner}
	if waitSeconds != 0 {
		body["wait_seconds"] = waitSeconds
	}
	if strings.TrimSpace(deviceID) != "" {
		out, err := proxyToDeviceJSON(context.Background(), "runner_auth_browser_start", strings.TrimSpace(deviceID), http.MethodPost, "/runner-auth/browser/start", body)
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return decorateRunnerBrowserAuthMCP(out)
	}
	out, err := localAgentRequest(http.MethodPost, "/runner-auth/browser/start", body)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return decorateRunnerBrowserAuthMCP(out)
}

func mcpRunnerBrowserAuthStatus(deviceID, sessionID string) map[string]interface{} {
	path := "/runner-auth/browser/status?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	if strings.TrimSpace(deviceID) != "" {
		out, err := proxyToDeviceJSON(context.Background(), "runner_auth_browser_status", strings.TrimSpace(deviceID), http.MethodGet, path, nil)
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return out
	}
	out, err := localAgentRequest(http.MethodGet, path, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return decorateRunnerBrowserAuthMCP(out)
}

func mcpRunnerBrowserAuthSubmitCode(deviceID, sessionID, code string) map[string]interface{} {
	path := "/runner-auth/browser/submit-code?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	if strings.TrimSpace(deviceID) != "" {
		out, err := proxyToDeviceJSON(context.Background(), "runner_auth_browser_submit_code", strings.TrimSpace(deviceID), http.MethodPost, path, map[string]string{"code": code})
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return out
	}
	out, err := localAgentRequest(http.MethodPost, path, map[string]interface{}{"code": code})
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return decorateRunnerBrowserAuthMCP(out)
}

func mcpRunnerBrowserAuthCancel(deviceID, sessionID string) map[string]interface{} {
	path := "/runner-auth/browser/cancel?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	if strings.TrimSpace(deviceID) != "" {
		out, err := proxyToDeviceJSON(context.Background(), "runner_auth_browser_cancel", strings.TrimSpace(deviceID), http.MethodPost, path, map[string]string{})
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return out
	}
	out, err := localAgentRequest(http.MethodPost, path, map[string]interface{}{})
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return decorateRunnerBrowserAuthMCP(out)
}

// mcpRunnerAuthCredentialsImport copies a subscription token blob to the
// named device's runner credentials file. See the HTTP handler at
// runner_auth_browser_http.go::handleRunnerAuthCredentialsImport for
// detail on storage paths and side-effects.
//
// Yaver is a single-user wrapper — when claude is already signed in on
// one of the user's devices, this tool ships that working state to a
// remote box without re-running the OAuth flow there (which is the
// fragile path on macOS-headless boxes).
func mcpRunnerAuthCredentialsImport(deviceID, runner, credentialsJSON string) map[string]interface{} {
	body := map[string]string{"runner": runner, "credentialsJson": credentialsJSON}
	if strings.TrimSpace(deviceID) != "" {
		out, err := proxyToDeviceJSON(context.Background(), "runner_auth_credentials_import", strings.TrimSpace(deviceID), http.MethodPost, "/runner-auth/credentials/import", body)
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return out
	}
	out, err := localAgentRequest(http.MethodPost, "/runner-auth/credentials/import", map[string]interface{}{
		"runner":          runner,
		"credentialsJson": credentialsJSON,
	})
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return out
}
