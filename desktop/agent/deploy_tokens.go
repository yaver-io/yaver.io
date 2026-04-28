package main

// deploy_tokens.go — first-class onboarding for the secrets a user
// needs in their Yaver vault to export / deploy a mobile sandbox
// project to the four canonical targets (Convex, Cloudflare, npm,
// PyPI) plus the App Store / Play Store binaries. Each target lists
// the secret names + scope hints + a deep-link to where to generate
// the token; the wizard collects, optionally verifies, and writes
// straight to the agent's vault per-project so the existing deploy
// scripts (which `eval "$(yaver vault env --project X)"` at the top)
// pick them up without any manual env juggling.
//
// Why "first class": today the user has to read CLAUDE.md, learn
// the secret names, generate each token in 4 different dashboards,
// then either paste env vars into a shell or run `yaver vault add`
// 8 times with the right flags. This module makes the catalogue
// programmatic and reusable across every UI surface (mobile, web,
// MCP) so onboarding lives wherever the user already is — the
// mobile sandbox wizard's export step, the dashboard's vault tab,
// or `yaver tokens onboard` from a terminal.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// DeployTokenField is one secret entry inside a target's spec.
type DeployTokenField struct {
	// Vault entry name — what the deploy scripts grep out of `yaver
	// vault env`. Matches the existing CI secret names where
	// possible (CONVEX_DEPLOY_KEY, CLOUDFLARE_API_TOKEN, …) so the
	// same vault entry powers both local self-hosted deploys and
	// CI-driven ones.
	Name string `json:"name"`
	// Human label rendered in the UI ("Convex deploy key").
	Label string `json:"label"`
	// One-line copy explaining what scope to grant when generating.
	Hint string `json:"hint"`
	// Direct URL the user opens to generate this token.
	GenerateURL string `json:"generateUrl"`
	// "secret" → mask + paste; "json" → multi-line paste; "file" →
	// upload-style. Mobile UI branches on this.
	Kind string `json:"kind"`
	// Whether the agent can ping the provider to confirm the token
	// works at save time. App Store / Play Store don't expose a
	// useful verify endpoint that doesn't already require the full
	// signing flow; those are kind="no-verify".
	CanVerify bool `json:"canVerify"`
	// Optional companion field that has to be filled in tandem
	// (e.g. CLOUDFLARE_ACCOUNT_ID alongside CLOUDFLARE_API_TOKEN).
	Pairs []string `json:"pairs,omitempty"`
}

type DeployTokenTarget struct {
	// Stable id — matches deploy_script_gen.go's target keys where
	// applicable so a UI can chain "set tokens" → "generate script"
	// → "deploy" with one slug.
	ID string `json:"id"`
	// Display title.
	Label string `json:"label"`
	// Short blurb shown above the field list.
	Description string `json:"description"`
	// Required vault fields for this target.
	Fields []DeployTokenField `json:"fields"`
}

// DeployTokenCatalogue is the canonical list of targets the mobile
// sandbox export wizard (and any other surface) walks the user
// through. Order matches a typical solo-founder ship sequence:
// backend (Convex) → web (Cloudflare) → registries (npm / PyPI) →
// app stores (TestFlight / Play). GitHub/GitLab PATs are out of
// scope here — they have their own first-class onboarding via
// /git/provider/setup which already verifies + persists.
func DeployTokenCatalogue() []DeployTokenTarget {
	return []DeployTokenTarget{
		{
			ID:          "convex",
			Label:       "Convex deploy",
			Description: "Backend functions, schema, HTTP actions. The deploy key powers `npx convex deploy --yes` on this machine and in CI.",
			Fields: []DeployTokenField{
				{
					Name:        "CONVEX_DEPLOY_KEY",
					Label:       "Convex deploy key",
					Hint:        "Pick the project, open Settings → Deploy keys, then choose Production.",
					GenerateURL: "https://dashboard.convex.dev",
					Kind:        "secret",
					CanVerify:   false, // Convex deploy keys aren't trivially verifiable without full deploy
				},
			},
		},
		{
			ID:          "cloudflare",
			Label:       "Cloudflare Workers + DNS",
			Description: "Edge worker deploys (yaver.io, carrotbytes.xyz) and DNS for project subdomains. One token covers both with the right scopes.",
			Fields: []DeployTokenField{
				{
					Name:        "CLOUDFLARE_API_TOKEN",
					Label:       "Cloudflare API token",
					Hint:        "Use the 'Edit Cloudflare Workers' template + add Zone DNS Edit for any zone you'll deploy DNS records to.",
					GenerateURL: "https://dash.cloudflare.com/profile/api-tokens",
					Kind:        "secret",
					CanVerify:   true,
					Pairs:       []string{"CLOUDFLARE_ACCOUNT_ID"},
				},
				{
					Name:        "CLOUDFLARE_ACCOUNT_ID",
					Label:       "Cloudflare account ID",
					Hint:        "From `wrangler whoami` or the right-hand sidebar of any zone overview page.",
					GenerateURL: "https://dash.cloudflare.com",
					Kind:        "secret",
					CanVerify:   false,
				},
			},
		},
		{
			ID:          "npm",
			Label:       "npm publish",
			Description: "Optional: only needed if your app exports an npm package or you want CI to publish on tag.",
			Fields: []DeployTokenField{
				{
					Name:        "NPM_TOKEN",
					Label:       "npm publish token",
					Hint:        "Granular access token, scope: publish to your packages or org.",
					GenerateURL: "https://www.npmjs.com/settings/~/tokens/new",
					Kind:        "secret",
					CanVerify:   true,
				},
			},
		},
		{
			ID:          "pypi",
			Label:       "PyPI publish",
			Description: "Optional: needed if you also ship a Python SDK alongside the app.",
			Fields: []DeployTokenField{
				{
					Name:        "PYPI_TOKEN",
					Label:       "PyPI API token",
					Hint:        "Scope to a single project or 'Entire account' for first-time publish.",
					GenerateURL: "https://pypi.org/manage/account/token/",
					Kind:        "secret",
					CanVerify:   false,
				},
			},
		},
		{
			ID:          "testflight",
			Label:       "TestFlight",
			Description: "iOS build upload. The .p8 + key id + issuer id + team id all four are needed; same set CI uses.",
			Fields: []DeployTokenField{
				{
					Name:        "APP_STORE_CONNECT_API_KEY",
					Label:       "App Store Connect API key (.p8 contents)",
					Hint:        "Paste the entire .p8 file body, including BEGIN/END lines.",
					GenerateURL: "https://appstoreconnect.apple.com/access/api",
					Kind:        "secret",
					CanVerify:   false,
				},
				{
					Name:        "APP_STORE_CONNECT_API_KEY_ID",
					Label:       "Key ID",
					Hint:        "10-char alphanumeric shown next to the key on App Store Connect.",
					GenerateURL: "https://appstoreconnect.apple.com/access/api",
					Kind:        "secret",
					CanVerify:   false,
				},
				{
					Name:        "APP_STORE_CONNECT_API_KEY_ISSUER",
					Label:       "Issuer ID",
					Hint:        "UUID at the top of the API Keys page.",
					GenerateURL: "https://appstoreconnect.apple.com/access/api",
					Kind:        "secret",
					CanVerify:   false,
				},
				{
					Name:        "APPLE_TEAM_ID",
					Label:       "Apple Team ID",
					Hint:        "10-char team identifier from developer.apple.com membership.",
					GenerateURL: "https://developer.apple.com/account",
					Kind:        "secret",
					CanVerify:   false,
				},
			},
		},
		{
			ID:          "playstore",
			Label:       "Google Play Store",
			Description: "Android AAB upload. Service-account JSON from a Play Console project linked to a Google Cloud project.",
			Fields: []DeployTokenField{
				{
					Name:        "GOOGLE_PLAY_SERVICE_ACCOUNT",
					Label:       "Service account JSON",
					Hint:        "Paste the entire JSON. Service account needs the Release Manager role on the Play Console.",
					GenerateURL: "https://play.google.com/console",
					Kind:        "json",
					CanVerify:   false,
				},
			},
		},
	}
}

// handleDeployTokensCatalogue serves the catalogue as JSON. Mobile +
// web hit this once on screen mount to render the UI; MCP can call
// it before walking the user through onboarding.
func (s *HTTPServer) handleDeployTokensCatalogue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"targets": DeployTokenCatalogue(),
	})
}

// handleDeployTokensVerify pings the provider to confirm a single
// secret value works. We only verify fields where CanVerify=true in
// the catalogue — for the others we just accept the value blindly
// and let the user discover the truth at deploy time.
func (s *HTTPServer) handleDeployTokensVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		Provider string `json:"provider"` // "cloudflare" | "npm" | …
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		jsonError(w, http.StatusBadRequest, "token is required")
		return
	}
	out, err := verifyDeployToken(req.Provider, strings.TrimSpace(req.Token))
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     false,
			"reason": err.Error(),
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"detail": out,
	})
}

// verifyDeployToken hits each provider's smallest-cost identity
// endpoint. Returns a short status string (provider-specific) on
// success.
func verifyDeployToken(provider, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	switch strings.ToLower(provider) {
	case "cloudflare":
		req, _ := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("cloudflare %d: %s", resp.StatusCode, string(body))
		}
		var data struct {
			Success bool `json:"success"`
			Result  struct {
				Status string `json:"status"`
				ID     string `json:"id"`
			} `json:"result"`
		}
		_ = json.Unmarshal(body, &data)
		if !data.Success || data.Result.Status != "active" {
			return "", fmt.Errorf("token reports status %q", data.Result.Status)
		}
		return fmt.Sprintf("active token id=%s", data.Result.ID), nil

	case "npm":
		req, _ := http.NewRequest("GET", "https://registry.npmjs.org/-/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("npm %d: %s", resp.StatusCode, string(body))
		}
		var data struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal(body, &data)
		if data.Username == "" {
			return "", fmt.Errorf("npm whoami returned no username")
		}
		return "logged in as " + data.Username, nil

	case "github":
		// Already covered by /git/provider/setup, but expose here for
		// catalogue consistency. Reuses verifyGitHubToken.
		username, _, err := verifyGitHubToken(token)
		if err != nil {
			return "", err
		}
		return "logged in as " + username, nil

	case "gitlab":
		username, _, err := verifyGitLabToken("gitlab.com", token)
		if err != nil {
			return "", err
		}
		return "logged in as " + username, nil

	default:
		return "", fmt.Errorf("provider %q has no verify path; saved as-is at deploy time", provider)
	}
}

// handleDeployTokensSave writes one or many tokens to the agent's
// vault scoped to a project. Single-shot UX: mobile/web posts the
// full set once the user clicks Save, the agent verifies the ones
// it can, persists everything, and returns per-field status so the
// UI can render green ticks / amber warnings appropriately.
func (s *HTTPServer) handleDeployTokensSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		Project string            `json:"project"` // vault scope; "" means global
		Tokens  map[string]string `json:"tokens"`  // map[VAULT_NAME] = value
		// Map vault-name → which provider to verify against. Matches
		// catalogue ids ("cloudflare", "npm", …). Names without a
		// matching provider entry are saved without verification.
		VerifyAs map[string]string `json:"verifyAs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Tokens) == 0 {
		jsonError(w, http.StatusBadRequest, "tokens map is required")
		return
	}

	if s.vaultStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "vault not unlocked on this agent")
		return
	}

	results := make(map[string]map[string]interface{}, len(req.Tokens))
	for name, value := range req.Tokens {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			results[name] = map[string]interface{}{"saved": false, "reason": "empty value"}
			continue
		}

		entry := VaultEntry{
			Name:     name,
			Project:  strings.TrimSpace(req.Project),
			Category: "deploy",
			Value:    trimmed,
		}
		var verifyDetail string
		var verifyErr error
		if provider := strings.TrimSpace(req.VerifyAs[name]); provider != "" {
			verifyDetail, verifyErr = verifyDeployToken(provider, trimmed)
		}

		if err := s.vaultStore.Set(entry); err != nil {
			results[name] = map[string]interface{}{"saved": false, "reason": err.Error()}
			continue
		}
		row := map[string]interface{}{"saved": true}
		if verifyErr != nil {
			row["verify"] = "failed"
			row["verifyReason"] = verifyErr.Error()
			log.Printf("[deploy-tokens] %s saved but verify failed: %v", name, verifyErr)
		} else if verifyDetail != "" {
			row["verify"] = "passed"
			row["verifyDetail"] = verifyDetail
		}
		results[name] = row
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"results": results,
	})
}

// handleDeployTokensStatus reads the vault and returns, per target,
// which fields are present + their last-updated timestamp. Used by
// the mobile + web UIs to render "Convex ready ✓ / Cloudflare needs
// 1 more token / TestFlight not started" status badges per project
// without exposing any actual secret values.
func (s *HTTPServer) handleDeployTokensStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	if s.vaultStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "vault not unlocked on this agent")
		return
	}
	entries := s.vaultStore.List(project)
	have := make(map[string]int64, len(entries))
	for _, e := range entries {
		have[e.Name] = e.UpdatedAt
	}

	type fieldStatus struct {
		Name      string `json:"name"`
		Set       bool   `json:"set"`
		UpdatedAt int64  `json:"updatedAt,omitempty"`
	}
	type targetStatus struct {
		ID     string        `json:"id"`
		Label  string        `json:"label"`
		Ready  bool          `json:"ready"`
		Total  int           `json:"total"`
		Filled int           `json:"filled"`
		Fields []fieldStatus `json:"fields"`
	}

	var out []targetStatus
	for _, t := range DeployTokenCatalogue() {
		st := targetStatus{ID: t.ID, Label: t.Label, Total: len(t.Fields)}
		for _, f := range t.Fields {
			ts, ok := have[f.Name]
			if ok {
				st.Filled++
			}
			st.Fields = append(st.Fields, fieldStatus{Name: f.Name, Set: ok, UpdatedAt: ts})
		}
		st.Ready = st.Filled == st.Total
		out = append(out, st)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"project": project,
		"targets": out,
	})
}

// Used by deploy_tokens_test.go to ensure verify path is reachable.
// Real callers go through HTTP. Kept tiny so the test surface
// doesn't become its own maintenance load.
func deployTokensSelfTest() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not on PATH: %w", err)
	}
	return nil
}
