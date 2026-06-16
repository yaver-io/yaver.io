package main

// gateway_connect.go — connector AUTHORING for engine "api" / auth "oauth_code".
//
// This is the missing front door: the runtime broker (gateway_oauth.go) can
// already refresh tokens and run reads, but only AFTER a connector exists and
// has consented credentials in the vault. This file builds that one-time path:
//
//	1. gatewayConnectStart(conn) — generate PKCE + state, build the provider
//	   authorization URL, and start a local loopback callback listener that
//	   captures the redirected code+state. A manual/paste-code fallback (for
//	   headless boxes with no browser) is supported by simply not waiting on the
//	   listener and letting the caller submit the code out-of-band.
//	2. gatewayConnectFinish(conn, code, verifier, secret, store) — exchange the
//	   code for tokens at the provider token endpoint (via the SHARED
//	   oauthCodeHandler.exchangeToken primitive — no divergent duplicate), persist
//	   them to the vault under conn.Auth.CredRef, and register the manifest.
//
// PRIVACY / POLICY (CLAUDE.md, docs §9):
//   - The client SECRET and the tokens live ONLY in the vault (CredStore). The
//     manifest persisted to disk carries the public clientId + URLs + scopes, and
//     NEVER a secret or a token. validateConnectorManifest enforces credRef is a
//     vault key, not an inline secret.
//   - Honest User-Agent on the token exchange (gatewayContactUA) — no browser
//     spoof, inherited from postToken.
//   - READ-ONLY slice: only GET capabilities may be authored here. The capability
//     spec is validated through validateConnectorManifest, which rejects any
//     non-read verb / non-GET method.
//
// SLICE SCOPE: engine "api" + method "oauth_code" only. redroid/web authoring
// (flow recording) and ACT capabilities are out of scope (docs §6/§7).

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// gatewayConnectCallbackPath is the loopback redirect path the consent flow
// listens on. The full redirect URI is http://127.0.0.1:<port><path>.
const gatewayConnectCallbackPath = "/gateway/oauth/callback"

// gatewayConnectTimeout bounds how long the loopback listener waits for the
// browser redirect before giving up (the user can still paste the code).
const gatewayConnectTimeout = 5 * time.Minute

// pendingConnect holds the in-flight state for one consent attempt: the PKCE
// verifier + state to validate the callback against, the connector being
// authored, the client secret (kept in memory only until finish persists it to
// the vault), and the loopback listener/result channel.
type pendingConnect struct {
	conn         *Connector
	pkce         PKCE
	state        string
	redirectURI  string
	clientSecret string

	listener net.Listener
	result   chan connectCallback
}

// connectCallback is the (code, error) captured from the loopback redirect.
type connectCallback struct {
	code string
	err  error
}

// newConnectState returns a high-entropy URL-safe state token for CSRF binding
// of the authorization request to its callback.
func newConnectState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("gateway: connect state entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// gatewayConnectStart generates PKCE + state, starts a loopback callback
// listener on an ephemeral port, and builds the authorization URL the user
// opens in a browser. The returned *pendingConnect carries everything
// gatewayConnectFinish needs; the caller either waits on (*pendingConnect).wait
// for the loopback redirect, or finishes with a pasted code (headless fallback).
//
// On a box with no browser, the loopback listener still binds (harmless) and the
// caller ignores it, completing via the paste-code path instead.
func gatewayConnectStart(conn *Connector) (authURL string, pc *pendingConnect, err error) {
	if conn == nil {
		return "", nil, fmt.Errorf("gateway: connect requires a connector")
	}
	if conn.Auth.Method != "oauth_code" {
		return "", nil, fmt.Errorf("gateway: connect supports only oauth_code (got %q)", conn.Auth.Method)
	}
	if strings.TrimSpace(conn.Auth.AuthURL) == "" || strings.TrimSpace(conn.Auth.TokenURL) == "" {
		return "", nil, fmt.Errorf("gateway: connector %q needs both authUrl and tokenUrl", conn.ID)
	}

	pkce, err := newPKCE()
	if err != nil {
		return "", nil, err
	}
	state, err := newConnectState()
	if err != nil {
		return "", nil, err
	}

	// Bind an ephemeral loopback port for the redirect. 127.0.0.1 only — never a
	// routable interface; this listener exists solely to catch the browser's
	// redirect on the same machine.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("gateway: bind loopback callback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, gatewayConnectCallbackPath)

	pc = &pendingConnect{
		conn:        conn,
		pkce:        pkce,
		state:       state,
		redirectURI: redirectURI,
		listener:    ln,
		result:      make(chan connectCallback, 1),
	}

	h := newOAuthCodeHandler(nil) // URL building needs no store
	authURL, err = h.AuthCodeURL(conn, redirectURI, state, pkce)
	if err != nil {
		ln.Close()
		return "", nil, err
	}

	pc.serve()
	return authURL, pc, nil
}

// serve runs the loopback HTTP server that captures the OAuth redirect. It
// validates state, extracts code (or an error param), pushes the result, and
// shows the user a terse "you can close this tab" page.
func (pc *pendingConnect) serve() {
	mux := http.NewServeMux()
	mux.HandleFunc(gatewayConnectCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			pc.deliver(connectCallback{err: fmt.Errorf("gateway: provider returned error %q: %s", e, q.Get("error_description"))})
			writeConnectPage(w, "Authorization failed. You can close this tab and return to Yaver.")
			return
		}
		if q.Get("state") != pc.state {
			// State mismatch — possible CSRF / a stale redirect. Reject; do NOT
			// deliver a result so a forged callback can't satisfy the consent.
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			pc.deliver(connectCallback{err: fmt.Errorf("gateway: callback had no code")})
			writeConnectPage(w, "No authorization code received. You can close this tab.")
			return
		}
		pc.deliver(connectCallback{code: code})
		writeConnectPage(w, "Connected. You can close this tab and return to Yaver.")
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(pc.listener) }()
}

// deliver pushes a callback result exactly once (the channel is buffered with
// cap 1; a second delivery is dropped).
func (pc *pendingConnect) deliver(cb connectCallback) {
	select {
	case pc.result <- cb:
	default:
	}
}

// wait blocks for the browser redirect (or ctx/timeout) and returns the captured
// authorization code. Used by the loopback path; the paste-code path skips it.
func (pc *pendingConnect) wait(ctx context.Context) (string, error) {
	timer := time.NewTimer(gatewayConnectTimeout)
	defer timer.Stop()
	select {
	case cb := <-pc.result:
		if cb.err != nil {
			return "", cb.err
		}
		return cb.code, nil
	case <-timer.C:
		return "", fmt.Errorf("gateway: timed out waiting for browser consent (paste the code manually instead)")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// close releases the loopback listener. Safe to call multiple times.
func (pc *pendingConnect) close() {
	if pc != nil && pc.listener != nil {
		_ = pc.listener.Close()
	}
}

// writeConnectPage renders a minimal close-this-tab page for the redirect.
func writeConnectPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><body style=\"font-family:system-ui;padding:2rem\"><h2>Yaver</h2><p>" + msg + "</p></body></html>"))
}

// gatewayConnectFinish completes the consent: it exchanges the authorization
// code for tokens at the provider token endpoint (reusing the SHARED
// exchangeToken primitive), persists those tokens to the vault under the
// connector's CredRef, and registers the manifest in the registry.
//
// The client secret (if any) is persisted to the vault too — under a derived
// "<credRef>/secret" ref — so a confidential-client refresh can find it later;
// it NEVER goes into the manifest. On success the connector is wired and
// gateway_query can call its read capabilities.
func gatewayConnectFinish(ctx context.Context, conn *Connector, code, codeVerifier, clientSecret, redirectURI string, store CredStore, reg *ConnectorRegistry) error {
	if conn == nil {
		return fmt.Errorf("gateway: finish requires a connector")
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("gateway: finish requires an authorization code")
	}
	if store == nil || reg == nil {
		return fmt.Errorf("gateway: finish requires a cred store and registry")
	}

	// Validate the manifest BEFORE doing any network work — this enforces the
	// public-safe invariants (engine api, read-only GET caps, credRef is a vault
	// key, no inline secret) so a bad manifest never reaches the provider.
	if err := validateConnectorManifest(*conn); err != nil {
		return err
	}

	h := newOAuthCodeHandler(store)
	creds, err := h.exchangeToken(ctx, conn, code, redirectURI, codeVerifier, clientSecret)
	if err != nil {
		return err
	}

	// Persist the tokens to the vault (CredStore), never to the manifest.
	if err := saveOAuthCreds(store, conn.Auth.CredRef, creds); err != nil {
		return fmt.Errorf("gateway: persist consent creds: %w", err)
	}

	// Persist a confidential-client secret to the vault too (separate ref), so a
	// later refresh of a confidential client can supply it. Public clients (PKCE,
	// no secret) skip this entirely.
	if strings.TrimSpace(clientSecret) != "" {
		if err := saveClientSecret(store, conn.Auth.CredRef, clientSecret); err != nil {
			return fmt.Errorf("gateway: persist client secret: %w", err)
		}
	}

	// Register the manifest last — only after creds are safely stored, so a
	// listed connector always has its credentials.
	if err := reg.Store(*conn); err != nil {
		return fmt.Errorf("gateway: register connector: %w", err)
	}
	return nil
}

// clientSecretRef derives the vault ref that holds a connector's confidential
// client secret from its OAuth CredRef. Kept distinct from the tokens blob so
// neither overwrites the other.
func clientSecretRef(credRef string) string {
	ref := strings.TrimRight(strings.TrimSpace(credRef), "/")
	if ref == "" {
		return ""
	}
	return ref + "/secret"
}

// saveClientSecret stores the confidential-client secret in the vault under the
// derived ref. Stored raw (the vault encrypts at rest); never returned in any
// listing.
func saveClientSecret(store CredStore, credRef, secret string) error {
	ref := clientSecretRef(credRef)
	if ref == "" {
		return fmt.Errorf("gateway: cannot derive secret ref from empty credRef")
	}
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return err
	}
	return store.SetCreds(project, name, []byte(secret))
}
