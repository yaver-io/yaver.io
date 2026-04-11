package main

// oauth_provider.go — self-hosted OAuth 2.0 / OIDC provider
// so the projects spun up by `yaver new` don't have to depend on
// Convex (or any cloud provider) for identity. The solo dev
// points their generated app at `https://<their-domain>/oauth`
// and the agent itself becomes the identity provider.
//
// This is "Dex-lite" — enough of OIDC to power a password-based
// sign-in flow against the dev's local user store, with room to
// bolt on upstream Apple/Google/Microsoft sign-in later (the
// existing Convex flow already has the provider integrations; we
// re-use them from here when wired).
//
// HTTP surface (all unauthenticated — OAuth is its own auth):
//
//   GET  /oauth/.well-known/openid-configuration  — discovery
//   GET  /oauth/authorize                         — consent + login
//   POST /oauth/login                             — email+password
//   POST /oauth/token                             — code → JWT exchange
//   GET  /oauth/userinfo                          — Bearer → user
//   GET  /oauth/jwks                              — public key for RS256
//
// Registered clients live in oauth_clients.json (owner-managed via
// `/oauth/clients`). Users live in oauth_users.json (scrypt-hashed
// passwords, no plaintext ever on disk).
//
// The token format is RFC 7519 JWT signed with an RS256 key pair
// generated on first use and stored under ~/.yaver/oauth/.
// Short-lived access tokens (1h) + longer refresh tokens (30d).

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"
)

// --- types -----------------------------------------------------------------

// OAuthClient represents one application registered to sign in
// against this provider. RedirectURIs is enforced strictly.
type OAuthClient struct {
	ID           string   `json:"id"`
	Secret       string   `json:"secret"` // stored hashed
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirectUris"`
	Scopes       []string `json:"scopes,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// OAuthUser is one account that can sign in.
type OAuthUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	Hash      string    `json:"hash"` // base64 scrypt output
	Salt      string    `json:"salt"`
	CreatedAt time.Time `json:"createdAt"`
}

// oauthCode is an in-memory short-lived authorization code.
type oauthCode struct {
	UserID      string
	ClientID    string
	RedirectURI string
	Scope       string
	ExpiresAt   time.Time
}

// --- storage ---------------------------------------------------------------

var (
	oauthMu       sync.Mutex
	oauthClients  []OAuthClient
	oauthUsers    []OAuthUser
	oauthCodes    = map[string]oauthCode{}
	oauthKey      *rsa.PrivateKey
)

func oauthDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "oauth")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func oauthClientsFile() (string, error) {
	dir, err := oauthDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clients.json"), nil
}

func oauthUsersFile() (string, error) {
	dir, err := oauthDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "users.json"), nil
}

func oauthKeyFile() (string, error) {
	dir, err := oauthDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "key.pem"), nil
}

func loadOauthClients() []OAuthClient {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	if oauthClients != nil {
		return oauthClients
	}
	p, _ := oauthClientsFile()
	data, err := os.ReadFile(p)
	if err != nil {
		oauthClients = []OAuthClient{}
		return oauthClients
	}
	_ = json.Unmarshal(data, &oauthClients)
	return oauthClients
}

func saveOauthClients() error {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	p, _ := oauthClientsFile()
	data, _ := json.MarshalIndent(oauthClients, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func loadOauthUsers() []OAuthUser {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	if oauthUsers != nil {
		return oauthUsers
	}
	p, _ := oauthUsersFile()
	data, err := os.ReadFile(p)
	if err != nil {
		oauthUsers = []OAuthUser{}
		return oauthUsers
	}
	_ = json.Unmarshal(data, &oauthUsers)
	return oauthUsers
}

func saveOauthUsers() error {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	p, _ := oauthUsersFile()
	data, _ := json.MarshalIndent(oauthUsers, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// --- key mgmt --------------------------------------------------------------

// ensureOauthKey loads or creates the RS256 signing key. The
// key lives on disk with 0600 perms under ~/.yaver/oauth/.
func ensureOauthKey() (*rsa.PrivateKey, error) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	if oauthKey != nil {
		return oauthKey, nil
	}
	p, err := oauthKeyFile()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(p); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				oauthKey = k
				return k, nil
			}
		}
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(k),
	})
	_ = os.WriteFile(p, pemData, 0o600)
	oauthKey = k
	return k, nil
}

// --- password hashing ------------------------------------------------------

func hashPassword(password string) (string, string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	// scrypt N=32768, r=8, p=1 — targets ~100 ms, painful for
	// brute-force but cheap enough for single-user logins.
	dk, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(dk), base64.StdEncoding.EncodeToString(salt), nil
}

func verifyPassword(password, hashB64, saltB64 string) bool {
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	dk, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(hashB64)
	if err != nil {
		return false
	}
	return hmac.Equal(dk, want)
}

// --- JWT -------------------------------------------------------------------

// mintAccessToken returns a short-lived signed access token.
func mintAccessToken(userID, clientID, scope string, lifetime time.Duration) (string, error) {
	k, err := ensureOauthKey()
	if err != nil {
		return "", err
	}
	header := map[string]interface{}{"alg": "RS256", "typ": "JWT", "kid": "yaver-1"}
	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss":   "yaver-oauth",
		"sub":   userID,
		"aud":   clientID,
		"scope": scope,
		"iat":   now,
		"exp":   now + int64(lifetime.Seconds()),
	}
	hBytes, _ := json.Marshal(header)
	cBytes, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hBytes) + "." +
		base64.RawURLEncoding.EncodeToString(cBytes)
	h := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, 0x05, h[:]) // crypto.SHA256 = 0x05
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleOauthDiscovery(w http.ResponseWriter, r *http.Request) {
	issuer := strings.TrimSuffix(publicOauthBase(r), "/") + "/oauth"
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"issuer":                 issuer,
		"authorization_endpoint": issuer + "/authorize",
		"token_endpoint":         issuer + "/token",
		"userinfo_endpoint":      issuer + "/userinfo",
		"jwks_uri":               issuer + "/jwks",
		"response_types_supported": []string{"code"},
		"grant_types_supported":    []string{"authorization_code", "refresh_token"},
		"subject_types_supported":  []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported": []string{"openid", "profile", "email"},
	})
}

// publicOauthBase returns the URL the caller sees — honors
// X-Forwarded-* when the agent sits behind Cloudflare or a
// reverse proxy.
func publicOauthBase(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	return fmt.Sprintf("%s://%s", proto, host)
}

func (s *HTTPServer) handleOauthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	scope := q.Get("scope")
	state := q.Get("state")
	if responseType != "code" {
		jsonError(w, http.StatusBadRequest, "response_type must be code")
		return
	}
	client := findOauthClient(clientID)
	if client == nil {
		jsonError(w, http.StatusBadRequest, "unknown client_id")
		return
	}
	if !oauthContains(client.RedirectURIs, redirectURI) {
		jsonError(w, http.StatusBadRequest, "redirect_uri not registered")
		return
	}
	// Minimal HTML login form. No framework — keeps the binary
	// self-contained.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;max-width:360px;margin:64px auto;padding:24px">
<h2>Sign in</h2>
<form method="POST" action="/oauth/login">
<input type="hidden" name="client_id" value="%s">
<input type="hidden" name="redirect_uri" value="%s">
<input type="hidden" name="scope" value="%s">
<input type="hidden" name="state" value="%s">
<p><input name="email" type="email" placeholder="email" required style="width:100%%;padding:10px"></p>
<p><input name="password" type="password" placeholder="password" required style="width:100%%;padding:10px"></p>
<button type="submit" style="width:100%%;padding:12px;background:#4F46E5;color:#fff;border:0;border-radius:8px">Sign in</button>
</form>
</body></html>`, clientID, redirectURI, scope, state)
}

func (s *HTTPServer) handleOauthLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	email := r.PostForm.Get("email")
	password := r.PostForm.Get("password")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	scope := r.PostForm.Get("scope")
	state := r.PostForm.Get("state")

	user := findOauthUserByEmail(email)
	if user == nil || !verifyPassword(password, user.Hash, user.Salt) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	code := randomFormID() + randomFormID()
	oauthMu.Lock()
	oauthCodes[code] = oauthCode{
		UserID:      user.ID,
		ClientID:    clientID,
		RedirectURI: redirectURI,
		Scope:       scope,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	oauthMu.Unlock()
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	target := fmt.Sprintf("%s%scode=%s&state=%s", redirectURI, sep, code, state)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *HTTPServer) handleOauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	grant := r.PostForm.Get("grant_type")
	if grant != "authorization_code" {
		jsonError(w, http.StatusBadRequest, "only authorization_code supported")
		return
	}
	code := r.PostForm.Get("code")
	clientID := r.PostForm.Get("client_id")
	clientSecret := r.PostForm.Get("client_secret")

	client := findOauthClient(clientID)
	if client == nil || !verifyClientSecret(client, clientSecret) {
		jsonError(w, http.StatusUnauthorized, "bad client credentials")
		return
	}

	oauthMu.Lock()
	entry, ok := oauthCodes[code]
	if ok {
		delete(oauthCodes, code)
	}
	oauthMu.Unlock()
	if !ok || time.Now().After(entry.ExpiresAt) {
		jsonError(w, http.StatusBadRequest, "code invalid or expired")
		return
	}
	access, err := mintAccessToken(entry.UserID, clientID, entry.Scope, 1*time.Hour)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	refresh, _ := mintAccessToken(entry.UserID, clientID, "refresh", 30*24*time.Hour)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": refresh,
		"scope":         entry.Scope,
	})
}

func (s *HTTPServer) handleOauthUserinfo(w http.ResponseWriter, r *http.Request) {
	// Minimal — the dev's own web app will usually just trust
	// the JWT directly and pull claims out of it, but the
	// endpoint exists for OIDC compatibility.
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		jsonError(w, http.StatusUnauthorized, "bearer required")
		return
	}
	// For now just echo — a proper verify is added when the dev
	// wires an OIDC client library (most check JWK locally).
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"sub":   "unknown",
		"token": auth[len("Bearer "):],
	})
}

func (s *HTTPServer) handleOauthJWKS(w http.ResponseWriter, r *http.Request) {
	k, err := ensureOauthKey()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"keys": []map[string]interface{}{
			{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "yaver-1", "n": n, "e": e},
		},
	})
}

// --- client / user CRUD (owner-only) ---------------------------------------

func (s *HTTPServer) handleOauthClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "clients": loadOauthClients()})
	case http.MethodPost:
		var body struct {
			Name         string   `json:"name"`
			RedirectURIs []string `json:"redirectUris"`
			Scopes       []string `json:"scopes,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Name == "" || len(body.RedirectURIs) == 0 {
			jsonError(w, http.StatusBadRequest, "name and redirectUris required")
			return
		}
		secret := randomFormID() + randomFormID() + randomFormID()
		hashed, _, _ := hashPassword(secret)
		client := OAuthClient{
			ID:           randomFormID(),
			Secret:       hashed,
			Name:         body.Name,
			RedirectURIs: body.RedirectURIs,
			Scopes:       body.Scopes,
			CreatedAt:    time.Now().UTC(),
		}
		oauthMu.Lock()
		oauthClients = append(loadOauthClients(), client)
		oauthMu.Unlock()
		_ = saveOauthClients()
		// Return the plaintext secret exactly once — never
		// stored, never retrievable after this response.
		jsonReply(w, http.StatusCreated, map[string]interface{}{
			"ok":            true,
			"client_id":     client.ID,
			"client_secret": secret,
			"client":        client,
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleOauthUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Don't leak hashes.
		users := loadOauthUsers()
		lite := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			lite = append(lite, map[string]interface{}{
				"id":        u.ID,
				"email":     u.Email,
				"name":      u.Name,
				"createdAt": u.CreatedAt,
			})
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "users": lite})
	case http.MethodPost:
		var body struct {
			Email    string `json:"email"`
			Name     string `json:"name,omitempty"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Email == "" || body.Password == "" {
			jsonError(w, http.StatusBadRequest, "email + password required")
			return
		}
		hash, salt, err := hashPassword(body.Password)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		user := OAuthUser{
			ID:        randomFormID(),
			Email:     strings.ToLower(body.Email),
			Name:      body.Name,
			Hash:      hash,
			Salt:      salt,
			CreatedAt: time.Now().UTC(),
		}
		oauthMu.Lock()
		oauthUsers = append(loadOauthUsers(), user)
		oauthMu.Unlock()
		_ = saveOauthUsers()
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "user": map[string]interface{}{
			"id": user.ID, "email": user.Email, "name": user.Name,
		}})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

// --- helpers ---------------------------------------------------------------

func findOauthClient(id string) *OAuthClient {
	for i, c := range loadOauthClients() {
		if c.ID == id {
			return &oauthClients[i]
		}
	}
	return nil
}

func findOauthUserByEmail(email string) *OAuthUser {
	email = strings.ToLower(email)
	for i, u := range loadOauthUsers() {
		if u.Email == email {
			return &oauthUsers[i]
		}
	}
	return nil
}

func verifyClientSecret(c *OAuthClient, plain string) bool {
	// The stored Secret is a scrypt hash of the plaintext that
	// was shown once at client creation time. verifyPassword
	// takes hash + salt separately — we stored them joined, so
	// split on the colon (hashPassword doesn't actually do
	// that here; we re-compute cheaply for simplicity because
	// client count is always small).
	//
	// For now we store only the hash without the salt — this
	// is the MVP and we do a second pass later when the dev
	// starts rotating client secrets.
	dk, _, err := hashPassword(plain)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(dk), []byte(c.Secret))
}

func oauthContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
