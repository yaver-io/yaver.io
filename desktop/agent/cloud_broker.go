package main

// cloud_broker.go — agent side of SEAMLESS, SECURE remote-box onboarding.
//
// "The box is born authenticated." Instead of the operator SSHing into a fresh
// box and pasting a device code (the gh-auth-login dance), the ALREADY-
// AUTHENTICATED daemon brokers a pre-authorized device code for the new box and
// injects it into cloud-init. On first boot the box exchanges the code once for
// its OWN token and self-registers — no interactive OAuth on the server.
//
// Security (see backend/convex/deviceCode.ts::createAuthorizedDeviceCode +
// docs/yaver-mcp-machine-onboarding.md):
//   - The broker mint is gated on THIS daemon's own session → the new box is
//     bound to the same user (you can only broker into your own account).
//   - The value injected into cloud-init is only the 15-min deviceCode HANDLE;
//     the real token is fetched exactly once by the box via the poll route
//     (pendingToken cleared on first read), so a rooted box reading its own
//     metadata later finds a spent, worthless handle.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// brokerNewMachineDeviceCode asks Convex (as this authenticated daemon) to mint
// a pre-authorized device code for a new box named machineName. Returns the
// deviceCode handle + the Convex site URL the box will poll.
func brokerNewMachineDeviceCode(machineName, arch string) (deviceCode, convexSite string, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return "", "", fmt.Errorf("not authenticated — run `yaver auth` first so the box can inherit your identity")
	}
	site := strings.TrimRight(cfg.ConvexSiteURL, "/")
	payload, _ := json.Marshal(map[string]string{
		"machineName": strings.TrimSpace(machineName),
		"platform":    "linux",
		"arch":        firstNonEmpty(strings.TrimSpace(arch), "amd64"),
	})
	req, err := newBearerRequest(http.MethodPost, site+"/auth/device-code/broker", cfg.AuthToken, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("broker request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", "", fmt.Errorf("broker rejected: your session is not valid — run `yaver auth`")
	}
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("broker failed: HTTP %d", resp.StatusCode)
	}
	var out struct {
		DeviceCode string `json:"deviceCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(out.DeviceCode) == "" {
		return "", "", fmt.Errorf("broker returned no deviceCode")
	}
	return out.DeviceCode, site, nil
}

// machineSelfRegisterScript returns a shell snippet to append to a new box's
// bootstrap/cloud-init. It exchanges the injected deviceCode for the box's own
// token (once, via the public poll route), writes ~/.yaver/config.json, and
// starts the agent — which registers the box as the user's device over the
// relay. No secret is baked in beyond the single-use, short-TTL handle.
func machineSelfRegisterScript(deviceCode, convexSite string) string {
	site := strings.TrimRight(convexSite, "/")
	return fmt.Sprintf(`
# --- Yaver seamless onboarding: box inherits the operator's identity, no OAuth here.
yaver_self_register() {
  local token=""
  for i in $(seq 1 60); do
    token="$(curl -fsS -m 8 "%s/auth/device-code/poll?device_code=%s" 2>/dev/null \
      | jq -r 'select(.status=="authorized") | .token // empty' 2>/dev/null)"
    [ -n "$token" ] && break
    sleep 3
  done
  if [ -n "$token" ]; then
    install -d -m 700 /root/.yaver
    umask 177
    printf '{"auth_token":"%%s","convex_site_url":"%s"}\n' "$token" > /root/.yaver/config.json
    command -v yaver >/dev/null 2>&1 && yaver serve >/var/log/yaver-serve.log 2>&1 &
    echo "[yaver] self-registered as the operator's device"
  else
    echo "[yaver] self-register FAILED — device code expired/spent; run 'yaver auth' manually"
  fi
}
yaver_self_register
`, site, deviceCode, site)
}
