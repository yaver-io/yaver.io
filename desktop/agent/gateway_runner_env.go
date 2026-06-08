package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// gateway_runner_env.go — the operator-box bridge between a tenant's coding
// runner and the Yaver Gateway.
//
// On an operator/fleet box a tenant must NOT get the host's environment
// (which holds the operator's API keys, vault-derived secrets, cloud
// tokens, etc.) and must NOT get the owner's GLM key. Instead the tenant's
// runner is pointed at the Yaver Gateway with a per-tenant SCOPED token:
//
//   OPENAI_BASE_URL = <gateway>/v1
//   OPENAI_API_KEY  = ygw_<scoped token for THIS tenant>
//
// The scoped token is minted by the agent acting as the operator
// (s.token is the operator principal in operator mode, which isOwner →
// /gateway/token/mint is allowed). The upstream GLM key never touches the
// box — it lives only as a Cloudflare Worker secret. A leaked scoped token
// only authorizes gateway inference for that one tenant within their
// operator-set caps (see gatewayPolicy / gatewayTokens).
//
// Gateway URL comes from env YAVER_GATEWAY_URL (e.g.
// https://yaver-gateway.<acct>.workers.dev). Unset → the bridge is off and
// tenants get only the clean (secret-stripped) env, no inference provider.

// gatewayBaseURL returns the configured gateway origin (no trailing slash),
// or "" if unset.
func gatewayBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("YAVER_GATEWAY_URL")), "/")
}

// per-tenant scoped-token cache (keyed by tenant userId). Minting is
// idempotent enough for our purpose: one token per tenant per agent
// process. A revoke on the operator side makes the cached token 401 at the
// gateway; clearTenantGatewayToken drops it so the next session re-mints.
var (
	tenantGatewayTokens   sync.Map // tenantUserID -> raw ygw_ token
	tenantGatewayTokenMu  sync.Mutex
)

func clearTenantGatewayToken(tenantUserID string) {
	tenantGatewayTokens.Delete(tenantUserID)
}

// mintGatewayToken asks Convex to mint a scoped inference token for
// tenantUserID. operatorToken must belong to an owner/operator principal
// (isOwner) or the route 403s.
func mintGatewayToken(convexURL, operatorToken, tenantUserID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"targetUserId": tenantUserID, "label": "operator-fleet"})
	req, err := newBearerRequest("POST", strings.TrimRight(convexURL, "/")+"/gateway/token/mint", operatorToken, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mint gateway token: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode mint response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("mint returned empty token")
	}
	return out.Token, nil
}

// tenantGatewayToken returns a cached scoped token for the tenant, minting
// one if needed. Returns "" if minting fails (caller falls back to a
// no-inference clean env).
func (s *HTTPServer) tenantGatewayToken(tenantUserID string) string {
	tenantUserID = strings.TrimSpace(tenantUserID)
	if tenantUserID == "" {
		return ""
	}
	if v, ok := tenantGatewayTokens.Load(tenantUserID); ok {
		if tok, _ := v.(string); tok != "" {
			return tok
		}
	}
	tenantGatewayTokenMu.Lock()
	defer tenantGatewayTokenMu.Unlock()
	// Re-check under lock.
	if v, ok := tenantGatewayTokens.Load(tenantUserID); ok {
		if tok, _ := v.(string); tok != "" {
			return tok
		}
	}
	tok, err := mintGatewayToken(s.convexURL, s.token, tenantUserID)
	if err != nil {
		log.Printf("[OPERATOR] mint gateway token for tenant %s: %v", tenantUserID, err)
		return ""
	}
	tenantGatewayTokens.Store(tenantUserID, tok)
	return tok
}

// secretEnvNamePatterns: a var is dropped from a tenant's env if its NAME
// contains any of these substrings (case-insensitive) or has a sensitive
// prefix below. Conservative denylist — keeps PATH/HOME/SHELL/TERM/LANG so
// the runner still works, drops anything key/token/secret-shaped.
var secretEnvNameSubstrings = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE",
	"APIKEY", "AUTH",
}

var secretEnvPrefixes = []string{
	"ANTHROPIC_", "OPENAI_", "GLM_", "ZAI_", "AWS_", "GCP_", "GOOGLE_",
	"AZURE_", "CLOUDFLARE_", "CF_", "CONVEX_", "LEMONSQUEEZY_", "HCLOUD_",
	"NPM_", "GITHUB_", "GITLAB_", "YAVER_", "DEEPINFRA_", "OPENROUTER_",
	"DEEPGRAM_", "CARTESIA_", "STRIPE_", "DO_", "DIGITALOCEAN_",
}

func isSecretEnvName(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range secretEnvPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	for _, s := range secretEnvSubstr() {
		if strings.Contains(up, s) {
			return true
		}
	}
	return false
}

func secretEnvSubstr() []string { return secretEnvNameSubstrings }

// cleanTenantEnv strips secret-shaped variables from a base environment so a
// tenant process inherits NONE of the operator's keys/tokens. Keeps benign
// system vars (PATH, HOME, SHELL, TERM, LANG, LC_*, TZ, TMPDIR, PWD, USER…).
func cleanTenantEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		if isSecretEnvName(kv[:eq]) {
			continue // drop it
		}
		out = append(out, kv)
	}
	return out
}

// tenantRunnerBaseEnv builds the base environment for a tenant's runner on
// an operator box: the host env with all secrets stripped, plus the gateway
// inference provider (if YAVER_GATEWAY_URL is set and a scoped token mints).
// Non-operator boxes never call this (they keep normal behavior).
func (s *HTTPServer) tenantRunnerBaseEnv(tenantUserID string) []string {
	env := cleanTenantEnv(os.Environ())
	gw := gatewayBaseURL()
	if gw == "" {
		return env // bridge off — clean env, no inference provider
	}
	tok := s.tenantGatewayToken(tenantUserID)
	if tok == "" {
		return env // mint failed — clean env, no provider (fail-closed, no key)
	}
	// OpenAI-compatible: clients post to <base>/chat/completions.
	openaiBase := gw + "/v1"
	env = append(env,
		"OPENAI_BASE_URL="+openaiBase,
		"OPENAI_API_BASE="+openaiBase,
		"OPENAI_API_KEY="+tok,
		// opencode/codex providers that read a generic base:
		"YAVER_INFERENCE_GATEWAY="+gw,
	)
	return env
}
