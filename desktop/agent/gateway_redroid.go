package main

// gateway_redroid.go — the password_totp AuthMethod handler (M-G4).
//
// This implements the AuthMethod interface from gateway_broker.go for
// connectors whose service is app/web-only with password + 2FA and NO public
// API. The insight (docs §17): a redroid (cloud Android) instance is not just an
// automation engine — it is a persistent, *trusted device identity*. Once logged
// in and snapshotted (golden snapshot), subsequent reads restore that device and
// need no login, no 2FA, no human.
//
// Auth flow (docs §3 / §17 / §19):
//
//	Ensure(ctx, connector):
//	  if a valid golden snapshot exists (ref in vault) → restore, return Session
//	  else login:
//	    driver.Launch(pkg) → driver.Type(user/pass from vault)
//	    at the 2FA prompt, route by connector.auth.mechanism:
//	      "totp_seed"         → generate code from the vault seed (RFC-6238) → type  // fully remote, no human
//	      "authenticator_app" → read the rotating code off the device app (UiTexts)  // remote
//	      "push_to_app"       → awaitHuman(approve_push, interactive remote-view)     // one remote tap
//	      "sms"               → read OTP from the redroid inbox OR awaitHuman         // sourced
//	      "passkey"           → fail clean: non-relayable → official API / manual
//	    on success → snapshot the logged-in instance → store {instanceId, snapshotId} in vault
//
// HONEST LINES (CLAUDE.md / docs §19): this makes 2FA convenient, it does NOT
// bypass it — the user still owns the seed/approves the push. No captcha
// auto-solve (the interactive gate streams the challenge to the account owner).
// On an anti-automation / block signal → back off + record, never evade.
//
// DEVICE INTERACTION IS BEHIND AN INTERFACE (deviceDriver) so the handler is
// unit-testable WITHOUT a real redroid or the keychain — the test injects a
// double; production wraps droid_interactive.go.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// uiNode is an on-screen node read off the device. Text alone is enough for the
// auth handler (scanning for a rotating code / a login prompt), so the extra
// accessibility fields are OPTIONAL and additive — existing uiNode{Text: …}
// literals keep working. The redroid invoke path (gateway_redroid_invoke.go)
// uses ResourceID/ContentDesc/Clickable to build a robust ScreenSignature and to
// match answerSchema labels, mirroring the Screen model in docs §3.
type uiNode struct {
	Text        string `json:"text"`
	ResourceID  string `json:"resourceId,omitempty"`
	ContentDesc string `json:"contentDesc,omitempty"`
	Role        string `json:"role,omitempty"`
	Bounds      string `json:"bounds,omitempty"`
	Clickable   bool   `json:"clickable,omitempty"`
}

// deviceDriver abstracts the redroid interaction surface the handler needs. The
// real impl (redroidDeviceDriver) wraps droid_interactive.go; the test double
// (in gateway_redroid_test.go) lets us exercise the whole handler offline.
type deviceDriver interface {
	// Launch starts the connector's app (by package id) on the device.
	Launch(pkg string) error
	// LaunchURL opens a URL/deep-link via an Android VIEW intent (e.g. a
	// market://details?id=<pkg> Play page for app provisioning). Additive — the
	// auth handler does not use it, but the app-sync installer
	// (gateway_appsync.go) does.
	LaunchURL(url string) error
	// Type enters text into the focused field.
	Type(text string) error
	// Frame returns the current screen as image bytes (PNG) — used to attach a
	// screenshot to an interactive human gate.
	Frame() ([]byte, error)
	// Tap activates the named/located target (e.g. a "Next" button). target is
	// a generic hint the driver resolves; for the test double it's a no-op log.
	Tap(target string) error
	// UiTexts returns the visible on-screen text nodes (for reading a rotating
	// authenticator code or confirming a prompt).
	UiTexts() ([]uiNode, error)
	// Snapshot captures the logged-in device as a golden snapshot and returns
	// its {instanceId, snapshotId}.
	Snapshot() (instanceID, snapshotID string, err error)
	// RestoreSnapshot restores a previously captured golden snapshot. Returns an
	// error if the snapshot is gone/invalid (→ the handler re-logs in).
	RestoreSnapshot(instanceID, snapshotID string) error
	// ReadSMS returns the most recent OTP-looking code from the device's own
	// SMS inbox, or "" if none / unsupported.
	ReadSMS() (string, error)
}

// redroidDeviceDriver is the production deviceDriver — it wraps the adb-backed
// droid_interactive.go helpers. serial targets a specific attached/emulated
// device (the redroid instance). Snapshot/RestoreSnapshot are best-effort here;
// the golden-snapshot curator (yaver-base pattern) fills these in a later slice,
// so they currently no-op-with-ref rather than fabricate a guarantee.
type redroidDeviceDriver struct {
	serial string
}

func (d *redroidDeviceDriver) Launch(pkg string) error {
	_, err := droidLaunchPackage(d.serial, pkg)
	return err
}

func (d *redroidDeviceDriver) Type(text string) error { return droidText(d.serial, text) }

// LaunchURL opens a URL/deep-link via `am start -a android.intent.action.VIEW -d
// <url>` — used by the app-sync installer to open a market:// Play page.
func (d *redroidDeviceDriver) LaunchURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("droid launch url: url is required")
	}
	_, err := runAdb(d.serial, 12*time.Second, "shell", "am", "start",
		"-a", "android.intent.action.VIEW", "-d", url)
	return err
}

func (d *redroidDeviceDriver) Frame() ([]byte, error) { return droidFrame(d.serial) }

func (d *redroidDeviceDriver) Tap(target string) error {
	// Generic tap by on-screen label is the curator's job (selector store). For
	// now, a key-event ENTER advances most login forms after typing — the
	// handler uses Tap only to confirm, so ENTER is a safe generic action.
	return droidKey(d.serial, 66) // KEYCODE_ENTER
}

func (d *redroidDeviceDriver) UiTexts() ([]uiNode, error) {
	texts, err := droidUITexts(d.serial)
	if err != nil {
		return nil, err
	}
	out := make([]uiNode, 0, len(texts))
	for _, t := range texts {
		out = append(out, uiNode{Text: t})
	}
	return out, nil
}

func (d *redroidDeviceDriver) Snapshot() (string, string, error) {
	// The golden-snapshot mechanism (yaver-base) lands with the curator slice;
	// until then we return the device serial as a stable instance ref and a
	// timestamped snapshot id so the vault round-trips a real reference.
	return d.serial, fmt.Sprintf("snap-%d", time.Now().Unix()), nil
}

func (d *redroidDeviceDriver) RestoreSnapshot(instanceID, snapshotID string) error {
	// No real restore engine yet → report "no valid snapshot" so Ensure takes
	// the login path rather than silently assuming a logged-in device.
	return fmt.Errorf("golden-snapshot restore not yet available for instance %q", instanceID)
}

func (d *redroidDeviceDriver) ReadSMS() (string, error) {
	// GATED: only a clone the user explicitly authorized (read_device_sms) reads
	// its OWN inbox. Without the opt-in we return "" so the handler escalates to a
	// human gate — we never read SMS off a device without that consent. With the
	// grant, query the content provider for the newest OTP (redroid has no SIM ⇒
	// "" cleanly; a SIM'd second-hand phone returns the code).
	if !consentAllows(consentReadDeviceSms) {
		return "", nil
	}
	return droidReadLatestOTP(d.serial)
}

// loginCreds is the {username,password} blob stored in vault under LoginRef.
type loginCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// snapshotRef is the {instanceId,snapshotId} blob stored in vault under DeviceRef.
type snapshotRef struct {
	InstanceID string `json:"instanceId"`
	SnapshotID string `json:"snapshotId"`
}

// passwordTotpHandler implements AuthMethod for connector.Auth.Method ==
// "password_totp".
type passwordTotpHandler struct {
	store CredStore
	// driver is the device the handler drives. Injected so tests use a double.
	driver deviceDriver
	// gate is the human-gate primitive (M-G3). Defaults to the process-wide
	// gatewayGates; tests inject an in-memory store.
	gate *gateStore
}

func newPasswordTotpHandler(store CredStore, driver deviceDriver, gate *gateStore) *passwordTotpHandler {
	if gate == nil {
		gate = gatewayGates
	}
	return &passwordTotpHandler{store: store, driver: driver, gate: gate}
}

func (h *passwordTotpHandler) Method() string { return "password_totp" }

// NeedsHuman reports whether a cold path MAY pause for a human. true for
// push_to_app (always a tap) and sms (may need a read); a totp_seed connector is
// fully remote (false) unless its golden snapshot is gone — which the router
// surfaces as "may pause" conservatively only for the human mechanisms.
func (h *passwordTotpHandler) NeedsHuman(c *Connector) bool {
	switch strings.ToLower(strings.TrimSpace(c.Auth.Mechanism)) {
	case "push_to_app", "sms":
		return true
	default:
		// totp_seed / authenticator_app are auto-satisfiable; passkey fails
		// clean (not a human gate).
		return false
	}
}

// Ensure returns a device-backed session for the connector, restoring the golden
// snapshot when valid, else running login + 2FA + snapshot.
func (h *passwordTotpHandler) Ensure(ctx context.Context, c *Connector) (Session, error) {
	if h.driver == nil {
		return Session{}, fmt.Errorf("gateway: connector %q has no device driver (redroid unavailable)", c.ID)
	}

	// Fast path: a valid golden snapshot ⇒ trusted device, no login/2FA/human.
	if ref, err := h.loadSnapshotRef(c); err == nil && ref.SnapshotID != "" {
		if rErr := h.driver.RestoreSnapshot(ref.InstanceID, ref.SnapshotID); rErr == nil {
			return Session{Kind: SessionDevice, DeviceID: ref.InstanceID}, nil
		}
		// Snapshot gone/stale → fall through to a fresh login.
	}

	return h.login(ctx, c)
}

// login runs the one-time interactive login: launch app, type credentials,
// satisfy the 2FA factor by mechanism, snapshot the logged-in device.
func (h *passwordTotpHandler) login(ctx context.Context, c *Connector) (Session, error) {
	creds, err := h.loadLoginCreds(c)
	if err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q login creds: %w", c.ID, err)
	}

	if err := h.driver.Launch(c.Surface); err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q launch: %w", c.ID, err)
	}
	if err := h.driver.Type(creds.Username); err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q type username: %w", c.ID, err)
	}
	_ = h.driver.Tap("next")
	if err := h.driver.Type(creds.Password); err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q type password: %w", c.ID, err)
	}
	_ = h.driver.Tap("sign_in")

	// Route the second factor.
	if err := h.satisfySecondFactor(ctx, c); err != nil {
		return Session{}, err
	}

	// Logged in → snapshot the trusted device and persist its ref.
	instanceID, snapshotID, err := h.driver.Snapshot()
	if err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q snapshot: %w", c.ID, err)
	}
	if err := h.saveSnapshotRef(c, snapshotRef{InstanceID: instanceID, SnapshotID: snapshotID}); err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q persist snapshot ref: %w", c.ID, err)
	}
	return Session{Kind: SessionDevice, DeviceID: instanceID}, nil
}

// satisfySecondFactor routes the 2FA challenge by the connector's declared
// mechanism. Never bypasses a factor: totp_seed/authenticator_app are codes the
// user legitimately owns; push_to_app/sms escalate to a human gate.
func (h *passwordTotpHandler) satisfySecondFactor(ctx context.Context, c *Connector) error {
	mech := strings.ToLower(strings.TrimSpace(c.Auth.Mechanism))
	switch mech {
	case "", "none":
		return nil // password-only

	case "totp_seed":
		seed, err := h.loadTotpSeed(c)
		if err != nil {
			return fmt.Errorf("gateway: connector %q totp seed: %w", c.ID, err)
		}
		code, err := totpNow(seed)
		if err != nil {
			return fmt.Errorf("gateway: connector %q generate totp: %w", c.ID, err)
		}
		if err := h.driver.Type(code); err != nil {
			return fmt.Errorf("gateway: connector %q type totp: %w", c.ID, err)
		}
		_ = h.driver.Tap("verify")
		return nil

	case "authenticator_app":
		code, err := h.readAuthenticatorCode()
		if err != nil {
			return fmt.Errorf("gateway: connector %q read authenticator code: %w", c.ID, err)
		}
		if err := h.driver.Type(code); err != nil {
			return fmt.Errorf("gateway: connector %q type authenticator code: %w", c.ID, err)
		}
		_ = h.driver.Tap("verify")
		return nil

	case "push_to_app":
		// Irreducible human factor — the user approves the push themselves via a
		// live remote-view window. We NEVER auto-approve.
		return h.gateApprovePush(ctx, c)

	case "sms":
		// Prefer reading the redroid's own inbox; otherwise the user reads the
		// code off their own phone and enters it through a gate.
		if code, _ := h.driver.ReadSMS(); strings.TrimSpace(code) != "" {
			if err := h.driver.Type(code); err != nil {
				return fmt.Errorf("gateway: connector %q type sms code: %w", c.ID, err)
			}
			_ = h.driver.Tap("verify")
			return nil
		}
		return h.gateEnterSMSCode(ctx, c)

	case "passkey":
		// Passkeys are device-bound + phishing-resistant BY DESIGN — that's the
		// security feature. Non-relayable: fail clean and steer to the official
		// API or a manual step. Never attempt to satisfy it.
		return fmt.Errorf("gateway: connector %q uses a passkey (WebAuthn) — non-relayable by design; use the service's official API or sign in manually", c.ID)

	default:
		return fmt.Errorf("gateway: connector %q has unknown 2FA mechanism %q", c.ID, mech)
	}
}

// gateApprovePush suspends on an interactive human gate for a push approval,
// attaching a live frame so the user can approve in a remote-view window.
func (h *passwordTotpHandler) gateApprovePush(ctx context.Context, c *Connector) error {
	res, err := h.gate.awaitHuman(ctx, GateRequest{
		ConnectorID: c.ID,
		Kind:        GateApprovePush,
		Prompt:      fmt.Sprintf("Approve the sign-in push for %q on your phone.", c.ID),
		ViewRef:     "redroid:" + c.ID, // interactive remote-view session ref
	})
	if err != nil {
		return fmt.Errorf("gateway: connector %q push gate: %w", c.ID, err)
	}
	if res.Status != GateResolved || !res.Approved {
		// Timeout / decline → clean abort + recorded finding (a human factor is
		// never auto-satisfied).
		return fmt.Errorf("gateway: connector %q push approval not granted (status %q) — aborting login", c.ID, res.Status)
	}
	return nil
}

// gateEnterSMSCode suspends on a human gate asking the user to read + enter an
// SMS code from their own phone, then types it.
func (h *passwordTotpHandler) gateEnterSMSCode(ctx context.Context, c *Connector) error {
	res, err := h.gate.awaitHuman(ctx, GateRequest{
		ConnectorID: c.ID,
		Kind:        GateEnterCode,
		Prompt:      fmt.Sprintf("Enter the SMS code for %q (sent to your number).", c.ID),
	})
	if err != nil {
		return fmt.Errorf("gateway: connector %q sms gate: %w", c.ID, err)
	}
	if res.Status != GateResolved || strings.TrimSpace(res.Answer) == "" {
		return fmt.Errorf("gateway: connector %q sms code not provided (status %q) — aborting login", c.ID, res.Status)
	}
	if err := h.driver.Type(strings.TrimSpace(res.Answer)); err != nil {
		return fmt.Errorf("gateway: connector %q type sms code: %w", c.ID, err)
	}
	_ = h.driver.Tap("verify")
	return nil
}

// readAuthenticatorCode scans the device's visible text for a 6-digit rotating
// code (an authenticator app showing the OTP on-screen). Best-effort.
func (h *passwordTotpHandler) readAuthenticatorCode() (string, error) {
	nodes, err := h.driver.UiTexts()
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if code := extractSixDigitCode(n.Text); code != "" {
			return code, nil
		}
	}
	return "", fmt.Errorf("no 6-digit code visible on the authenticator screen")
}

// ── vault helpers ─────────────────────────────────────────────────────────────

func (h *passwordTotpHandler) loadLoginCreds(c *Connector) (*loginCreds, error) {
	ref := strings.TrimSpace(c.Auth.LoginRef)
	if ref == "" {
		return nil, fmt.Errorf("no loginRef configured")
	}
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return nil, err
	}
	blob, err := h.store.GetCreds(project, name)
	if err != nil {
		return nil, err
	}
	var lc loginCreds
	if err := json.Unmarshal(blob, &lc); err != nil {
		return nil, fmt.Errorf("decode login creds: %w", err)
	}
	if lc.Username == "" {
		return nil, fmt.Errorf("login creds missing username")
	}
	return &lc, nil
}

func (h *passwordTotpHandler) loadTotpSeed(c *Connector) (string, error) {
	ref := strings.TrimSpace(c.Auth.TotpRef)
	if ref == "" {
		return "", fmt.Errorf("no totpRef configured")
	}
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return "", err
	}
	blob, err := h.store.GetCreds(project, name)
	if err != nil {
		return "", err
	}
	// The seed may be stored raw (base32) or wrapped as {"seed": "..."}.
	raw := strings.TrimSpace(string(blob))
	if strings.HasPrefix(raw, "{") {
		var wrap struct {
			Seed string `json:"seed"`
		}
		if err := json.Unmarshal(blob, &wrap); err == nil && wrap.Seed != "" {
			return wrap.Seed, nil
		}
	}
	if raw == "" {
		return "", fmt.Errorf("empty totp seed")
	}
	return raw, nil
}

func (h *passwordTotpHandler) loadSnapshotRef(c *Connector) (*snapshotRef, error) {
	ref := strings.TrimSpace(c.Auth.DeviceRef)
	if ref == "" {
		return nil, fmt.Errorf("no deviceRef configured")
	}
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return nil, err
	}
	blob, err := h.store.GetCreds(project, name)
	if err != nil {
		return nil, err
	}
	var sr snapshotRef
	if err := json.Unmarshal(blob, &sr); err != nil {
		return nil, fmt.Errorf("decode snapshot ref: %w", err)
	}
	return &sr, nil
}

func (h *passwordTotpHandler) saveSnapshotRef(c *Connector, sr snapshotRef) error {
	ref := strings.TrimSpace(c.Auth.DeviceRef)
	if ref == "" {
		return fmt.Errorf("no deviceRef configured")
	}
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return err
	}
	blob, err := json.Marshal(sr)
	if err != nil {
		return err
	}
	return h.store.SetCreds(project, name, blob)
}

// ── RFC-6238 TOTP code generation ─────────────────────────────────────────────
//
// The agent's TOTP machinery lives in Convex (backend/convex/totp.ts) and isn't
// cleanly callable from Go, so — as the spec allows — we add a small, standard
// RFC-6238 helper here (HMAC-SHA1, 6 digits, 30s step). It is exercised against
// the official RFC-6238 test vectors in gateway_redroid_test.go, so correctness
// is verified deterministically without any device.

// totpDigits / totpPeriod are the standard authenticator defaults.
const (
	totpDigits = 6
	totpPeriod = 30
)

// totpNow generates the current 6-digit TOTP code for a base32 seed.
func totpNow(base32Seed string) (string, error) {
	return totpAt(base32Seed, time.Now())
}

// totpAt generates the 6-digit TOTP code for a base32 seed at a given time.
func totpAt(base32Seed string, t time.Time) (string, error) {
	key, err := decodeBase32Secret(base32Seed)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / totpPeriod
	return hotp(key, counter, totpDigits), nil
}

// decodeBase32Secret decodes a (possibly padded, spaced, lowercase) base32 TOTP
// secret into raw key bytes.
func decodeBase32Secret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	if s == "" {
		return nil, fmt.Errorf("empty totp secret")
	}
	// Tolerate missing padding by re-padding to a multiple of 8.
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode base32 totp secret: %w", err)
	}
	return key, nil
}

// hotp computes an RFC-4226 HOTP value (the per-counter primitive TOTP uses).
func hotp(key []byte, counter uint64, digits int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// extractSixDigitCode returns the first run of exactly 6 consecutive digits in s
// (a rotating authenticator code), or "" if none.
func extractSixDigitCode(s string) string {
	run := 0
	start := -1
	for i := 0; i <= len(s); i++ {
		isDigit := i < len(s) && s[i] >= '0' && s[i] <= '9'
		if isDigit {
			if run == 0 {
				start = i
			}
			run++
			continue
		}
		if run == 6 {
			return s[start : start+6]
		}
		run = 0
	}
	return ""
}
