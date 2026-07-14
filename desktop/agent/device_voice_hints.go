package main

// device_voice_hints.go — the spoken names of a machine.
//
// `alias` is one short token you TYPE at a shell (`yaver ssh pokayoke`).
// Voice hints are many, natural, and you never type them — you SAY them:
// "my mac mini", "the box at maltepe", "work laptop".
//
// They exist because of a hard constraint on the car surface: Apple's CarPlay
// voice-based-conversation category forbids showing text or lists in response
// to a query, so a driver can never be handed a device picker to tap. Speaking
// the machine's name is the ONLY way to retarget a turn while driving. The
// fuzzy match happens on the phone (mobile/src/lib/carMachineSwitch.ts), which
// is why the agent's job here is just to own the list.
//
// Display-only data, same privacy class as `name`/`alias` — never a path, an
// IP, or a secret, so it is safe to hold in Convex under the privacy contract.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// deviceVoiceHintsResult is what Convex's /devices/voice-hints returns.
type deviceVoiceHintsResult struct {
	OK         bool     `json:"ok"`
	VoiceHints []string `json:"voiceHints"`
}

// setDeviceVoiceHints replaces (hints) or mutates (add/remove) the spoken names
// for a device. Exactly one mode should be used per call; Convex checks `hints`
// first. Owner-scoped server-side — a token that doesn't own the device is
// rejected there, not here.
func setDeviceVoiceHints(
	ctx context.Context,
	token, convexURL, deviceID string,
	hints, add, remove []string,
) (*deviceVoiceHintsResult, error) {
	if strings.TrimSpace(deviceID) == "" {
		return nil, fmt.Errorf("deviceId is required")
	}
	payload := map[string]interface{}{"deviceId": deviceID}
	if hints != nil {
		payload["hints"] = hints
	}
	if len(add) > 0 {
		payload["add"] = add
	}
	if len(remove) > 0 {
		payload["remove"] = remove
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal voice hints: %w", err)
	}

	url := strings.TrimRight(convexURL, "/") + "/devices/voice-hints"
	req, err := newBearerRequest("POST", url, token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create voice-hints request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voice-hints request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("voice-hints update failed: %s", e.Error)
	}

	var out deviceVoiceHintsResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode voice-hints response: %w", err)
	}
	return &out, nil
}
