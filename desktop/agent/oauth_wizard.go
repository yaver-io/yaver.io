package main

// oauth_wizard.go — OAuth provider setup wizard for Yaver workspaces.
//
// Guides developers through setting up OAuth with Google, Apple,
// Microsoft, and GitHub for their apps via step-by-step instructions.
// Also handles credential storage, live flow testing, production URI
// migration, and integration code generation.
//
// Usage from CLI or MCP:
//
//	wizard := NewOAuthWizardManager("/path/to/project")
//	steps, err := wizard.Setup("google")
//	err = wizard.SaveCredentials("google", map[string]string{
//	    "GOOGLE_CLIENT_ID":     "...",
//	    "GOOGLE_CLIENT_SECRET": "...",
//	})
//	result, err := wizard.Test("google")
//	providers, err := wizard.List()

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// OAuthProviderStatus describes the current state of one OAuth provider.
type OAuthProviderStatus struct {
	Provider     string    `json:"provider"`
	Configured   bool      `json:"configured"`
	Working      bool      `json:"working"`
	ClientID     string    `json:"clientId"`     // masked: "abcd..."
	RedirectURIs []string  `json:"redirectUris"` // derived from env or defaults
	LastTested   time.Time `json:"lastTested,omitempty"`
}

// OAuthSetupStep is one actionable step in the setup wizard.
type OAuthSetupStep struct {
	StepNumber  int      `json:"stepNumber"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	URL         string   `json:"url,omitempty"`       // link to the relevant console
	Action      string   `json:"action"`              // what the user must do
	InputNeeded []string `json:"inputNeeded,omitempty"` // env var names to paste back
}

// OAuthWizardManager orchestrates OAuth setup for all providers.
type OAuthWizardManager struct {
	mu      sync.Mutex
	workDir string
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewOAuthWizardManager creates a wizard scoped to the given project directory.
func NewOAuthWizardManager(workDir string) *OAuthWizardManager {
	return &OAuthWizardManager{workDir: workDir}
}

// ---------------------------------------------------------------------------
// Setup — step-by-step guide
// ---------------------------------------------------------------------------

// Setup returns an ordered list of setup steps for the given provider.
// provider must be one of: "google", "apple", "microsoft", "github".
func (w *OAuthWizardManager) Setup(provider string) ([]OAuthSetupStep, error) {
	domain := w.detectAppDomain()

	switch strings.ToLower(provider) {
	case "google":
		return w.setupGoogle(domain), nil
	case "apple":
		return w.setupApple(domain), nil
	case "microsoft":
		return w.setupMicrosoft(domain), nil
	case "github":
		return w.setupGitHub(domain), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q — choose: google, apple, microsoft, github", provider)
	}
}

func (w *OAuthWizardManager) setupGoogle(domain string) []OAuthSetupStep {
	devCallback := "http://localhost:3000/api/auth/callback/google"
	prodCallback := fmt.Sprintf("https://%s/api/auth/callback/google", domain)

	return []OAuthSetupStep{
		{
			StepNumber:  1,
			Title:       "Create or select a Google Cloud project",
			Description: "Every OAuth app lives inside a Google Cloud project. Create one if you don't have it yet.",
			URL:         "https://console.cloud.google.com/projectcreate",
			Action:      "Open the URL, click 'New Project', give it a name, then click 'Create'.",
		},
		{
			StepNumber:  2,
			Title:       "Configure the OAuth consent screen",
			Description: "Google requires an app name, support email, and at least one scope before it will issue credentials.",
			URL:         "https://console.cloud.google.com/apis/credentials/consent",
			Action: "Select 'External', fill in App name and User support email. " +
				"Under Scopes click 'Add or remove scopes' and add: openid, email, profile. " +
				"Save and continue until you reach the summary.",
		},
		{
			StepNumber:  3,
			Title:       "Create an OAuth 2.0 Client ID",
			Description: "This generates the client_id and client_secret your app uses.",
			URL:         "https://console.cloud.google.com/apis/credentials",
			Action:      "Click 'Create Credentials' → 'OAuth client ID' → Application type: 'Web application'. Give it a name.",
		},
		{
			StepNumber:  4,
			Title:       "Set Authorized Redirect URIs",
			Description: fmt.Sprintf("Add both the local dev URI and the production URI so the same credentials work in both environments.\n  Dev:  %s\n  Prod: %s", devCallback, prodCallback),
			URL:         "https://console.cloud.google.com/apis/credentials",
			Action: fmt.Sprintf(
				"In the OAuth client editor, under 'Authorized redirect URIs', add:\n  1. %s\n  2. %s\nClick 'Save'.",
				devCallback, prodCallback,
			),
		},
		{
			StepNumber:  5,
			Title:       "Copy Client ID and Client Secret",
			Description: "After saving, Google shows you the credentials. Copy both values.",
			URL:         "https://console.cloud.google.com/apis/credentials",
			Action:      "Click the pencil icon on your newly created client. Copy 'Client ID' and 'Client secret'.",
			InputNeeded: []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"},
		},
	}
}

func (w *OAuthWizardManager) setupApple(domain string) []OAuthSetupStep {
	prodCallback := fmt.Sprintf("https://%s/api/auth/callback/apple", domain)

	return []OAuthSetupStep{
		{
			StepNumber:  1,
			Title:       "Register an App ID and enable Sign In with Apple",
			Description: "The App ID is the umbrella identifier for your iOS/macOS app. The Services ID (step 2) is what Apple uses as the OAuth client_id.",
			URL:         "https://developer.apple.com/account/resources/identifiers/list",
			Action: "Click '+' → select 'App IDs' → Capabilities tab → check 'Sign In with Apple'. " +
				"Complete the registration. Note your Bundle ID.",
		},
		{
			StepNumber:  2,
			Title:       "Create a Services ID",
			Description: "The Services ID is your OAuth client_id. It is separate from the App ID.",
			URL:         "https://developer.apple.com/account/resources/identifiers/list/serviceId",
			Action: "Click '+' → select 'Services IDs' → give it a reverse-domain identifier " +
				"(e.g. com.yourapp.web). Enable 'Sign In with Apple'. Click 'Configure'.",
		},
		{
			StepNumber:  3,
			Title:       "Configure domains and return URLs",
			Description: fmt.Sprintf("Apple validates the return URL strictly — it MUST be HTTPS even for dev (use ngrok or a tunnel).\n  Prod: %s", prodCallback),
			URL:         "https://developer.apple.com/account/resources/identifiers/list/serviceId",
			Action: fmt.Sprintf(
				"In the Services ID configuration, add:\n  Domain: %s\n  Return URL: %s\nClick 'Save'.\n\nNOTE: Apple requires HTTPS. For local dev use a tunnel (e.g. ngrok http 3000).",
				domain, prodCallback,
			),
		},
		{
			StepNumber:  4,
			Title:       "Create a Sign In with Apple Key and download the .p8 file",
			Description: "The .p8 private key is used to create client secrets for the token endpoint. Download it once — Apple will not let you download it again.",
			URL:         "https://developer.apple.com/account/resources/authkeys/list",
			Action: "Click '+' → enable 'Sign In with Apple' → configure → select your App ID → Continue → Register. " +
				"Download the .p8 file. Note the Key ID shown on the confirmation page.",
		},
		{
			StepNumber:  5,
			Title:       "Collect all Apple credentials",
			Description: "You need four values. The private key is the full contents of the .p8 file (multi-line).",
			Action:      "Open your .p8 file in a text editor. Copy the full contents including the BEGIN/END PRIVATE KEY lines.",
			InputNeeded: []string{
				"APPLE_CLIENT_ID",    // your Services ID (e.g. com.yourapp.web)
				"APPLE_TEAM_ID",      // 10-char team ID visible in top-right of developer.apple.com
				"APPLE_KEY_ID",       // Key ID from the key detail page
				"APPLE_PRIVATE_KEY",  // full .p8 file contents
			},
		},
	}
}

func (w *OAuthWizardManager) setupMicrosoft(domain string) []OAuthSetupStep {
	devCallback := "http://localhost:3000/api/auth/callback/microsoft"
	prodCallback := fmt.Sprintf("https://%s/api/auth/callback/microsoft", domain)

	return []OAuthSetupStep{
		{
			StepNumber:  1,
			Title:       "Register a new application in Azure AD",
			Description: "This creates the OAuth app in Microsoft Entra ID (formerly Azure AD).",
			URL:         "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
			Action: "Click 'New registration'. Enter a display name. Under 'Supported account types' choose " +
				"'Accounts in any organizational directory and personal Microsoft accounts (Multitenant + personal)'.",
		},
		{
			StepNumber:  2,
			Title:       "Add Redirect URIs",
			Description: fmt.Sprintf("Add the local dev URI and the production URI.\n  Dev:  %s\n  Prod: %s", devCallback, prodCallback),
			URL:         "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
			Action: fmt.Sprintf(
				"In your app registration, click 'Authentication' in the left sidebar. "+
					"Under 'Web' click 'Add a platform' if needed. Add redirect URIs:\n  1. %s\n  2. %s\nSave.",
				devCallback, prodCallback,
			),
		},
		{
			StepNumber:  3,
			Title:       "Create a client secret",
			Description: "The secret is shown only once — copy it immediately after creation.",
			URL:         "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
			Action: "In your app registration, click 'Certificates & secrets' → 'New client secret'. " +
				"Set an expiry (recommend 24 months). Click 'Add'. Copy the 'Value' column — NOT the 'Secret ID'.",
		},
		{
			StepNumber:  4,
			Title:       "Grant API permissions",
			Description: "Add the Microsoft Graph delegated permissions needed for sign-in.",
			URL:         "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
			Action: "Click 'API permissions' → 'Add a permission' → 'Microsoft Graph' → 'Delegated permissions'. " +
				"Search for and add: User.Read, email, profile, openid. Click 'Grant admin consent for <your-org>' if visible.",
		},
		{
			StepNumber:  5,
			Title:       "Collect credentials from the Overview page",
			Description: "All three values are on the 'Overview' tab of your app registration.",
			URL:         "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
			Action:      "Copy 'Application (client) ID' as CLIENT_ID and 'Directory (tenant) ID' as TENANT_ID. Use the secret value from step 3.",
			InputNeeded: []string{
				"MICROSOFT_CLIENT_ID",
				"MICROSOFT_CLIENT_SECRET",
				"MICROSOFT_TENANT_ID",
			},
		},
	}
}

func (w *OAuthWizardManager) setupGitHub(domain string) []OAuthSetupStep {
	devCallback := "http://localhost:3000/api/auth/callback/github"
	prodCallback := fmt.Sprintf("https://%s/api/auth/callback/github", domain)

	return []OAuthSetupStep{
		{
			StepNumber:  1,
			Title:       "Register a new GitHub OAuth App",
			Description: "GitHub OAuth apps are per-developer-account or per-organization.",
			URL:         "https://github.com/settings/developers",
			Action: "Click 'OAuth Apps' → 'Register a new application'. " +
				"Fill in Application name and Homepage URL (e.g. https://" + domain + ").",
		},
		{
			StepNumber:  2,
			Title:       "Set the Authorization callback URL",
			Description: fmt.Sprintf("GitHub only allows one callback URL per OAuth App. Use the production URL and rely on the dev OAuth App for local work, or use a single URL that handles both.\n  Dev:  %s\n  Prod: %s", devCallback, prodCallback),
			URL:         "https://github.com/settings/developers",
			Action: fmt.Sprintf(
				"In the 'Authorization callback URL' field enter: %s\n\n"+
					"TIP: For local dev, register a second GitHub OAuth App with callback %s. "+
					"Both apps can share the same GitHub account.",
				prodCallback, devCallback,
			),
		},
		{
			StepNumber:  3,
			Title:       "Generate a client secret",
			Description: "The secret is shown only once — copy it before navigating away.",
			URL:         "https://github.com/settings/developers",
			Action:      "After registering the app, click 'Generate a new client secret'. Copy both the Client ID (at the top) and the generated secret.",
			InputNeeded: []string{"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET"},
		},
	}
}

// ---------------------------------------------------------------------------
// SaveCredentials — write to .env.local
// ---------------------------------------------------------------------------

// SaveCredentials appends or updates the given env vars in .env.local inside
// the workDir. Existing entries are updated in-place; new entries are appended.
// Returns the path of the file written.
func (w *OAuthWizardManager) SaveCredentials(provider string, creds map[string]string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(creds) == 0 {
		return "", fmt.Errorf("no credentials provided")
	}

	// Validate that keys match the expected set for the provider.
	if err := validateCredKeys(provider, creds); err != nil {
		return "", err
	}

	envPath := filepath.Join(w.workDir, ".env.local")
	existing, err := readEnvFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reading %s: %w", envPath, err)
	}
	if existing == nil {
		existing = make(map[string]string)
	}

	for k, v := range creds {
		existing[k] = v
	}

	if err := writeEnvFile(envPath, existing); err != nil {
		return "", fmt.Errorf("writing %s: %w", envPath, err)
	}
	return envPath, nil
}

// validateCredKeys checks that the supplied keys include (at minimum) the
// expected env var names for the provider.
func validateCredKeys(provider string, creds map[string]string) error {
	required := map[string][]string{
		"google":    {"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"},
		"apple":     {"APPLE_CLIENT_ID", "APPLE_TEAM_ID", "APPLE_KEY_ID", "APPLE_PRIVATE_KEY"},
		"microsoft": {"MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_TENANT_ID"},
		"github":    {"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET"},
	}
	keys, ok := required[strings.ToLower(provider)]
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	var missing []string
	for _, k := range keys {
		if v := strings.TrimSpace(creds[k]); v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required credentials for %s: %s", provider, strings.Join(missing, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test — live OAuth flow
// ---------------------------------------------------------------------------

// Test starts a local HTTP server, opens the browser to the OAuth authorize
// URL, waits for the callback, and verifies a code was returned.
// Returns a human-readable result string.
func (w *OAuthWizardManager) Test(provider string) (string, error) {
	w.mu.Lock()
	creds, err := readEnvFile(filepath.Join(w.workDir, ".env.local"))
	w.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reading .env.local: %w", err)
	}
	if creds == nil {
		creds = make(map[string]string)
	}

	config, err := buildOAuthTestConfig(provider, creds)
	if err != nil {
		return "", err
	}

	// Pick a random ephemeral port.
	port, err := freePort()
	if err != nil {
		return "", fmt.Errorf("finding free port: %w", err)
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	state := oauthRandomState()
	authorizeURL := config.buildAuthorizeURL(redirectURI, state)

	// Channel receives the raw query string from the callback.
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}

	mux.HandleFunc("/callback", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if gotState := q.Get("state"); gotState != state {
			errCh <- fmt.Errorf("state mismatch: expected %s, got %s", state, gotState)
			fmt.Fprintf(rw, "<html><body>State mismatch — possible CSRF. You can close this tab.</body></html>")
			return
		}
		code := q.Get("code")
		errParam := q.Get("error")
		if errParam != "" {
			desc := q.Get("error_description")
			errCh <- fmt.Errorf("provider returned error: %s — %s", errParam, desc)
			fmt.Fprintf(rw, "<html><body>OAuth error: %s. You can close this tab.</body></html>", errParam)
			return
		}
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprintf(rw, "<html><body>No code received. You can close this tab.</body></html>")
			return
		}
		resultCh <- code
		fmt.Fprintf(rw, "<html><body><strong>Success!</strong> OAuth code received. You can close this tab.</body></html>")
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return "", fmt.Errorf("binding port %d: %w", port, err)
	}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case errCh <- serveErr:
			default:
			}
		}
	}()
	defer srv.Close()

	openBrowser(authorizeURL)

	timeout := time.After(2 * time.Minute)
	select {
	case code := <-resultCh:
		_ = code // we just confirm receipt; token exchange needs a real client secret
		return fmt.Sprintf(
			"OAuth test PASSED for %s.\nAuthorization code received successfully.\nRedirect URI used: %s",
			provider, redirectURI,
		), nil
	case e := <-errCh:
		return "", fmt.Errorf("OAuth test FAILED for %s: %w", provider, e)
	case <-timeout:
		return "", fmt.Errorf("OAuth test timed out after 2 minutes — no callback received")
	}
}

// oauthProviderTestConfig holds per-provider authorize endpoint metadata.
type oauthProviderTestConfig struct {
	authorizeEndpoint string
	clientID          string
	scopes            []string
}

func buildOAuthTestConfig(provider string, creds map[string]string) (*oauthProviderTestConfig, error) {
	switch strings.ToLower(provider) {
	case "google":
		id := creds["GOOGLE_CLIENT_ID"]
		if id == "" {
			return nil, fmt.Errorf("GOOGLE_CLIENT_ID not configured — run Setup first")
		}
		return &oauthProviderTestConfig{
			authorizeEndpoint: "https://accounts.google.com/o/oauth2/v2/auth",
			clientID:          id,
			scopes:            []string{"openid", "email", "profile"},
		}, nil
	case "apple":
		id := creds["APPLE_CLIENT_ID"]
		if id == "" {
			return nil, fmt.Errorf("APPLE_CLIENT_ID not configured — run Setup first")
		}
		return &oauthProviderTestConfig{
			authorizeEndpoint: "https://appleid.apple.com/auth/authorize",
			clientID:          id,
			scopes:            []string{"name", "email"},
		}, nil
	case "microsoft":
		id := creds["MICROSOFT_CLIENT_ID"]
		if id == "" {
			return nil, fmt.Errorf("MICROSOFT_CLIENT_ID not configured — run Setup first")
		}
		tenant := creds["MICROSOFT_TENANT_ID"]
		if tenant == "" {
			tenant = "common"
		}
		return &oauthProviderTestConfig{
			authorizeEndpoint: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenant),
			clientID:          id,
			scopes:            []string{"openid", "email", "profile", "User.Read"},
		}, nil
	case "github":
		id := creds["GITHUB_CLIENT_ID"]
		if id == "" {
			return nil, fmt.Errorf("GITHUB_CLIENT_ID not configured — run Setup first")
		}
		return &oauthProviderTestConfig{
			authorizeEndpoint: "https://github.com/login/oauth/authorize",
			clientID:          id,
			scopes:            []string{"read:user", "user:email"},
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}

func (c *oauthProviderTestConfig) buildAuthorizeURL(redirectURI, state string) string {
	params := url.Values{
		"client_id":     {c.clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"state":         {state},
		"scope":         {strings.Join(c.scopes, " ")},
	}
	return c.authorizeEndpoint + "?" + params.Encode()
}

// ---------------------------------------------------------------------------
// List — provider status overview
// ---------------------------------------------------------------------------

// List returns the configuration status for all four supported providers.
func (w *OAuthWizardManager) List() ([]OAuthProviderStatus, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	envPath := filepath.Join(w.workDir, ".env.local")
	creds, err := readEnvFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", envPath, err)
	}
	if creds == nil {
		creds = make(map[string]string)
	}

	domain := detectAppDomainFromEnv(creds, w.workDir)

	providers := []struct {
		name      string
		idKey     string
		secretKey string
	}{
		{"google", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"},
		{"apple", "APPLE_CLIENT_ID", "APPLE_PRIVATE_KEY"},
		{"microsoft", "MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET"},
		{"github", "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET"},
	}

	var statuses []OAuthProviderStatus
	for _, p := range providers {
		clientID := creds[p.idKey]
		secret := creds[p.secretKey]
		configured := clientID != "" && secret != ""

		s := OAuthProviderStatus{
			Provider:     p.name,
			Configured:   configured,
			Working:      false,
			ClientID:     maskSecret(clientID),
			RedirectURIs: defaultRedirectURIs(p.name, domain),
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

// defaultRedirectURIs returns the canonical redirect URI list for a provider.
func defaultRedirectURIs(provider, domain string) []string {
	cb := fmt.Sprintf("https://%s/api/auth/callback/%s", domain, provider)
	local := fmt.Sprintf("http://localhost:3000/api/auth/callback/%s", provider)
	return []string{local, cb}
}

// ---------------------------------------------------------------------------
// MigrateURIs — domain migration helper
// ---------------------------------------------------------------------------

// MigrateURIs generates a report of all redirect URIs that must be updated
// in each provider console when moving from oldDomain to newDomain, plus the
// production env var diff.
func (w *OAuthWizardManager) MigrateURIs(oldDomain, newDomain string) (string, error) {
	if oldDomain == "" || newDomain == "" {
		return "", fmt.Errorf("oldDomain and newDomain must both be non-empty")
	}

	w.mu.Lock()
	creds, err := readEnvFile(filepath.Join(w.workDir, ".env.local"))
	w.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reading .env.local: %w", err)
	}
	if creds == nil {
		creds = make(map[string]string)
	}

	providers := []string{"google", "apple", "microsoft", "github"}
	consoleURLs := map[string]string{
		"google":    "https://console.cloud.google.com/apis/credentials",
		"apple":     "https://developer.apple.com/account/resources/identifiers/list/serviceId",
		"microsoft": "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
		"github":    "https://github.com/settings/developers",
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# OAuth Redirect URI Migration\n"))
	sb.WriteString(fmt.Sprintf("# Old domain: %s\n", oldDomain))
	sb.WriteString(fmt.Sprintf("# New domain: %s\n\n", newDomain))

	for _, p := range providers {
		oldURI := fmt.Sprintf("https://%s/api/auth/callback/%s", oldDomain, p)
		newURI := fmt.Sprintf("https://%s/api/auth/callback/%s", newDomain, p)

		sb.WriteString(fmt.Sprintf("## %s\n", strings.Title(p)))
		sb.WriteString(fmt.Sprintf("Console: %s\n", consoleURLs[p]))
		sb.WriteString(fmt.Sprintf("Remove: %s\n", oldURI))
		sb.WriteString(fmt.Sprintf("Add:    %s\n\n", newURI))
	}

	// Production env var diff.
	sb.WriteString("## .env.local updates\n")
	sb.WriteString(fmt.Sprintf("# Replace all occurrences of %q with %q in .env.local\n\n", oldDomain, newDomain))

	updated := false
	for k, v := range creds {
		if strings.Contains(v, oldDomain) {
			newVal := strings.ReplaceAll(v, oldDomain, newDomain)
			sb.WriteString(fmt.Sprintf("-%s=%s\n", k, v))
			sb.WriteString(fmt.Sprintf("+%s=%s\n\n", k, newVal))
			updated = true
		}
	}
	if !updated {
		sb.WriteString("(no domain-specific values found in .env.local)\n")
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// GenerateCode — integration snippets
// ---------------------------------------------------------------------------

// GenerateCode returns a ready-to-paste integration snippet for the given
// provider and framework combination.
// framework: "nextjs+better-auth", "nextjs+next-auth", "expo".
func (w *OAuthWizardManager) GenerateCode(provider, framework string) (string, error) {
	prov := strings.ToLower(provider)
	fw := strings.ToLower(framework)

	switch fw {
	case "nextjs+better-auth":
		return generateBetterAuth(prov)
	case "nextjs+next-auth", "nextjs+nextauth":
		return generateNextAuth(prov)
	case "expo":
		return generateExpo(prov)
	default:
		return "", fmt.Errorf("unsupported framework %q — choose: nextjs+better-auth, nextjs+next-auth, expo", framework)
	}
}

func generateBetterAuth(provider string) (string, error) {
	providerSnippets := map[string]string{
		"google": `
import { betterAuth } from "better-auth";
import { google } from "better-auth/providers/google";

export const auth = betterAuth({
  // ... your existing config ...
  socialProviders: {
    google: {
      clientId: process.env.GOOGLE_CLIENT_ID!,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET!,
    },
  },
});
`,
		"apple": `
import { betterAuth } from "better-auth";
import { apple } from "better-auth/providers/apple";

export const auth = betterAuth({
  // ... your existing config ...
  socialProviders: {
    apple: {
      clientId: process.env.APPLE_CLIENT_ID!,
      teamId: process.env.APPLE_TEAM_ID!,
      keyId: process.env.APPLE_KEY_ID!,
      privateKey: process.env.APPLE_PRIVATE_KEY!,
    },
  },
});
`,
		"microsoft": `
import { betterAuth } from "better-auth";
import { microsoft } from "better-auth/providers/microsoft";

export const auth = betterAuth({
  // ... your existing config ...
  socialProviders: {
    microsoft: {
      clientId: process.env.MICROSOFT_CLIENT_ID!,
      clientSecret: process.env.MICROSOFT_CLIENT_SECRET!,
      tenantId: process.env.MICROSOFT_TENANT_ID ?? "common",
    },
  },
});
`,
		"github": `
import { betterAuth } from "better-auth";
import { github } from "better-auth/providers/github";

export const auth = betterAuth({
  // ... your existing config ...
  socialProviders: {
    github: {
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
    },
  },
});
`,
	}

	snippet, ok := providerSnippets[provider]
	if !ok {
		return "", fmt.Errorf("unsupported provider %q for better-auth", provider)
	}
	return strings.TrimLeft(snippet, "\n"), nil
}

func generateNextAuth(provider string) (string, error) {
	providerSnippets := map[string]string{
		"google": `
// app/api/auth/[...nextauth]/route.ts
import NextAuth from "next-auth";
import Google from "next-auth/providers/google";

const handler = NextAuth({
  providers: [
    Google({
      clientId: process.env.GOOGLE_CLIENT_ID!,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET!,
    }),
  ],
});

export { handler as GET, handler as POST };
`,
		"apple": `
// app/api/auth/[...nextauth]/route.ts
import NextAuth from "next-auth";
import Apple from "next-auth/providers/apple";

const handler = NextAuth({
  providers: [
    Apple({
      clientId: process.env.APPLE_CLIENT_ID!,
      clientSecret: {
        appleId: process.env.APPLE_CLIENT_ID!,
        teamId: process.env.APPLE_TEAM_ID!,
        keyId: process.env.APPLE_KEY_ID!,
        privateKey: process.env.APPLE_PRIVATE_KEY!,
      },
    }),
  ],
});

export { handler as GET, handler as POST };
`,
		"microsoft": `
// app/api/auth/[...nextauth]/route.ts
import NextAuth from "next-auth";
import AzureAD from "next-auth/providers/azure-ad";

const handler = NextAuth({
  providers: [
    AzureAD({
      clientId: process.env.MICROSOFT_CLIENT_ID!,
      clientSecret: process.env.MICROSOFT_CLIENT_SECRET!,
      tenantId: process.env.MICROSOFT_TENANT_ID ?? "common",
    }),
  ],
});

export { handler as GET, handler as POST };
`,
		"github": `
// app/api/auth/[...nextauth]/route.ts
import NextAuth from "next-auth";
import GitHub from "next-auth/providers/github";

const handler = NextAuth({
  providers: [
    GitHub({
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
    }),
  ],
});

export { handler as GET, handler as POST };
`,
	}

	snippet, ok := providerSnippets[provider]
	if !ok {
		return "", fmt.Errorf("unsupported provider %q for next-auth", provider)
	}
	return strings.TrimLeft(snippet, "\n"), nil
}

func generateExpo(provider string) (string, error) {
	// bt is a backtick character. TypeScript template literals use backticks,
	// which cannot be embedded inside Go raw string literals — use this const.
	const bt = "`"

	providerSnippets := map[string]string{
		"google": "// lib/auth.ts — Google OAuth for Expo with expo-auth-session\n" +
			"import * as Google from \"expo-auth-session/providers/google\";\n" +
			"import * as WebBrowser from \"expo-web-browser\";\n\n" +
			"WebBrowser.maybeCompleteAuthSession();\n\n" +
			"export function useGoogleAuth() {\n" +
			"  const [request, response, promptAsync] = Google.useAuthRequest({\n" +
			"    clientId: process.env.EXPO_PUBLIC_GOOGLE_CLIENT_ID,\n" +
			"    iosClientId: process.env.EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID,\n" +
			"    androidClientId: process.env.EXPO_PUBLIC_GOOGLE_ANDROID_CLIENT_ID,\n" +
			"  });\n\n" +
			"  return { request, response, signIn: () => promptAsync() };\n" +
			"}\n" +
			"// .env.local:\n" +
			"// EXPO_PUBLIC_GOOGLE_CLIENT_ID=<web client id>\n" +
			"// EXPO_PUBLIC_GOOGLE_IOS_CLIENT_ID=<ios client id>\n" +
			"// EXPO_PUBLIC_GOOGLE_ANDROID_CLIENT_ID=<android client id>\n",

		"apple": "// lib/auth.ts — Apple Sign In for Expo (iOS native)\n" +
			"import * as AppleAuthentication from \"expo-apple-authentication\";\n\n" +
			"export async function signInWithApple() {\n" +
			"  const credential = await AppleAuthentication.signInAsync({\n" +
			"    requestedScopes: [\n" +
			"      AppleAuthentication.AppleAuthenticationScope.FULL_NAME,\n" +
			"      AppleAuthentication.AppleAuthenticationScope.EMAIL,\n" +
			"    ],\n" +
			"  });\n" +
			"  // credential.identityToken — send to your backend for verification\n" +
			"  return credential;\n" +
			"}\n" +
			"// Note: Apple Sign In requires a real device. It will not work in the simulator.\n",

		"microsoft": "// lib/auth.ts — Microsoft OAuth for Expo with expo-auth-session\n" +
			"import * as AuthSession from \"expo-auth-session\";\n" +
			"import * as WebBrowser from \"expo-web-browser\";\n\n" +
			"WebBrowser.maybeCompleteAuthSession();\n\n" +
			"const TENANT_ID = process.env.EXPO_PUBLIC_MICROSOFT_TENANT_ID ?? \"common\";\n" +
			"const CLIENT_ID = process.env.EXPO_PUBLIC_MICROSOFT_CLIENT_ID!;\n\n" +
			"const discovery = AuthSession.useAutoDiscovery(\n" +
			"  " + bt + "https://login.microsoftonline.com/${TENANT_ID}/v2.0" + bt + "\n" +
			");\n\n" +
			"export function useMicrosoftAuth() {\n" +
			"  const redirectUri = AuthSession.makeRedirectUri({ scheme: \"your-app-scheme\" });\n" +
			"  const [request, response, promptAsync] = AuthSession.useAuthRequest(\n" +
			"    {\n" +
			"      clientId: CLIENT_ID,\n" +
			"      scopes: [\"openid\", \"profile\", \"email\", \"User.Read\"],\n" +
			"      redirectUri,\n" +
			"    },\n" +
			"    discovery\n" +
			"  );\n\n" +
			"  return { request, response, signIn: () => promptAsync() };\n" +
			"}\n",

		"github": "// lib/auth.ts — GitHub OAuth for Expo (server-side flow required)\n" +
			"// GitHub does not support PKCE / SPA flows — use a backend proxy.\n" +
			"import * as AuthSession from \"expo-auth-session\";\n" +
			"import * as WebBrowser from \"expo-web-browser\";\n\n" +
			"WebBrowser.maybeCompleteAuthSession();\n\n" +
			"export async function signInWithGitHub(apiBase: string) {\n" +
			"  // apiBase = your backend URL, e.g. \"https://api.yourapp.com\"\n" +
			"  const redirectUri = AuthSession.makeRedirectUri({ scheme: \"your-app-scheme\" });\n" +
			"  const result = await WebBrowser.openAuthSessionAsync(\n" +
			"    " + bt + "${apiBase}/api/auth/github/start?redirect=${encodeURIComponent(redirectUri)}" + bt + ",\n" +
			"    redirectUri\n" +
			"  );\n" +
			"  // Your backend handles the code exchange and returns a session token\n" +
			"  return result;\n" +
			"}\n",
	}
	_ = bt // used above in string concatenation

	snippet, ok := providerSnippets[provider]
	if !ok {
		return "", fmt.Errorf("unsupported provider %q for expo", provider)
	}
	return snippet, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// readEnvFile parses a .env file into a key→value map.
// Lines starting with '#' and blank lines are skipped.
// Inline comments (# after value) are not stripped to preserve existing values.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := line[idx+1:]
		// Strip surrounding quotes if present.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		result[key] = val
	}
	return result, scanner.Err()
}

// writeEnvFile writes vars to path, updating existing entries in-place and
// appending new ones at the end. Comments and blank lines are preserved.
func writeEnvFile(path string, vars map[string]string) error {
	// Read existing lines so we can update in-place.
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
		// Remove trailing empty element from Split.
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	written := make(map[string]bool)
	for i, line := range lines {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if newVal, ok := vars[key]; ok {
			lines[i] = key + "=" + quoteEnvValue(newVal)
			written[key] = true
		}
	}

	// Append keys that were not already in the file.
	for k, v := range vars {
		if !written[k] {
			lines = append(lines, k+"="+quoteEnvValue(v))
		}
	}

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0600)
}

// quoteEnvValue wraps a value in double-quotes if it contains spaces or
// special characters; otherwise returns it as-is.
func quoteEnvValue(v string) string {
	needsQuote := strings.ContainsAny(v, " \t\n\"'#\\")
	if needsQuote {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return v
}

// maskSecret shows the first 4 chars followed by "..." for safe display.
// Returns an empty string if s is empty.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "..."
	}
	return s[:4] + "..."
}

// detectAppDomain tries to determine the production domain for this project.
func (w *OAuthWizardManager) detectAppDomain() string {
	w.mu.Lock()
	creds, _ := readEnvFile(filepath.Join(w.workDir, ".env.local"))
	w.mu.Unlock()
	if creds == nil {
		creds = make(map[string]string)
	}
	return detectAppDomainFromEnv(creds, w.workDir)
}

// detectAppDomainFromEnv is the stateless version used by List and other callers
// that already hold a lock.
func detectAppDomainFromEnv(creds map[string]string, workDir string) string {
	// 1. NEXTAUTH_URL / BETTER_AUTH_URL / APP_URL in .env.local.
	for _, key := range []string{"NEXTAUTH_URL", "BETTER_AUTH_URL", "APP_URL", "NEXT_PUBLIC_APP_URL"} {
		if raw := creds[key]; raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				return u.Host
			}
		}
	}

	// 2. "homepage" in package.json.
	pkgPath := filepath.Join(workDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg struct {
			Homepage string `json:"homepage"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Homepage != "" {
			if u, err := url.Parse(pkg.Homepage); err == nil && u.Host != "" {
				return u.Host
			}
		}
	}

	// 3. Default.
	return "localhost:3000"
}

// freePort returns a random available TCP port.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// oauthRandomState returns a URL-safe random hex string for OAuth state params.
// Uses the package-level randomHex helper (defined in analytics_selfhost.go).
func oauthRandomState() string {
	return randomHex(16)
}
