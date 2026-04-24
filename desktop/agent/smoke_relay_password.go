package main

// smoke_relay_password — the in-agent equivalent of
// ci/remote/smoke/relay-password.sh. Registered with the TaskSupervisor
// so it runs on the same scheduled-task plumbing as heartbeat, peer
// watcher, etc., instead of as a separate systemd timer.
//
// Per the user's direction: "single service that does all". Moving the
// smoke check inside yaver serve means one less pile of scheduling
// infra to keep in sync, and the check sees the agent's own config
// (Convex URL, relay password) without having to load env files.
//
// Safety gate: this flow signs up a throwaway Convex user each run and
// deletes it after. Skipped automatically when YAVER_DISABLE_RELAY_SMOKE=1
// is set — useful on the primary developer machine where you don't
// want to hit prod Convex every 15 min just for a smoke. On the
// Hetzner box we leave it enabled; that's the box designed to prove
// the platform works end-to-end.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultConvexURL      = "https://perceptive-minnow-557.eu-west-1.convex.site"
	smokeInterval         = 15 * time.Minute
	smokeHTTPTimeout      = 15 * time.Second
	smokeEmailDomain      = "yaver.test"
	smokeEmailPrefix      = "e2e-smoke-"
	smokePasswordPrefix   = "SmokeAgent!"
	smokeUserFullNameStub = "In-agent smoke user"
)

// StartRelayPasswordSmoke registers the smoke flow with the process-
// wide supervisor. Idempotent — calling it twice replaces the prior
// registration, matching the rest of the supervisor contract.
//
// Enable / disable toggle:
//
//	YAVER_ENABLE_RELAY_SMOKE=1   → register on boxes that opt in
//	(unset / 0)                  → skip (default — dev boxes)
//
// Rationale: running a Convex signup + delete every 15 min from every
// running agent would explode throwaway-user count. We default OFF
// and have Hetzner's bootstrap set the env var.
func StartRelayPasswordSmoke() {
	if v := os.Getenv("YAVER_ENABLE_RELAY_SMOKE"); v != "1" && v != "true" {
		return
	}
	convexURL := strings.TrimRight(
		firstNonEmpty(os.Getenv("YAVER_CONVEX_URL"), defaultConvexURL),
		"/",
	)
	interval := smokeInterval
	if raw := os.Getenv("YAVER_RELAY_SMOKE_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 30*time.Second {
			interval = parsed
		}
	}

	client := &http.Client{Timeout: smokeHTTPTimeout}
	SupervisedGo("smoke:relay-password", interval, true, func(ctx context.Context) error {
		return runRelayPasswordSmoke(ctx, client, convexURL)
	})
}

// runRelayPasswordSmoke: signup → get settings → verify relay password
// validates via Convex → (optional) verify live relay accepts it →
// delete the throwaway user. Returns the FIRST regression error
// encountered — the supervisor records it as LastError.
//
// Error strings are intentionally short + greppable; an operator
// reading /self-check should see "relay 401" or "convex unreachable"
// rather than a stack trace.
func runRelayPasswordSmoke(ctx context.Context, client *http.Client, convexURL string) error {
	uuid, err := randHex(6)
	if err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	email := smokeEmailPrefix + uuid + "@" + smokeEmailDomain
	password := smokePasswordPrefix + uuid

	// 1. Signup (throwaway).
	signupBody, _ := json.Marshal(map[string]string{
		"email": email, "password": password, "fullName": smokeUserFullNameStub,
	})
	resp, body, err := smokeRequest(ctx, client, http.MethodPost, convexURL+"/auth/signup", nil, signupBody)
	if err != nil {
		return fmt.Errorf("signup unreachable: %w", err)
	}
	if resp != 200 {
		return fmt.Errorf("signup: HTTP %d body=%s", resp, snippet(body))
	}
	token := gjsonString(body, "token")
	if token == "" {
		return errors.New("signup: no token in response")
	}

	// Always try to delete the user on the way out, even on regression.
	defer func() {
		_, _, _ = smokeRequest(ctx, client, http.MethodPost,
			convexURL+"/auth/delete-account",
			map[string]string{"Authorization": "Bearer " + token},
			nil)
	}()

	// 2. Settings — relayPassword must be populated.
	resp, body, err = smokeRequest(ctx, client, http.MethodGet,
		convexURL+"/settings",
		map[string]string{"Authorization": "Bearer " + token}, nil)
	if err != nil {
		return fmt.Errorf("/settings unreachable: %w", err)
	}
	if resp != 200 {
		return fmt.Errorf("/settings: HTTP %d", resp)
	}
	userPW := gjsonNestedString(body, "settings", "relayPassword")
	if userPW == "" {
		// New-signup race — exercise the repair endpoint we built for
		// exactly this case. If repair also fails, that's the regression.
		resp, body, err = smokeRequest(ctx, client, http.MethodPost,
			convexURL+"/settings/repair-relay",
			map[string]string{"Authorization": "Bearer " + token}, nil)
		if err != nil {
			return fmt.Errorf("/settings/repair-relay unreachable: %w", err)
		}
		if resp != 200 {
			return fmt.Errorf("/settings/repair-relay: HTTP %d", resp)
		}
		// Re-read.
		_, body, err = smokeRequest(ctx, client, http.MethodGet,
			convexURL+"/settings",
			map[string]string{"Authorization": "Bearer " + token}, nil)
		if err != nil {
			return fmt.Errorf("/settings re-read unreachable: %w", err)
		}
		userPW = gjsonNestedString(body, "settings", "relayPassword")
		if userPW == "" {
			return errors.New("relayPassword empty even after repair")
		}
	}

	// 3. Convex-side validate: relay asks Convex "is this password valid".
	validateBody, _ := json.Marshal(map[string]string{"password": userPW})
	resp, body, err = smokeRequest(ctx, client, http.MethodPost,
		convexURL+"/relay/validate", nil, validateBody)
	if err != nil {
		return fmt.Errorf("/relay/validate unreachable: %w", err)
	}
	if resp != 200 {
		return fmt.Errorf("/relay/validate: HTTP %d body=%s", resp, snippet(body))
	}
	if gjsonString(body, "userId") == "" {
		return errors.New("convex rejected freshly-synced relay password")
	}

	// 4. Live-relay probe. Agents fetch the live relay httpUrl from
	//    /config. A bogus deviceId should give us 404/5xx (device
	//    unknown), but crucially NOT 401 "invalid relay password".
	_, cfgBody, err := smokeRequest(ctx, client, http.MethodGet, convexURL+"/config", nil, nil)
	if err != nil {
		// Non-fatal — we already proved Convex accepts the password.
		return nil
	}
	relayURL := firstRelayURL(cfgBody)
	if relayURL == "" {
		return nil
	}
	probeURL := fmt.Sprintf("%s/d/smoke-nonexistent/health?__rp=%s",
		strings.TrimRight(relayURL, "/"), userPW)
	code, _, err := smokeRequest(ctx, client, http.MethodGet, probeURL, nil, nil)
	if err != nil {
		// Network hiccup, not a password regression.
		return nil
	}
	if code == 401 {
		return errors.New("live relay 401 — relay rejects freshly-synced password (prod drift)")
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────

func smokeRequest(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

// Minimal JSON accessors — we don't need gjson for a handful of known
// keys. Ignores nested structures we don't read; refuses to panic on
// malformed input.

func gjsonString(body []byte, key string) string {
	var m map[string]interface{}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func gjsonNestedString(body []byte, outer, inner string) string {
	var m map[string]interface{}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	sub, ok := m[outer].(map[string]interface{})
	if !ok {
		return ""
	}
	v, _ := sub[inner].(string)
	return v
}

func firstRelayURL(body []byte) string {
	var m struct {
		RelayServers []struct {
			HTTPURL string `json:"httpUrl"`
		} `json:"relayServers"`
	}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if len(m.RelayServers) == 0 {
		return ""
	}
	return m.RelayServers[0].HTTPURL
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func snippet(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// Not imported elsewhere in this file, but the supervisor log uses
// log.Printf for structured diagnostics so we match here.
var _ = log.Printf
