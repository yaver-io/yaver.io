package main

// cloud_capacity.go — capacity-resilient Hetzner provisioning.
//
// Distilled from a real outage (2026-07-11): Hetzner Ampere/arm ran out of
// stock across EVERY EU location and size at once, and the single-location
// create path just died with a raw `resource_unavailable` error — no fallback,
// no retry, no actionable signal. A real product can't hard-fail because one
// datacenter is momentarily out; it must try alternatives and, when everything
// is genuinely out, say so in a way the UI/MCP/retry loop can act on.
//
// This file centralizes:
//   - the plan→server-type map (previously triplicated + drifting: cx vs cax),
//   - arch-aware location selection (arm/cax is EU-only — a "us" arm request
//     must NOT try ash, which has no cax),
//   - a typed capacity error (errHetznerCapacity) so callers distinguish
//     "try again shortly" from a real misconfiguration,
//   - a multi-location create that falls through capacity errors.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// errHetznerCapacity: the requested server type had no stock in ANY tried
// location. Transient + retry-able — NOT a config error. Callers surface it as
// "capacity temporarily unavailable" (offer retry / another size / another
// region), and automated retry loops back off and try again.
var errHetznerCapacity = errors.New("hetzner capacity unavailable")

// isHetznerCapacityErr reports whether a Hetzner API error code means
// "no stock here" (try elsewhere/later) rather than a permanent failure.
func isHetznerCapacityErr(code string) bool {
	switch code {
	case "resource_unavailable", "resource_limit_exceeded", "no_available_resource":
		return true
	}
	return false
}

// hetznerServerTypeForPlan maps a Yaver plan to a Hetzner arm (cax) type. arm
// so it matches the arm-only bootstrap + golden image. Single source of truth
// (was duplicated in cloud_deploy.go / cloud_stopstart.go / cloud_byo_provision.go).
func hetznerServerTypeForPlan(plan string) string {
	switch strings.TrimSpace(plan) {
	case "pro":
		return "cax21"
	case "scale":
		return "cax31"
	case "starter", "":
		return "cax11"
	default:
		return "cax11"
	}
}

// hetznerLocationsFor returns the ordered list of locations to try for a server
// type, honoring the region preference but ARCH-AWARE. Ampere/arm (cax*) is
// EU-only (fsn1/nbg1/hel1), so region is ignored for arm — a "us" arm request
// still cycles EU rather than dying on ash (no cax there). amd64 (cpx*/cx*) can
// reach US/APAC. Trying multiple locations is what lets a create survive a
// single-DC outage.
func hetznerLocationsFor(serverType, region string) []string {
	eu := []string{"nbg1", "fsn1", "hel1"}
	if strings.HasPrefix(serverType, "cax") {
		return eu // arm = EU-only
	}
	switch strings.TrimSpace(region) {
	case "us":
		return []string{"ash", "hil", "nbg1", "fsn1", "hel1"}
	case "apac", "sin":
		return []string{"sin", "nbg1", "fsn1", "hel1"}
	default:
		return append(append([]string{}, eu...), "ash")
	}
}

// hetznerCreateAttempt POSTs one create at whatever location is in `payload`.
// Returns (ip, id, apiErrorCode, apiErrorMsg, transportErr): a non-empty ip
// means success; a non-empty apiErrorCode (with nil transportErr) means the API
// rejected it — inspect the code.
func (m *CloudDeployManager) hetznerCreateAttempt(token string, payload map[string]interface{}) (string, string, string, string, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", hetznerAPIBase+"/servers", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", "", fmt.Errorf("hetzner API: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Server struct {
			ID        int `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", "", "", fmt.Errorf("parse hetzner response: %w", err)
	}
	if result.Error != nil {
		return "", "", result.Error.Code, result.Error.Message, nil
	}
	ip := result.Server.PublicNet.IPv4.IP
	if ip == "" {
		return "", "", "", "", fmt.Errorf("hetzner returned no IP for new server")
	}
	return ip, fmt.Sprintf("%d", result.Server.ID), "", "", nil
}

// hetznerCreateResilient tries the `base` payload across every location for the
// server type, falling through capacity errors to the next. Returns
// errHetznerCapacity (wrapped, with the list of exhausted locations) when ALL
// are out of stock; surfaces any non-capacity API error immediately.
func (m *CloudDeployManager) hetznerCreateResilient(token, serverType, region string, base map[string]interface{}) (string, string, error) {
	locations := hetznerLocationsFor(serverType, region)
	var triedCapacity []string
	var lastCode, lastMsg string
	for _, loc := range locations {
		payload := make(map[string]interface{}, len(base)+2)
		for k, v := range base {
			payload[k] = v
		}
		payload["server_type"] = serverType
		payload["location"] = loc
		ip, id, code, msg, err := m.hetznerCreateAttempt(token, payload)
		if err != nil {
			return "", "", err
		}
		if code == "" {
			return ip, id, nil // placed
		}
		if isHetznerCapacityErr(code) {
			triedCapacity = append(triedCapacity, loc)
			lastCode, lastMsg = code, msg
			continue
		}
		return "", "", fmt.Errorf("hetzner error %s: %s", code, msg)
	}
	if len(triedCapacity) > 0 {
		return "", "", fmt.Errorf("%w: %s has no stock in [%s] (last %s: %s) — retry shortly, or pick another size/region",
			errHetznerCapacity, serverType, strings.Join(triedCapacity, ", "), lastCode, lastMsg)
	}
	return "", "", fmt.Errorf("hetzner error %s: %s", lastCode, lastMsg)
}

// hetznerWaitSSH blocks until the fresh server answers SSH (~boot time), or
// gives up after ~100s. No-op under test (hetznerSkipReadyWait).
func (m *CloudDeployManager) hetznerWaitSSH(ip string) {
	if hetznerSkipReadyWait {
		return
	}
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Second)
		if err := m.cloudSSHExec(ip, "echo ready"); err == nil {
			return
		}
	}
}
