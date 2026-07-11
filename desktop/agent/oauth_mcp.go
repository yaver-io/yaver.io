package main

// oauth_mcp.go — wires the self-hosted OAuth AS (oauth_provider.go) to the /mcp
// resource so Yaver can be added as a remote MCP connector in Claude Desktop /
// ChatGPT / Codex with per-user OAuth 2.1 (MCP auth spec 2025-11-25).
//
// Security model:
//   - A connector authenticates with an RS256 JWT minted by our own AS (the user
//     signed in via /oauth/authorize). Strangers with no account cannot get one.
//   - Connector requests are HARD-SCOPED default-deny: they may only call the
//     safe utility tools in mcpConnectorAllowedTools(). They can NEVER reach
//     exec/file/device/cloud/shell tools regardless of scope — this is a security
//     control, not the product owner-gate. Enforced via the server-stamped
//     X-Yaver-AllowedTools header that MCP dispatch already checks.
//   - The owner's own bearer (and paired/guest tokens) keep FULL access through
//     the existing s.auth path — this layer only ADDS the OAuth connector path.
//
// Discovery (RFC 9728 / RFC 8414) lets the client find the AS from a 401 on /mcp.

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// mcpConnectorAllowedTools is the default-deny allowlist for OAuth connector
// tokens: pure computation / lookup tools with NO access to the host's files,
// shell, devices, cloud, or state. Everything not listed is denied. Deliberately
// conservative — widen only via an explicit, reviewed policy.
//
// This is the scope a FRESH connector gets. A connector token lives in a
// third-party cloud (Anthropic's servers when you use the Claude app), so
// default-deny to toys is the right stranger-safe posture. The owner opts a
// specific connector UP to mcpOwnerConnectorAllowedTools() via the consent
// checkbox — see mcpElevatedConnectorScope.
func mcpConnectorAllowedTools() string {
	return strings.Join([]string{
		"calculate", "translate", "world_clock", "currency_exchange", "convert_units",
		"crypto_price", "stock_price", "weather", "news", "qr_code", "uuid", "hash",
		"base64", "color", "password_gen", "lorem_ipsum", "epoch", "regex_test",
		"jwt_decode", "figlet", "tldr", "geocode",
	}, ",")
}

// mcpElevatedConnectorScope is the marker, carried in a token's `scope` claim,
// that a connector was OWNER-elevated. It is NOT a scope a client may request:
// stripConnectorElevation removes it from any client-supplied scope, and it is
// re-added ONLY when the human ticks "Full access" on the /oauth consent form
// (handleOauthLogin). This keeps elevation a deliberate human decision, not
// something a connector can grant itself by asking.
const mcpElevatedConnectorScope = "owner-connector"

// mcpOwnerConnectorAllowedTools is the ELEVATED allowlist for a connector the
// owner deliberately trusted. It is small and voice-shaped on purpose: a general
// agent (the phone's Claude app) picks well from ~10 tools and badly from 740.
// Breadth comes from the `ops` grand-tool (one tool, ~290 verbs) rather than
// exposing every specialist tool. Even here, Layer-4 secret verbs never cross
// machines (mcp_remote_proxy.go) and ACT verbs stay confirm-gated.
//
//	ops / ops_verbs / ops_plan  — the whole ops surface incl. runner_turn,
//	                              runner_sessions, machine, status, git, deploy…
//	say                          — speak a line back
//	read-only device/status/discovery tools for "what do I have / is it up"
func mcpOwnerConnectorAllowedTools() string {
	return strings.Join([]string{
		"ops", "ops_verbs", "ops_plan",
		"say",
		"yaver_status", "yaver_devices", "get_info", "get_system_info",
		"primary_status", "primary_ping", "ping",
	}, ",")
}

// stripConnectorElevation removes the elevation marker from a scope string so a
// client cannot self-elevate by requesting scope="owner-connector". Called on
// the client-supplied scope before the marker is (maybe) re-added on consent.
func stripConnectorElevation(scope string) string {
	fields := strings.Fields(scope)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == mcpElevatedConnectorScope {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// connectorScopeElevated reports whether a verified connector JWT carries the
// owner-elevation marker minted by our own AS.
func connectorScopeElevated(claims map[string]interface{}) bool {
	sc, _ := claims["scope"].(string)
	for _, f := range strings.Fields(sc) {
		if f == mcpElevatedConnectorScope {
			return true
		}
	}
	return false
}

// parseVerifyJWT validates an RS256 JWT minted by our AS: signature against the
// local public key + exp. Returns claims on success. Does NOT check scope — callers
// decide whether a refresh token is acceptable.
func parseVerifyJWT(token string) (map[string]interface{}, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	key, err := ensureOauthKey()
	if err != nil {
		return nil, false
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	expf, ok := claims["exp"].(float64)
	if !ok || time.Now().Unix() > int64(expf) {
		return nil, false
	}
	return claims, true
}

// verifyOauthJWT accepts only ACCESS tokens (rejects refresh tokens) — used to
// authenticate /mcp requests.
func verifyOauthJWT(token string) (map[string]interface{}, bool) {
	claims, ok := parseVerifyJWT(token)
	if !ok {
		return nil, false
	}
	if sc, _ := claims["scope"].(string); sc == "refresh" {
		return nil, false
	}
	return claims, true
}

// mcpResourceURL is the canonical resource identifier for this server's /mcp
// endpoint (RFC 8707 audience / RFC 9728 resource).
func mcpResourceURL(r *http.Request) string {
	return strings.TrimSuffix(publicOauthBase(r), "/") + "/mcp"
}

// handleMCPProtectedResourceMetadata serves RFC 9728 Protected Resource Metadata
// so MCP clients discover the authorization server. Unauthenticated by design.
func (s *HTTPServer) handleMCPProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSuffix(publicOauthBase(r), "/")
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base + "/oauth"},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
	})
}

// handleMCPAuthServerMetadata serves RFC 8414 Authorization Server Metadata at the
// standard well-known path (the existing OIDC discovery lives under /oauth). It
// advertises PKCE S256, which both directories require.
func (s *HTTPServer) handleMCPAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := strings.TrimSuffix(publicOauthBase(r), "/") + "/oauth"
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/authorize",
		"token_endpoint":                        issuer + "/token",
		"jwks_uri":                              issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"mcp"},
	})
}

// authMCP guards /mcp. It ADDS an OAuth-connector path on top of the existing
// owner/paired/guest auth (s.auth):
//   - no bearer, or a JWT-shaped bearer that fails verification → 401 with a
//     WWW-Authenticate challenge pointing at the protected-resource metadata (so
//     a fresh connector client can discover the AS and start the OAuth flow);
//   - a valid AS-minted JWT → CONNECTOR path: strip any client-supplied scope
//     header and stamp the hard default-deny allowlist, then dispatch;
//   - any other (non-JWT) bearer → delegate to s.auth unchanged (owner / paired /
//     guest keep full access exactly as before).
func (s *HTTPServer) authMCP(next http.HandlerFunc) http.HandlerFunc {
	ownerPath := s.auth(next)
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !strings.HasPrefix(authHeader, "Bearer ") || token == "" {
			s.mcpAuthChallenge(w, r)
			return
		}
		// JWT-shaped bearer → this is (or claims to be) an OAuth connector token.
		if strings.Count(token, ".") == 2 {
			claims, ok := verifyOauthJWT(token)
			if !ok {
				s.mcpAuthChallenge(w, r)
				return
			}
			// RFC 8707 audience binding: if the token was issued for a specific
			// resource, it must be THIS server's /mcp resource.
			if aud, _ := claims["aud"].(string); aud != "" &&
				aud != mcpResourceURL(r) && !strings.HasSuffix(aud, "/mcp") {
				// aud is a bare client_id (legacy OIDC) — allow; a real resource
				// audience that doesn't match us is rejected.
				if strings.Contains(aud, "://") {
					s.mcpAuthChallenge(w, r)
					return
				}
			}
			// CONNECTOR: hard default-deny scope. Strip any inbound scope header
			// (never trust the client) and stamp the allowlist the TOKEN earned.
			// A fresh connector gets the toy list; one the owner elevated on the
			// consent form (marker in the signed scope claim) gets the small
			// voice-shaped ops surface. The elevation cannot be self-granted — it
			// is only ever minted by our own AS after a human ticked the box.
			allowed := mcpConnectorAllowedTools()
			if connectorScopeElevated(claims) {
				allowed = mcpOwnerConnectorAllowedTools()
				r.Header.Set("X-Yaver-Connector-Elevated", "true")
			}
			r.Header.Del("X-Yaver-AllowedTools")
			r.Header.Set("X-Yaver-AllowedTools", allowed)
			r.Header.Set("X-Yaver-Connector", "true")
			next(w, r)
			return
		}
		// Non-JWT bearer → owner / paired / guest path, full access as today.
		ownerPath(w, r)
	}
}

// mcpAuthChallenge returns 401 with the RFC 9728 WWW-Authenticate discovery hint.
func (s *HTTPServer) mcpAuthChallenge(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSuffix(publicOauthBase(r), "/")
	w.Header().Set("WWW-Authenticate",
		`Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource"`)
	jsonError(w, http.StatusUnauthorized, "authentication required")
}
