package main

// gateway_broker.go — the Auth Broker.
//
// The broker turns a connector manifest into a *valid session* (a bearer token
// ready to use), acquiring or refreshing credentials as needed. It selects an
// AuthMethod handler by connector.Auth.Method.
//
// SLICE SCOPE: only the oauth_code handler is implemented (gateway_oauth.go).
// The AuthMethod interface is deliberately shaped so future handlers slot in
// later WITHOUT changing the broker or the invoke path:
//
//   - password_totp (redroid device-as-2FA, golden snapshot)   — docs §17
//   - sms (OTP sourced from your own number)                    — docs §17
//   - human-gate (captcha/push approval, resumable PendingGate) — docs §1.4
//
// Those handlers will set NeedsHuman(connector) == true and, on first use,
// suspend on a PendingGate (built in milestone M-G3). The oauth_code handler
// in this slice never needs a human after the initial consent (it only
// refreshes), so NeedsHuman returns false.

import (
	"context"
	"fmt"
	"time"
)

// SessionKind classifies how a session authenticates a request.
type SessionKind string

const (
	// SessionBearer carries an OAuth/bearer access token in the Authorization
	// header. (The only kind this slice produces.)
	SessionBearer SessionKind = "bearer"
	// SessionStorageState (future) carries a serialized web session
	// (playwright storageState). SessionDevice (future) references a redroid
	// golden snapshot. Declared for forward shape only.
	SessionStorageState SessionKind = "storage_state"
	SessionDevice       SessionKind = "device"
)

// Session is a ready-to-use, authenticated session for a connector. Token is
// the credential to attach (bearer access token for SessionBearer). The
// StorageStateRef / DeviceID fields are placeholders for the redroid/web
// handlers that arrive in later slices — unused by oauth_code.
type Session struct {
	Kind            SessionKind
	Token           string
	Expiry          time.Time
	StorageStateRef string
	DeviceID        string
}

// AuthMethod acquires/maintains a session for connectors it handles.
//
// Ensure returns a valid session, acquiring or refreshing credentials as
// needed. It MUST NOT block on a human in this slice; future handlers that
// require a human factor will instead return a sentinel the broker turns into a
// PendingGate (M-G3) — the interface stays the same.
//
// NeedsHuman reports whether a human gate MAY fire for this connector on a cold
// path (first login, re-consent). The router uses this to decide whether to
// surface a "may pause for your approval" hint. oauth_code returns false: it
// only refreshes silently here; revocation surfaces as a clean error the caller
// can turn into a re-consent prompt.
type AuthMethod interface {
	Method() string
	Ensure(ctx context.Context, connector *Connector) (Session, error)
	NeedsHuman(connector *Connector) bool
}

// broker dispatches to the right AuthMethod by connector.Auth.Method.
type broker struct {
	handlers map[string]AuthMethod
}

// newBroker wires the handlers available in this slice. Pass the CredStore the
// handlers persist tokens through (vault-backed in production, in-memory in
// tests).
func newBroker(store CredStore) *broker {
	b := &broker{handlers: map[string]AuthMethod{}}
	b.register(newOAuthCodeHandler(store))
	// password_totp (redroid device-as-2FA, gateway_redroid.go). The production
	// device driver picks the first attached/emulated device lazily; the gate
	// store is the process-wide gatewayGates (notifies the user's own phone).
	b.register(newPasswordTotpHandler(store, &redroidDeviceDriver{serial: droidPickDevice()}, gatewayGates))
	return b
}

func (b *broker) register(h AuthMethod) {
	b.handlers[h.Method()] = h
}

// handlerFor returns the AuthMethod for a connector, or an error if its method
// is unknown in this slice.
func (b *broker) handlerFor(c *Connector) (AuthMethod, error) {
	m := c.Auth.Method
	h, ok := b.handlers[m]
	if !ok {
		return nil, fmt.Errorf("gateway: no auth handler for method %q (connector %q) — only oauth_code is implemented in this slice", m, c.ID)
	}
	return h, nil
}

// Ensure returns a valid session for the connector, selecting + delegating to
// the appropriate handler.
func (b *broker) Ensure(ctx context.Context, c *Connector) (Session, error) {
	h, err := b.handlerFor(c)
	if err != nil {
		return Session{}, err
	}
	return h.Ensure(ctx, c)
}

// Refresher is optionally implemented by auth methods that can force a
// credential refresh even when the current credential is still valid by clock —
// used on a 401 from a resource server (token rejected/rotated server-side).
type Refresher interface {
	Refresh(ctx context.Context, connector *Connector) (Session, error)
}

// Refresh forces a credential refresh for the connector when the handler
// supports it (oauth_code); otherwise it falls back to Ensure. Used by the
// 401-retry path so a still-clock-valid-but-rejected token gets exchanged
// exactly once rather than re-handed unchanged.
func (b *broker) Refresh(ctx context.Context, c *Connector) (Session, error) {
	h, err := b.handlerFor(c)
	if err != nil {
		return Session{}, err
	}
	if r, ok := h.(Refresher); ok {
		return r.Refresh(ctx, c)
	}
	return h.Ensure(ctx, c)
}

// NeedsHuman reports whether acquiring a session for the connector may require a
// human gate. Unknown method ⇒ false (the Ensure call will return the routable
// "no handler" error instead).
func (b *broker) NeedsHuman(c *Connector) bool {
	h, err := b.handlerFor(c)
	if err != nil {
		return false
	}
	return h.NeedsHuman(c)
}
