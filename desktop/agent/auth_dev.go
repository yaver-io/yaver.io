package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AuthDevStatus represents the current state of the local auth dev server.
type AuthDevStatus struct {
	Running   bool   `json:"running"`
	Engine    string `json:"engine"`
	Port      int    `json:"port"`
	AdminPort int    `json:"adminPort"`
	URL       string `json:"url"`
	AdminURL  string `json:"adminUrl"`
	UserCount int    `json:"userCount"`
}

// AuthProvider represents a configured OAuth provider.
type AuthProvider struct {
	Name     string `json:"name"` // google, github, apple, email
	Enabled  bool   `json:"enabled"`
	ClientID string `json:"clientId,omitempty"`
}

// AuthUser represents a user in the local auth server.
type AuthUser struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

// authDevConfig is persisted to ~/.yaver/auth/config.json.
type authDevConfig struct {
	Engine    string `json:"engine"`
	Port      int    `json:"port"`
	AdminPort int    `json:"adminPort"`
}

// AuthDevManager manages a local authentication server for development.
type AuthDevManager struct {
	configDir string
}

// NewAuthDevManager creates a new AuthDevManager.
func NewAuthDevManager() *AuthDevManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &AuthDevManager{
		configDir: filepath.Join(home, ".yaver", "auth"),
	}
}

// Start starts the auth dev server using the specified engine.
// Engine options: "logto" (default), "keycloak".
func (m *AuthDevManager) Start(engine string) error {
	if engine == "" {
		engine = "logto"
	}
	if engine != "logto" && engine != "keycloak" {
		return fmt.Errorf("unsupported engine %q: must be 'logto' or 'keycloak'", engine)
	}

	// Check Docker is available.
	if err := exec.Command("docker", "info").Run(); err != nil {
		return fmt.Errorf("docker is not available: %w", err)
	}

	engineDir := filepath.Join(m.configDir, engine)
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		return fmt.Errorf("create engine dir: %w", err)
	}

	composePath := filepath.Join(engineDir, "docker-compose.yml")
	var port, adminPort int

	switch engine {
	case "logto":
		port = 3001
		adminPort = 3002
		content := logtoDockerCompose()
		if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write docker-compose.yml: %w", err)
		}
	case "keycloak":
		port = 8080
		adminPort = 8080
		content := keycloakDockerCompose()
		if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write docker-compose.yml: %w", err)
		}
	}

	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	cmd.Dir = engineDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up: %w\n%s", err, string(out))
	}

	cfg := authDevConfig{
		Engine:    engine,
		Port:      port,
		AdminPort: adminPort,
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(m.configDir, "config.json"), cfgData, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// Stop stops the running auth dev server.
func (m *AuthDevManager) Stop() error {
	cfg, err := m.loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	composePath := filepath.Join(m.configDir, cfg.Engine, "docker-compose.yml")
	cmd := exec.Command("docker", "compose", "-f", composePath, "down")
	cmd.Dir = filepath.Join(m.configDir, cfg.Engine)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down: %w\n%s", err, string(out))
	}
	return nil
}

// Status returns the current state of the auth dev server.
func (m *AuthDevManager) Status() (*AuthDevStatus, error) {
	cfg, err := m.loadConfig()
	if err != nil {
		// No config means never started.
		return &AuthDevStatus{Running: false}, nil
	}

	composePath := filepath.Join(m.configDir, cfg.Engine, "docker-compose.yml")
	out, err := exec.Command("docker", "compose", "-f", composePath, "ps", "--services", "--filter", "status=running").Output()
	running := err == nil && strings.TrimSpace(string(out)) != ""

	status := &AuthDevStatus{
		Running:   running,
		Engine:    cfg.Engine,
		Port:      cfg.Port,
		AdminPort: cfg.AdminPort,
		URL:       fmt.Sprintf("http://localhost:%d", cfg.Port),
		AdminURL:  fmt.Sprintf("http://localhost:%d", cfg.AdminPort),
	}

	if running {
		count, _ := m.userCount(cfg)
		status.UserCount = count
	}

	return status, nil
}

// Users performs user management actions: "list", "create", "delete".
func (m *AuthDevManager) Users(action, email, password, role string) (interface{}, error) {
	cfg, err := m.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("auth dev server not configured: %w", err)
	}

	switch cfg.Engine {
	case "logto":
		return m.logtoUsers(cfg, action, email, password, role)
	case "keycloak":
		return m.keycloakUsers(cfg, action, email, password, role)
	default:
		return nil, fmt.Errorf("unsupported engine %q", cfg.Engine)
	}
}

// Providers returns the list of configured OAuth providers.
func (m *AuthDevManager) Providers() ([]AuthProvider, error) {
	cfg, err := m.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("auth dev server not configured: %w", err)
	}

	switch cfg.Engine {
	case "logto":
		return m.logtoProviders(cfg)
	case "keycloak":
		return m.keycloakProviders(cfg)
	default:
		return nil, fmt.Errorf("unsupported engine %q", cfg.Engine)
	}
}

// Setup generates framework-specific integration code.
// Framework options: "nextjs", "react", or "" for generic.
func (m *AuthDevManager) Setup(framework string) string {
	cfg, err := m.loadConfig()
	port := 3001
	if err == nil {
		port = cfg.Port
	}

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	switch strings.ToLower(framework) {
	case "nextjs", "next.js":
		return nextjsSetupCode(baseURL)
	case "react":
		return reactSetupCode(baseURL)
	default:
		return genericSetupCode(baseURL)
	}
}

// Tokens handles JWT actions: "generate" (create JWT for testing), "inspect" (decode JWT).
func (m *AuthDevManager) Tokens(action, userEmail, token string) (interface{}, error) {
	switch action {
	case "generate":
		return m.generateToken(userEmail)
	case "inspect":
		return m.inspectToken(token)
	default:
		return nil, fmt.Errorf("unsupported action %q: must be 'generate' or 'inspect'", action)
	}
}

// --- Internal helpers ---

func (m *AuthDevManager) loadConfig() (*authDevConfig, error) {
	data, err := os.ReadFile(filepath.Join(m.configDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg authDevConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (m *AuthDevManager) userCount(cfg *authDevConfig) (int, error) {
	switch cfg.Engine {
	case "logto":
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/users?page_size=1", cfg.Port))
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		// Logto returns total-number header or count in body.
		totalStr := resp.Header.Get("Total-Number")
		if totalStr == "" {
			return 0, nil
		}
		var count int
		fmt.Sscanf(totalStr, "%d", &count)
		return count, nil
	case "keycloak":
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/admin/realms/master/users/count", cfg.AdminPort))
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var count int
		json.Unmarshal(body, &count)
		return count, nil
	}
	return 0, nil
}

// logtoUsers calls the Logto management API.
func (m *AuthDevManager) logtoUsers(cfg *authDevConfig, action, email, password, role string) (interface{}, error) {
	base := fmt.Sprintf("http://localhost:%d", cfg.Port)

	switch action {
	case "list":
		resp, err := http.Get(base + "/api/users")
		if err != nil {
			return nil, fmt.Errorf("logto list users: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var users []AuthUser
		if err := json.Unmarshal(body, &users); err != nil {
			// Return raw if unmarshal fails.
			return string(body), nil
		}
		return users, nil

	case "create":
		if email == "" {
			return nil, fmt.Errorf("email is required for create")
		}
		payload := map[string]string{
			"primaryEmail": email,
			"password":     password,
		}
		if role != "" {
			payload["name"] = role
		}
		data, _ := json.Marshal(payload)
		resp, err := http.Post(base+"/api/users", "application/json", strings.NewReader(string(data)))
		if err != nil {
			return nil, fmt.Errorf("logto create user: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var user AuthUser
		if err := json.Unmarshal(body, &user); err != nil {
			return string(body), nil
		}
		return user, nil

	case "delete":
		if email == "" {
			return nil, fmt.Errorf("email is required for delete")
		}
		// Find user by email first.
		resp, err := http.Get(fmt.Sprintf("%s/api/users?search=%s", base, email))
		if err != nil {
			return nil, fmt.Errorf("logto find user: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var users []AuthUser
		if err := json.Unmarshal(body, &users); err != nil || len(users) == 0 {
			return nil, fmt.Errorf("user %q not found", email)
		}
		userID := users[0].ID
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/users/%s", base, userID), nil)
		client := &http.Client{Timeout: 10 * time.Second}
		delResp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("logto delete user: %w", err)
		}
		defer delResp.Body.Close()
		return map[string]string{"status": "deleted", "id": userID}, nil

	default:
		return nil, fmt.Errorf("unsupported action %q: must be 'list', 'create', or 'delete'", action)
	}
}

// keycloakUsers calls the Keycloak admin API.
func (m *AuthDevManager) keycloakUsers(cfg *authDevConfig, action, email, password, role string) (interface{}, error) {
	base := fmt.Sprintf("http://localhost:%d", cfg.AdminPort)

	switch action {
	case "list":
		resp, err := http.Get(base + "/admin/realms/master/users")
		if err != nil {
			return nil, fmt.Errorf("keycloak list users: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var users []AuthUser
		if err := json.Unmarshal(body, &users); err != nil {
			return string(body), nil
		}
		return users, nil

	case "create":
		if email == "" {
			return nil, fmt.Errorf("email is required for create")
		}
		payload := map[string]interface{}{
			"email":   email,
			"enabled": true,
		}
		if password != "" {
			payload["credentials"] = []map[string]interface{}{
				{"type": "password", "value": password, "temporary": false},
			}
		}
		data, _ := json.Marshal(payload)
		resp, err := http.Post(base+"/admin/realms/master/users", "application/json", strings.NewReader(string(data)))
		if err != nil {
			return nil, fmt.Errorf("keycloak create user: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			return map[string]string{"status": "created", "email": email}, nil
		}
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("keycloak create user failed (status %d): %s", resp.StatusCode, body)

	case "delete":
		if email == "" {
			return nil, fmt.Errorf("email is required for delete")
		}
		resp, err := http.Get(fmt.Sprintf("%s/admin/realms/master/users?email=%s", base, email))
		if err != nil {
			return nil, fmt.Errorf("keycloak find user: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var users []map[string]interface{}
		if err := json.Unmarshal(body, &users); err != nil || len(users) == 0 {
			return nil, fmt.Errorf("user %q not found", email)
		}
		userID, _ := users[0]["id"].(string)
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/admin/realms/master/users/%s", base, userID), nil)
		client := &http.Client{Timeout: 10 * time.Second}
		delResp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("keycloak delete user: %w", err)
		}
		defer delResp.Body.Close()
		return map[string]string{"status": "deleted", "id": userID}, nil

	default:
		return nil, fmt.Errorf("unsupported action %q: must be 'list', 'create', or 'delete'", action)
	}
}

func (m *AuthDevManager) logtoProviders(cfg *authDevConfig) ([]AuthProvider, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/connectors", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("logto list connectors: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		// Return defaults if server not reachable yet.
		return defaultProviders(), nil
	}

	var providers []AuthProvider
	for _, c := range raw {
		name, _ := c["connectorId"].(string)
		enabled, _ := c["enabled"].(bool)
		clientID, _ := c["config"].(map[string]interface{})["clientId"].(string)
		providers = append(providers, AuthProvider{
			Name:     name,
			Enabled:  enabled,
			ClientID: clientID,
		})
	}
	if len(providers) == 0 {
		return defaultProviders(), nil
	}
	return providers, nil
}

func (m *AuthDevManager) keycloakProviders(cfg *authDevConfig) ([]AuthProvider, error) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/admin/realms/master/identity-provider/instances", cfg.AdminPort))
	if err != nil {
		return nil, fmt.Errorf("keycloak list providers: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return defaultProviders(), nil
	}

	var providers []AuthProvider
	for _, p := range raw {
		alias, _ := p["alias"].(string)
		enabled, _ := p["enabled"].(bool)
		providers = append(providers, AuthProvider{
			Name:    alias,
			Enabled: enabled,
		})
	}
	if len(providers) == 0 {
		return defaultProviders(), nil
	}
	return providers, nil
}

func defaultProviders() []AuthProvider {
	return []AuthProvider{
		{Name: "email", Enabled: true},
		{Name: "google", Enabled: false},
		{Name: "github", Enabled: false},
		{Name: "apple", Enabled: false},
	}
}

// generateToken creates a signed JWT for testing using HMAC-SHA256.
func (m *AuthDevManager) generateToken(userEmail string) (interface{}, error) {
	if userEmail == "" {
		userEmail = "dev@localhost"
	}

	now := time.Now()
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	claims := map[string]interface{}{
		"sub":   "dev-" + strings.ReplaceAll(userEmail, "@", "-"),
		"email": userEmail,
		"iat":   now.Unix(),
		"exp":   now.Add(24 * time.Hour).Unix(),
		"iss":   "yaver-auth-dev",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("marshal claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	secret := []byte("yaver-dev-secret-key")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := signingInput + "." + sig
	return map[string]interface{}{
		"token":     token,
		"expiresAt": now.Add(24 * time.Hour).Format(time.RFC3339),
		"claims":    claims,
	}, nil
}

// inspectToken decodes a JWT and returns its claims.
func (m *AuthDevManager) inspectToken(token string) (interface{}, error) {
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode JWT header: %w", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}

	var header map[string]interface{}
	var payload map[string]interface{}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("parse JWT header: %w", err)
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("parse JWT payload: %w", err)
	}

	// Check expiry if present.
	expired := false
	if exp, ok := payload["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			expired = true
		}
	}

	return map[string]interface{}{
		"header":  header,
		"payload": payload,
		"expired": expired,
	}, nil
}

// --- Docker Compose templates ---

func logtoDockerCompose() string {
	return `version: "3.9"

services:
  postgres:
    image: postgres:14-alpine
    environment:
      POSTGRES_USER: logto
      POSTGRES_PASSWORD: logto_dev_password
      POSTGRES_DB: logto
    volumes:
      - logto_postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U logto"]
      interval: 10s
      timeout: 5s
      retries: 5

  logto:
    image: svhd/logto:latest
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "3001:3001"
      - "3002:3002"
    environment:
      DB_URL: postgres://logto:logto_dev_password@postgres:5432/logto
      ENDPOINT: http://localhost:3001
      ADMIN_ENDPOINT: http://localhost:3002
    command: ["sh", "-c", "npx @logto/cli db seed -- --db-url $DB_URL || true && node /etc/logto/dist/index.js"]

volumes:
  logto_postgres_data:
`
}

func keycloakDockerCompose() string {
	return `version: "3.9"

services:
  postgres:
    image: postgres:14-alpine
    environment:
      POSTGRES_USER: keycloak
      POSTGRES_PASSWORD: keycloak_dev_password
      POSTGRES_DB: keycloak
    volumes:
      - keycloak_postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U keycloak"]
      interval: 10s
      timeout: 5s
      retries: 5

  keycloak:
    image: quay.io/keycloak/keycloak:latest
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "8080:8080"
    environment:
      KC_DB: postgres
      KC_DB_URL: jdbc:postgresql://postgres:5432/keycloak
      KC_DB_USERNAME: keycloak
      KC_DB_PASSWORD: keycloak_dev_password
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: admin
    command: start-dev

volumes:
  keycloak_postgres_data:
`
}

// --- Setup code templates ---

func nextjsSetupCode(baseURL string) string {
	return fmt.Sprintf(`// Install: npm install @logto/next
// File: app/providers.tsx

import LogtoProvider from '@logto/next/server-component';

const logtoConfig = {
  endpoint: '%s',
  appId: 'your-app-id',       // Create in Logto Admin Console
  appSecret: 'your-app-secret',
  baseUrl: 'http://localhost:3000',
  cookieSecret: 'dev-cookie-secret-at-least-32-chars!!',
  cookieSecure: false, // true in production
};

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <LogtoProvider config={logtoConfig}>
      {children}
    </LogtoProvider>
  );
}

// File: app/api/logto/[action]/route.ts
import { handleSignIn, handleSignOut, handleCallback } from '@logto/next/server-component';

export const GET = handleSignIn;
export const POST = handleCallback;
`, baseURL)
}

func reactSetupCode(baseURL string) string {
	return fmt.Sprintf(`// Install: npm install @logto/react
// File: src/App.tsx

import { LogtoProvider, LogtoConfig } from '@logto/react';

const logtoConfig: LogtoConfig = {
  endpoint: '%s',
  appId: 'your-app-id', // Create in Logto Admin Console at http://localhost:3002
};

function App() {
  return (
    <LogtoProvider config={logtoConfig}>
      <YourRoutes />
    </LogtoProvider>
  );
}

// File: src/components/AuthButton.tsx
import { useLogto } from '@logto/react';

export function AuthButton() {
  const { isAuthenticated, signIn, signOut } = useLogto();

  if (isAuthenticated) {
    return <button onClick={() => signOut('http://localhost:3000')}>Sign Out</button>;
  }
  return (
    <button onClick={() => signIn('http://localhost:3000/callback')}>Sign In</button>
  );
}
`, baseURL)
}

func genericSetupCode(baseURL string) string {
	return fmt.Sprintf(`// Generic fetch-based auth integration
// Auth server: %s

// Sign in — redirect user to:
const signInURL = '%s/oidc/auth?response_type=code&client_id=YOUR_APP_ID&redirect_uri=http://localhost:3000/callback&scope=openid+email+profile';

// Exchange code for token (POST to your backend):
async function exchangeCode(code) {
  const res = await fetch('%s/oidc/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      client_id: 'YOUR_APP_ID',
      client_secret: 'YOUR_APP_SECRET',
      redirect_uri: 'http://localhost:3000/callback',
    }),
  });
  return res.json(); // { access_token, id_token, token_type, expires_in }
}

// Get user info:
async function getUserInfo(accessToken) {
  const res = await fetch('%s/oidc/me', {
    headers: { Authorization: 'Bearer ' + accessToken },
  });
  return res.json(); // { sub, email, name, ... }
}
`, baseURL, baseURL, baseURL, baseURL)
}
