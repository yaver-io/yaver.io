package main

// gateway_registry.go — the Personal Agent Gateway connector registry.
//
// A *connector* is a generic, public-safe description of one credentialed
// app/service the user already uses (Google, a broker, an EV network, …) and
// the read capabilities Yaver can perform against it AS the user. The manifest
// is generic and open; the actual credentials it references live ONLY in the
// vault (project = "gateway"), referenced by key — NEVER inline in the manifest
// and NEVER in Convex.
//
// Manifests are plain JSON on local disk at ~/.yaver/connectors/<id>.json. The
// registry loads them on demand and exposes Get(id), List(), and
// CapabilitiesForMCP() so the gateway_query MCP tool can surface "your apps as
// tools".
//
// SLICE SCOPE: READ-ONLY. Capabilities here describe GET verbs only. There is
// no write/ACT path in this slice — the ACT consent model (dry-run + confirm +
// audit) is specced in docs/yaver-personal-agent-gateway.md §16 and built in a
// later slice. The Verb field accommodates "add"/"update"/"delete" for forward
// compatibility, but gatewayInvoke rejects anything that is not a read here.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConnectorAuth describes how the broker acquires a session for a connector.
// Secrets are NEVER inline: CredRef points at a vault key (project "gateway").
type ConnectorAuth struct {
	// Method selects the broker handler: "oauth_code" in this slice. Future:
	// "password_totp" (redroid), "device_grant", "open_banking", "sms", etc.
	Method string `json:"method"`

	// OAuth2 authorization-code + PKCE fields (Method == "oauth_code").
	AuthURL  string   `json:"authUrl,omitempty"`
	TokenURL string   `json:"tokenUrl,omitempty"`
	ClientID string   `json:"clientId,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`

	// CredRef is the vault key under project "gateway" that holds this
	// connector's persisted credentials (e.g. "gateway/google/oauth"). The
	// CredStore resolves it to a project/name pair. Required for any method
	// that persists tokens. NEVER an inline secret.
	CredRef string `json:"credRef,omitempty"`

	// ── password_totp (redroid device-as-2FA) fields ─────────────────────────
	// Forward-shape for the M-G4 passwordTotpHandler (gateway_redroid.go). All
	// reference vault keys / package ids — NEVER an inline secret.

	// Mechanism selects how the broker satisfies the connector's 2FA step:
	// "totp_seed" | "authenticator_app" | "push_to_app" | "sms" | "passkey".
	// Empty ⇒ no second factor (password-only).
	Mechanism string `json:"mechanism,omitempty"`
	// LoginRef is the vault key holding the connector's login credentials
	// ({username, password}) the handler types into the app.
	LoginRef string `json:"loginRef,omitempty"`
	// TotpRef is the vault key holding the base32 TOTP seed Yaver owns (used
	// when Mechanism == "totp_seed" — Yaver-as-authenticator).
	TotpRef string `json:"totpRef,omitempty"`
	// DeviceRef is the vault key holding the redroid golden-snapshot reference
	// ({instanceId, snapshotId}) for this connector's trusted device.
	DeviceRef string `json:"deviceRef,omitempty"`
}

// CapabilityFlow describes how a capability is executed. The "api" engine uses a
// single authed HTTP GET (Method/Path). The "redroid" engine drives an app:
// launch a package, run a short ordered list of UI Steps, then read the screen
// (see gateway_redroid_invoke.go and docs §3-§5). All redroid fields are
// OPTIONAL and additive — an "api" manifest never sets them.
type CapabilityFlow struct {
	Type   string `json:"type"`             // "api" | "redroid"
	Method string `json:"method,omitempty"` // HTTP method for type "api" — GET for reads; POST/PUT/PATCH/DELETE for acts
	Path   string `json:"path,omitempty"`   // path appended to connector.Surface, with {param} placeholders

	// Body is the JSON request-body template for an ACT capability on the "api"
	// engine (POST/PUT/PATCH). {param} placeholders are substituted exactly like
	// Path. It is a TEMPLATE only and NEVER carries a secret — credentials live in
	// the vault and are attached as the bearer session, not the body. Empty for
	// reads and for body-less acts (DELETE).
	Body string `json:"body,omitempty"`

	// ── redroid engine fields (type == "redroid") ───────────────────────────
	// LaunchPkg is the Android package to open (defaults to connector.Surface,
	// which for redroid connectors is the package id, when empty).
	LaunchPkg string `json:"launchPkg,omitempty"`
	// Steps is the ordered UI flow to reach the answer screen. Each step is
	// re-observed + verified after execution (advance/needs-heal) per docs §4.
	Steps []FlowStep `json:"steps,omitempty"`

	// Version is the self-heal revision of this flow (M-G7). It starts unset (0)
	// and the selector-heal path bumps it each time it rewrites a step's
	// Target/ExpectSignature, so a rewritten flow is versioned + auditable
	// (docs §8: "all changes versioned, reversible, audited"). omitempty so
	// existing manifests that never healed are byte-identical on disk.
	Version int `json:"version,omitempty"`
}

// FlowStep is one UI action in a redroid Flow. action ∈ {tap, type, wait};
// target is a generic hint (resourceId / text / contentDesc) the driver/extractor
// resolves; text is the literal to type for action "type". expectSignature, when
// set, is the ScreenSignature the step should advance TO — a mismatch is the
// self-heal trigger (docs §3/§4). This slice records the mismatch and continues
// best-effort (the curator that rewrites a stale step lands in M-G6).
type FlowStep struct {
	Action          string `json:"action"`                    // "tap" | "type" | "wait"
	Target          string `json:"target,omitempty"`          // resourceId / text / contentDesc hint
	Text            string `json:"text,omitempty"`            // literal for action "type" ({param} substituted)
	ExpectSignature string `json:"expectSignature,omitempty"` // ScreenSignature expected after this step
}

// Capability is one read operation a connector advertises — the "tool" shape.
// answerSchema is the KEY artifact: it declares which fields to project out of
// the raw response (deterministic dotted-path mapping in this slice; an AI
// extraction hook plugs in later — see gateway_invoke.go).
type Capability struct {
	ID           string            `json:"id"`
	Verb         string            `json:"verb"` // "get" in this read-only slice
	Risk         string            `json:"risk"` // "read" in this slice
	Title        string            `json:"title,omitempty"`
	Description  string            `json:"description,omitempty"`
	Flow         CapabilityFlow    `json:"flow"`
	AnswerSchema map[string]string `json:"answerSchema,omitempty"` // outKey -> "type" or "dotted.path:type"
}

// Connector is a manifest for one credentialed service. Generic + open; creds
// referenced by vault key only.
type Connector struct {
	ID           string        `json:"id"`
	Engine       string        `json:"engine"`  // "api" in this slice
	Surface      string        `json:"surface"` // API base URL (engine "api")
	Auth         ConnectorAuth `json:"auth"`
	Capabilities []Capability  `json:"capabilities"`
}

// Capability looks up a capability by id on the connector.
func (c *Connector) Capability(id string) (*Capability, bool) {
	for i := range c.Capabilities {
		if c.Capabilities[i].ID == id {
			return &c.Capabilities[i], true
		}
	}
	return nil, false
}

// gatewayConnectorsDir is ~/.yaver/connectors — where manifests live.
func gatewayConnectorsDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connectors"), nil
}

// ConnectorRegistry loads/stores connector manifests as JSON on local disk.
// It is stateless beyond the directory path — Get/List read fresh from disk so
// a manifest edited on disk (or by the authoring funnel later) is picked up
// without a restart, matching the spec's "loads at startup + on change".
type ConnectorRegistry struct {
	dir string
}

// NewConnectorRegistry returns a registry rooted at ~/.yaver/connectors,
// creating the directory if needed.
func NewConnectorRegistry() (*ConnectorRegistry, error) {
	dir, err := gatewayConnectorsDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create connectors dir: %w", err)
	}
	return &ConnectorRegistry{dir: dir}, nil
}

// newConnectorRegistryAt is a test/seam helper that roots the registry at an
// explicit directory (no ConfigDir, no keychain).
func newConnectorRegistryAt(dir string) (*ConnectorRegistry, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create connectors dir: %w", err)
	}
	return &ConnectorRegistry{dir: dir}, nil
}

// pathFor returns the on-disk path for a connector id, validating the id so it
// can never escape the connectors directory.
func (r *ConnectorRegistry) pathFor(id string) (string, error) {
	if err := validateConnectorID(id); err != nil {
		return "", err
	}
	return filepath.Join(r.dir, id+".json"), nil
}

// validateConnectorID keeps ids filesystem-safe (no path separators / traversal).
func validateConnectorID(id string) error {
	if id == "" {
		return fmt.Errorf("connector id cannot be empty")
	}
	if len(id) > 128 {
		return fmt.Errorf("connector id too long (max 128)")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.'
		if !ok {
			return fmt.Errorf("connector id %q contains invalid character %q (allowed: A-Z a-z 0-9 _ - .)", id, c)
		}
	}
	if id == "." || id == ".." {
		return fmt.Errorf("connector id %q is reserved", id)
	}
	return nil
}

// Store validates and persists a connector manifest to disk. It refuses any
// manifest that inlines a secret in CredRef (an extra belt-and-suspenders
// check on top of the schema — CredRef must look like a vault key, not a token).
func (r *ConnectorRegistry) Store(c Connector) error {
	if err := validateConnectorID(c.ID); err != nil {
		return err
	}
	if err := validateConnectorManifest(c); err != nil {
		return err
	}
	path, err := r.pathFor(c.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal connector: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write connector tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename connector: %w", err)
	}
	return nil
}

// Get loads a single connector manifest by id.
func (r *ConnectorRegistry) Get(id string) (*Connector, error) {
	path, err := r.pathFor(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("connector %q not found", id)
		}
		return nil, fmt.Errorf("read connector %q: %w", id, err)
	}
	var c Connector
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse connector %q: %w", id, err)
	}
	return &c, nil
}

// List loads every connector manifest in the directory, sorted by id. A single
// unparseable file is skipped (logged by the caller if desired), never fatal.
func (r *ConnectorRegistry) List() ([]Connector, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read connectors dir: %w", err)
	}
	out := make([]Connector, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		c, err := r.Get(id)
		if err != nil {
			continue // skip a corrupt manifest rather than fail the whole list
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// MCPCapability is the flattened (connector, capability) descriptor surfaced to
// MCP consumers — "your apps as tools".
type MCPCapability struct {
	Connector    string            `json:"connector"`
	Capability   string            `json:"capability"`
	Verb         string            `json:"verb"`
	Risk         string            `json:"risk"`
	Title        string            `json:"title,omitempty"`
	Description  string            `json:"description,omitempty"`
	AnswerSchema map[string]string `json:"answerSchema,omitempty"`
}

// CapabilitiesForMCP returns the flattened capability list across all
// connectors so a host AI can see which (connector, capability) pairs it may
// call via gateway_query. Read-only slice ⇒ only read capabilities are
// surfaced.
func (r *ConnectorRegistry) CapabilitiesForMCP() ([]MCPCapability, error) {
	connectors, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]MCPCapability, 0)
	for _, c := range connectors {
		for _, cap := range c.Capabilities {
			if !isReadVerb(cap.Verb) {
				continue // slice exposes reads only
			}
			out = append(out, MCPCapability{
				Connector:    c.ID,
				Capability:   cap.ID,
				Verb:         cap.Verb,
				Risk:         cap.Risk,
				Title:        cap.Title,
				Description:  cap.Description,
				AnswerSchema: cap.AnswerSchema,
			})
		}
	}
	return out, nil
}

// isReadVerb reports whether a capability verb is a read (GET-equivalent). The
// gateway slice only ever executes reads; everything else is rejected until the
// ACT consent model lands.
func isReadVerb(verb string) bool {
	switch strings.ToLower(strings.TrimSpace(verb)) {
	case "get", "read", "":
		// "" tolerated for terse manifests; treated as read.
		return true
	default:
		return false
	}
}

// validateConnectorManifest enforces the public-safe invariants: no inline
// secrets, a supported engine, and a well-shaped flow per capability. READ
// capabilities must be non-mutating (GET / observe). ACT capabilities (M-G7,
// gateway_act.go) are permitted but must be MUTATING and explicitly risk-tiered,
// so the consent wrapper can require the right confirmation — an act verb paired
// with a GET method (a contradiction) is rejected loudly.
func validateConnectorManifest(c Connector) error {
	if strings.TrimSpace(c.Engine) == "" {
		return fmt.Errorf("connector %q: engine is required", c.ID)
	}
	switch c.Engine {
	case "api", "redroid":
		// supported engines
	default:
		return fmt.Errorf("connector %q: engine %q not supported (supported: \"api\", \"redroid\")", c.ID, c.Engine)
	}
	if err := validateCredRef(c.Auth.CredRef); err != nil {
		return fmt.Errorf("connector %q auth: %w", c.ID, err)
	}
	if len(c.Capabilities) == 0 {
		return fmt.Errorf("connector %q: at least one capability is required", c.ID)
	}
	for _, cap := range c.Capabilities {
		if cap.ID == "" {
			return fmt.Errorf("connector %q: capability id is required", c.ID)
		}
		if err := validateCapabilityFlow(c, cap); err != nil {
			return err
		}
	}
	return nil
}

// validateCapabilityFlow validates one capability's flow against its engine and
// whether it is a read or an act.
func validateCapabilityFlow(c Connector, cap Capability) error {
	act := !isReadVerb(cap.Verb)
	ft := strings.TrimSpace(cap.Flow.Type)
	switch c.Engine {
	case "api":
		if ft != "api" {
			return fmt.Errorf("connector %q capability %q: engine \"api\" requires flow type \"api\" (got %q)", c.ID, cap.ID, ft)
		}
		if strings.TrimSpace(cap.Flow.Path) == "" {
			return fmt.Errorf("connector %q capability %q: flow.path is required", c.ID, cap.ID)
		}
		method := strings.ToUpper(strings.TrimSpace(cap.Flow.Method))
		if act {
			switch method {
			case "POST", "PUT", "PATCH", "DELETE":
				// mutating — good
			default:
				return fmt.Errorf("connector %q capability %q: an act verb (%q) requires a mutating method (POST/PUT/PATCH/DELETE), got %q", c.ID, cap.ID, cap.Verb, cap.Flow.Method)
			}
			if err := validateActRisk(c, cap); err != nil {
				return err
			}
		} else if method != "" && method != "GET" {
			return fmt.Errorf("connector %q capability %q: a read verb requires GET, got %q", c.ID, cap.ID, cap.Flow.Method)
		}
	case "redroid":
		if ft != "redroid" {
			return fmt.Errorf("connector %q capability %q: engine \"redroid\" requires flow type \"redroid\" (got %q)", c.ID, cap.ID, ft)
		}
		if act {
			if len(cap.Flow.Steps) == 0 {
				return fmt.Errorf("connector %q capability %q: an act verb requires at least one flow step", c.ID, cap.ID)
			}
			if err := validateActRisk(c, cap); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateActRisk requires an act capability to declare a recognized risk tier
// so the consent wrapper can pick the right gate (a voice "yes" for a low,
// reversible act; a tapped second-key for a high/financial one).
func validateActRisk(c Connector, cap Capability) error {
	switch gatewayRiskTier(cap.Risk) {
	case riskLow, riskHigh, riskFinancial:
		return nil
	default:
		return fmt.Errorf("connector %q capability %q: an act capability must declare risk (\"low\", \"high\", or \"financial\"), got %q", c.ID, cap.ID, cap.Risk)
	}
}

// riskTier ranks how much confirmation an act needs.
type riskTier int

const (
	riskUnknown riskTier = iota
	riskRead
	riskLow       // reversible, low-stakes — a voice "yes" may confirm
	riskHigh      // irreversible / high-stakes — second-key tap required
	riskFinancial // moves money — second-key tap required, never voice-alone
)

// gatewayRiskTier maps a manifest risk string onto a tier.
func gatewayRiskTier(s string) riskTier {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read", "observe":
		return riskRead
	case "low", "reversible", "safe":
		return riskLow
	case "high", "irreversible", "destructive":
		return riskHigh
	case "financial", "payment", "money", "purchase":
		return riskFinancial
	default:
		return riskUnknown
	}
}

// ActCapabilitiesForMCP returns the flattened ACT (write) capability list across
// all connectors, so the intent router / a host AI can see which actions are
// available and their risk tier. Distinct from CapabilitiesForMCP (reads).
func (r *ConnectorRegistry) ActCapabilitiesForMCP() ([]MCPCapability, error) {
	connectors, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]MCPCapability, 0)
	for _, c := range connectors {
		for _, cap := range c.Capabilities {
			if isReadVerb(cap.Verb) {
				continue
			}
			out = append(out, MCPCapability{
				Connector:    c.ID,
				Capability:   cap.ID,
				Verb:         cap.Verb,
				Risk:         cap.Risk,
				Title:        cap.Title,
				Description:  cap.Description,
				AnswerSchema: cap.AnswerSchema,
			})
		}
	}
	return out, nil
}

// validateCredRef ensures a credential reference looks like a vault key, never
// an inline secret. A vault key is "gateway/<connector>/<name>" or just a name;
// it must not contain whitespace and must not look like a JWT / long token.
func validateCredRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil // a manifest may legitimately reference no creds (public API)
	}
	if strings.ContainsAny(ref, " \t\n\r") {
		return fmt.Errorf("credRef must be a vault key, not an inline secret")
	}
	// A bearer/JWT token would be long and base64ish with dots; a vault key is
	// short and slash-separated. Reject the obvious inline-secret shape.
	if len(ref) > 200 || strings.Count(ref, ".") >= 2 {
		return fmt.Errorf("credRef looks like an inline secret — store it in the vault and reference its key")
	}
	return nil
}
