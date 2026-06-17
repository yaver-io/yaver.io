package main

// gateway_consent.go — the Personal Agent Gateway CONSENT/POLICY layer.
//
// The sensitive phone-clone capabilities — sharing the user's installed-app list,
// auto-forwarding a one-time code, and letting a clone read its own SMS inbox —
// are each a SEPARATE, EXPLICIT opt-in. Nothing here is on by default. A grant is
// recorded LOCAL-FIRST (~/.yaver/gateway-consent.json, never Convex) and every
// change is written to the gateway audit ledger (gateway_audit.go), so the record
// of "what I allowed and when" lives on the user's own device.
//
// DELIBERATELY NOT A UI WALL: the gateway is P2P + open source + local-first, and
// the full disclosure lives in the docs + the privacy policy (web/app/privacy),
// not in a thicket of in-app toggles. This file is the enforcement + the small
// programmatic surface (gateway_consent / gateway_consent_set) the AI or a power
// user drives; the human-readable explanation is the doc, by design.
//
// Each capability gates a feature built elsewhere:
//   - share_app_inventory → mcpGatewayPhoneInventoryReport (gateway_phone_inventory.go)
//   - auto_relay_otp      → mcpGatewayProvideOTP            (gateway_gate.go)
//   - read_device_sms     → the device ReadSMS path         (gateway_redroid.go)

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Consent feature keys (stable strings used by the MCP set tool + gating checks).
const (
	consentShareAppInventory = "share_app_inventory"
	consentAutoRelayOtp      = "auto_relay_otp"
	consentReadDeviceSms     = "read_device_sms"
)

// GatewayConsent is the local, opt-in consent record. All fields default false —
// a fresh install grants nothing until the user explicitly opts in.
type GatewayConsent struct {
	ShareAppInventory bool  `json:"shareAppInventory"`
	AutoRelayOtp      bool  `json:"autoRelayOtp"`
	ReadDeviceSms     bool  `json:"readDeviceSms"`
	UpdatedAt         int64 `json:"updatedAt,omitempty"`
}

// consentFeatureDoc is the plain-language description surfaced by the get tool so
// the AI can explain a capability before asking the user to grant it.
type consentFeatureDoc struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// gatewayConsentPath returns ~/.yaver/gateway-consent.json (local, never Convex).
func gatewayConsentPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway-consent.json"), nil
}

// loadGatewayConsent reads the consent record. A missing file ⇒ all-false (the
// safe default), not an error.
func loadGatewayConsent() (GatewayConsent, error) {
	path, err := gatewayConsentPath()
	if err != nil {
		return GatewayConsent{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GatewayConsent{}, nil
		}
		return GatewayConsent{}, fmt.Errorf("read gateway consent: %w", err)
	}
	var c GatewayConsent
	if err := json.Unmarshal(data, &c); err != nil {
		return GatewayConsent{}, fmt.Errorf("parse gateway consent: %w", err)
	}
	return c, nil
}

// saveGatewayConsent persists the consent record (atomic write, 0600).
func saveGatewayConsent(c GatewayConsent) error {
	path, err := gatewayConsentPath()
	if err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Unix()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gateway consent: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write gateway consent tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename gateway consent: %w", err)
	}
	return nil
}

// consentGet reads whether a feature is currently allowed. A read error ⇒ false
// (fail-closed: when in doubt, the sensitive capability is OFF).
func consentGet(c GatewayConsent, feature string) bool {
	switch feature {
	case consentShareAppInventory:
		return c.ShareAppInventory
	case consentAutoRelayOtp:
		return c.AutoRelayOtp
	case consentReadDeviceSms:
		return c.ReadDeviceSms
	default:
		return false
	}
}

// consentAllows loads the record and reports whether feature is granted. On any
// load error it returns false (fail-closed).
func consentAllows(feature string) bool {
	c, err := loadGatewayConsent()
	if err != nil {
		return false
	}
	return consentGet(c, feature)
}

// consentSet applies enabled to feature on c. Returns an error for an unknown
// feature so a typo can never silently no-op a grant.
func consentSet(c *GatewayConsent, feature string, enabled bool) error {
	switch feature {
	case consentShareAppInventory:
		c.ShareAppInventory = enabled
	case consentAutoRelayOtp:
		c.AutoRelayOtp = enabled
	case consentReadDeviceSms:
		c.ReadDeviceSms = enabled
	default:
		return fmt.Errorf("unknown consent feature %q", feature)
	}
	return nil
}

// consentNotEnabled is the standard response when a gated entrypoint is called
// without the grant — clear, actionable, and honest about where to opt in.
func consentNotEnabled(feature string) map[string]interface{} {
	return map[string]interface{}{
		"ok":           false,
		"needsConsent": true,
		"feature":      feature,
		"note": "This capability is off until you opt in (it is peer-to-peer, open source, and local-first). " +
			"Grant it with gateway_consent_set{feature:\"" + feature + "\", enabled:true}. " +
			"Full disclosure: yaver.io/privacy.",
	}
}

// consentFeatureDocs returns the canonical feature list with current state, for
// the get tool. The descriptions are the source of the in-doc/policy wording.
func consentFeatureDocs(c GatewayConsent) []consentFeatureDoc {
	return []consentFeatureDoc{
		{
			Key:         consentShareAppInventory,
			Title:       "Share my installed-app list with a clone",
			Description: "Lets the Yaver app report which apps are on your phone so the same set can be mirrored onto your clone. App names only, stored on your devices, never on our backend.",
			Enabled:     c.ShareAppInventory,
		},
		{
			Key:         consentAutoRelayOtp,
			Title:       "Auto-forward one-time codes to my agent",
			Description: "Lets your phone pass a one-time code to a sign-in that is waiting for it on your clone. The code is relayed, never stored, and 2FA is never bypassed — you still receive it yourself.",
			Enabled:     c.AutoRelayOtp,
		},
		{
			Key:         consentReadDeviceSms,
			Title:       "Let a clone read its own SMS inbox",
			Description: "Lets a clone you own read one-time codes from its OWN SIM's inbox (a dedicated number), so SMS sign-in completes without you. Never reads any other phone's messages.",
			Enabled:     c.ReadDeviceSms,
		},
	}
}

// ── MCP entrypoints ──────────────────────────────────────────────────────────

// mcpGatewayConsent returns the current consent state + plain-language docs. It
// is read-only and reveals no secrets.
func mcpGatewayConsent() interface{} {
	c, err := loadGatewayConsent()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"features":  consentFeatureDocs(c),
		"updatedAt": c.UpdatedAt,
		"note": "Personal Agent Gateway is peer-to-peer, open source, and local-first: " +
			"grants live on your device (never our backend) and are revocable here. See yaver.io/privacy.",
	}
}

// mcpGatewayConsentSet grants or revokes one feature, persisting the change and
// recording it in the local audit ledger.
func mcpGatewayConsentSet(feature string, enabled bool) interface{} {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return map[string]interface{}{"error": "feature is required"}
	}
	c, err := loadGatewayConsent()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := consentSet(&c, feature, enabled); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := saveGatewayConsent(c); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	outcome := "consent_revoked"
	if enabled {
		outcome = "consent_granted"
	}
	// Best-effort local audit — a write failure must not block the grant itself.
	_ = appendGatewayAudit(GatewayAuditEntry{
		Connector:  "_consent",
		Capability: feature,
		Verb:       "update",
		Risk:       "consent",
		Confirmed:  "explicit",
		Outcome:    outcome,
	})
	return map[string]interface{}{
		"ok":      true,
		"feature": feature,
		"enabled": enabled,
		"note":    "Recorded locally + audited (never our backend). Revoke anytime by setting enabled:false.",
	}
}
