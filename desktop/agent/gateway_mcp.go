package main

// gateway_mcp.go — MCP exposure of the Personal Agent Gateway.
//
// Slice 1 surfaces a single dispatcher tool, gateway_query{connector,
// capability, params}, which runs one READ capability against one connector and
// returns its structured answer (projected to the capability's answerSchema).
// This is "your apps as tools": the host AI (Claude Code, the in-car voice
// surface) calls gateway_query to read state from any credentialed service you
// have wired up — no per-app integration in the AI.
//
// The dynamic per-capability tool registration (gw_<connector>_<capability>) is
// the Slice-1b stretch (docs §1.6) and is intentionally NOT built here.
//
// READ-ONLY: gateway_query can only invoke read capabilities. There is no
// write/ACT verb in this slice.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// mcpGatewayQuery is the gateway_query MCP tool entrypoint. It resolves the
// production gateway (on-disk registry + vault-backed broker), runs the
// capability, and returns a structured result. Credential acquisition/refresh
// happens inside gatewayInvoke via the broker.
func mcpGatewayQuery(connector, capability string, params map[string]string) interface{} {
	if connector == "" || capability == "" {
		return map[string]interface{}{"error": "connector and capability are required"}
	}
	deps, err := newGatewayDeps()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := deps.gatewayInvoke(ctx, connector, capability, params)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

// mcpGatewayCapabilities lists the read capabilities available across all wired
// connectors — useful for a host AI to discover what gateway_query can call.
// (Not registered as its own MCP tool in this minimal slice, but available for
// the dispatch layer / future per-capability registration.)
func mcpGatewayCapabilities() interface{} {
	reg, err := NewConnectorRegistry()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	caps, err := reg.CapabilitiesForMCP()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"count":        len(caps),
		"capabilities": caps,
		"note":         "Read-only. Call one via gateway_query{connector, capability, params}.",
	}
}

// ── gateway_connectors / gateway_capabilities (listing, NO secrets) ──────────

// mcpGatewayConnectors lists registered connectors with only public metadata —
// id, engine, auth method, and the read capability ids. It NEVER exposes a
// token, client secret, or credRef value (the manifest stores none inline; we
// also do not echo CredRef to avoid leaking the vault key shape).
func mcpGatewayConnectors() interface{} {
	reg, err := NewConnectorRegistry()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	connectors, err := reg.List()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	out := make([]map[string]interface{}, 0, len(connectors))
	for _, c := range connectors {
		capIDs := make([]string, 0, len(c.Capabilities))
		for _, cap := range c.Capabilities {
			capIDs = append(capIDs, cap.ID)
		}
		out = append(out, map[string]interface{}{
			"id":           c.ID,
			"engine":       c.Engine,
			"method":       c.Auth.Method,
			"capabilities": capIDs,
		})
	}
	return map[string]interface{}{
		"count":      len(out),
		"connectors": out,
		"note":       "Public metadata only — no tokens or secrets. Credentials live in your vault.",
	}
}

// mcpGatewayConnectorCapabilities lists one connector's read capabilities with
// the shape a host AI needs to call gateway_query: id, verb, params (placeholders
// in the flow path), and answerSchema. No secrets.
func mcpGatewayConnectorCapabilities(connector string) interface{} {
	if connector == "" {
		return map[string]interface{}{"error": "connector is required"}
	}
	reg, err := NewConnectorRegistry()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	c, err := reg.Get(connector)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	out := make([]map[string]interface{}, 0, len(c.Capabilities))
	for _, cap := range c.Capabilities {
		out = append(out, map[string]interface{}{
			"id":           cap.ID,
			"verb":         cap.Verb,
			"risk":         cap.Risk,
			"title":        cap.Title,
			"description":  cap.Description,
			"params":       flowParams(cap.Flow.Path),
			"answerSchema": cap.AnswerSchema,
		})
	}
	return map[string]interface{}{
		"connector":    c.ID,
		"count":        len(out),
		"capabilities": out,
	}
}

// flowParams extracts the {placeholder} names from a capability flow path so a
// caller knows which params gateway_query expects. The built-in {now} is omitted
// (the engine fills it automatically).
func flowParams(path string) []string {
	var params []string
	seen := map[string]bool{}
	for {
		i := indexByte(path, '{')
		if i < 0 {
			break
		}
		j := indexByte(path[i:], '}')
		if j < 0 {
			break
		}
		name := path[i+1 : i+j]
		path = path[i+j+1:]
		if name == "" || name == "now" || seen[name] {
			continue
		}
		seen[name] = true
		params = append(params, name)
	}
	return params
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// ── gateway_connect (author an OAuth connector) ──────────────────────────────

// pendingConnectStore holds in-flight consent attempts between the start call
// (returns auth URL + connect id) and the finish call (callback/paste delivers
// the code). Keyed by an opaque connect id. Process-wide; entries are removed on
// finish or eviction. The client secret is held ONLY here in memory (never on
// disk) until finish persists it to the vault.
type pendingConnectStore struct {
	mu sync.Mutex
	m  map[string]*pendingConnect
}

var gatewayPendingConnects = &pendingConnectStore{m: map[string]*pendingConnect{}}

func (s *pendingConnectStore) put(id string, pc *pendingConnect) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = pc
}

func (s *pendingConnectStore) take(id string) (*pendingConnect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc, ok := s.m[id]
	if ok {
		delete(s.m, id)
	}
	return pc, ok
}

func newConnectID() string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return "conn_" + base64.RawURLEncoding.EncodeToString(raw)
}

// mcpGatewayConnect authors an engine:"api" / auth:"oauth_code" connector. It
// builds the manifest from the inputs, starts the PKCE consent flow (loopback
// listener + auth URL), and returns the auth URL + a connect id. The host opens
// the URL; the loopback callback (or a pasted code via gateway_connect_finish)
// completes the exchange and registers the connector.
//
// The client secret is taken as input but NEVER written to the manifest — it is
// held in memory until finish persists it to the vault (CredStore).
func mcpGatewayConnect(id, surface, authURL, tokenURL string, scopes []string, clientID, clientSecret string, capabilities []Capability) interface{} {
	if id == "" || surface == "" || authURL == "" || tokenURL == "" {
		return map[string]interface{}{"error": "id, surface, authUrl and tokenUrl are required"}
	}
	if len(capabilities) == 0 {
		return map[string]interface{}{"error": "at least one read capability is required"}
	}
	credRef := "gateway/" + id + "/oauth"
	conn := &Connector{
		ID:      id,
		Engine:  "api",
		Surface: surface,
		Auth: ConnectorAuth{
			Method:   "oauth_code",
			AuthURL:  authURL,
			TokenURL: tokenURL,
			ClientID: clientID,
			Scopes:   scopes,
			CredRef:  credRef,
		},
		Capabilities: capabilities,
	}

	// Validate the manifest up front — rejects an inline secret, a non-api
	// engine, or a non-read/non-GET capability before any network work.
	if err := validateConnectorManifest(*conn); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	authConsentURL, pc, err := gatewayConnectStart(conn)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	pc.clientSecret = clientSecret

	connectID := newConnectID()
	gatewayPendingConnects.put(connectID, pc)

	return map[string]interface{}{
		"connect_id": connectID,
		"auth_url":   authConsentURL,
		"next":       "Open auth_url in a browser to consent. On the same machine the loopback callback finishes automatically; otherwise paste the returned code via gateway_connect_finish{connect_id, code}.",
		"note":       "Client secret (if any) is held in memory and will be stored in your vault on finish — never in the manifest.",
	}
}

// mcpGatewayConnectFinish completes a pending consent: it waits for the loopback
// callback (when code is empty) or uses a pasted code, exchanges it for tokens,
// stores them in the vault, and registers the connector.
func mcpGatewayConnectFinish(connectID, code string) interface{} {
	if connectID == "" {
		return map[string]interface{}{"error": "connect_id is required"}
	}
	pc, ok := gatewayPendingConnects.take(connectID)
	if !ok {
		return map[string]interface{}{"error": "unknown or already-completed connect_id"}
	}
	defer pc.close()

	ctx, cancel := context.WithTimeout(context.Background(), gatewayConnectTimeout)
	defer cancel()

	// No pasted code → wait for the loopback redirect to deliver one.
	if code == "" {
		c, err := pc.wait(ctx)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
		code = c
	}

	store, err := newVaultCredStore()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	reg, err := NewConnectorRegistry()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := gatewayConnectFinish(ctx, pc.conn, code, pc.pkce.Verifier, pc.clientSecret, pc.redirectURI, store, reg); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"connector": pc.conn.ID,
		"status":    "connected",
		"note":      "Tokens stored in your vault. Call its read capabilities via gateway_query.",
	}
}
