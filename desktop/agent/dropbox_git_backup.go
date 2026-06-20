package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	dropboxVaultProject = "managed-git"
	dropboxVaultEntry   = "dropbox-oauth"
)

var (
	dropboxOAuthMu       sync.Mutex
	dropboxOAuthSessions = map[string]*dropboxOAuthSession{}
)

type dropboxOAuthSession struct {
	ID           string    `json:"id"`
	ClientID     string    `json:"-"`
	CodeVerifier string    `json:"-"`
	RedirectURI  string    `json:"redirectUri"`
	AuthURL      string    `json:"authUrl"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type dropboxTokenRecord struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
	AccountID    string `json:"accountId,omitempty"`
	Scope        string `json:"scope,omitempty"`
	UpdatedAt    int64  `json:"updatedAt"`
}

func dropboxClientID() (string, error) {
	if v := strings.TrimSpace(os.Getenv("YAVER_DROPBOX_CLIENT_ID")); v != "" {
		return v, nil
	}
	if entry, _ := loadVaultEntryOptional("dropbox-oauth-client-id"); entry != nil && strings.TrimSpace(entry.Value) != "" {
		return strings.TrimSpace(entry.Value), nil
	}
	return "", fmt.Errorf("no Dropbox OAuth client ID configured. Set YAVER_DROPBOX_CLIENT_ID or vault entry dropbox-oauth-client-id")
}

func startDropboxOAuth(redirectURI string) (*dropboxOAuthSession, error) {
	clientID, err := dropboxClientID()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = "https://yaver.io/oauth/dropbox/callback"
	}
	verifier := randomURLToken(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	id := randomURLToken(18)
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"token_access_type":     {"offline"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {id},
	}
	sess := &dropboxOAuthSession{
		ID:           id,
		ClientID:     clientID,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		AuthURL:      "https://www.dropbox.com/oauth2/authorize?" + q.Encode(),
		CreatedAt:    time.Now().UTC(),
		ExpiresAt:    time.Now().UTC().Add(10 * time.Minute),
	}
	dropboxOAuthMu.Lock()
	dropboxOAuthSessions[id] = sess
	dropboxOAuthMu.Unlock()
	return sess, nil
}

func submitDropboxOAuthCode(sessionID, code string) (*dropboxTokenRecord, error) {
	dropboxOAuthMu.Lock()
	sess := dropboxOAuthSessions[sessionID]
	delete(dropboxOAuthSessions, sessionID)
	dropboxOAuthMu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("Dropbox OAuth session not found or already used")
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, fmt.Errorf("Dropbox OAuth session expired")
	}
	form := url.Values{
		"code":          {strings.TrimSpace(code)},
		"grant_type":    {"authorization_code"},
		"client_id":     {sess.ClientID},
		"redirect_uri":  {sess.RedirectURI},
		"code_verifier": {sess.CodeVerifier},
	}
	resp, err := http.PostForm("https://api.dropboxapi.com/oauth2/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("dropbox token exchange %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		AccountID    string `json:"account_id"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	rec := &dropboxTokenRecord{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		AccountID:    parsed.AccountID,
		Scope:        parsed.Scope,
		UpdatedAt:    time.Now().Unix(),
	}
	if parsed.ExpiresIn > 0 {
		rec.ExpiresAt = time.Now().Add(time.Duration(parsed.ExpiresIn-60) * time.Second).Unix()
	}
	if err := saveDropboxToken(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func saveDropboxToken(rec *dropboxTokenRecord) error {
	vs, err := openVaultOptional()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(rec)
	return vs.Set(VaultEntry{
		Name:     dropboxVaultEntry,
		Project:  dropboxVaultProject,
		Category: "api-key",
		Value:    string(data),
		Notes:    "Dropbox OAuth token for Yaver Managed Git backups",
	})
}

func loadDropboxToken() (*dropboxTokenRecord, error) {
	vs, err := openVaultOptional()
	if err != nil {
		return nil, err
	}
	entry, err := vs.Get(dropboxVaultProject, dropboxVaultEntry)
	if err != nil {
		return nil, err
	}
	var rec dropboxTokenRecord
	if err := json.Unmarshal([]byte(entry.Value), &rec); err != nil {
		return nil, err
	}
	if rec.AccessToken == "" {
		return nil, fmt.Errorf("Dropbox token missing access token")
	}
	return &rec, nil
}

func dropboxAccessToken() (string, error) {
	rec, err := loadDropboxToken()
	if err != nil {
		return "", err
	}
	if rec.RefreshToken == "" || rec.ExpiresAt == 0 || time.Now().Unix() < rec.ExpiresAt {
		return rec.AccessToken, nil
	}
	clientID, err := dropboxClientID()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rec.RefreshToken},
		"client_id":     {clientID},
	}
	resp, err := http.PostForm("https://api.dropboxapi.com/oauth2/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("dropbox refresh %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	rec.AccessToken = parsed.AccessToken
	rec.Scope = parsed.Scope
	rec.UpdatedAt = time.Now().Unix()
	if parsed.ExpiresIn > 0 {
		rec.ExpiresAt = time.Now().Add(time.Duration(parsed.ExpiresIn-60) * time.Second).Unix()
	}
	_ = saveDropboxToken(rec)
	return rec.AccessToken, nil
}

func uploadManagedGitBackupToDropbox(backupPath, repoID string) (*ManagedGitExternalBackupMeta, error) {
	token, err := dropboxAccessToken()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(backupPath)
	dbxPath := "/YaverBackups/" + repoID + "/" + base
	if err := dropboxUpload(token, dbxPath, data); err != nil {
		return nil, err
	}
	_ = dropboxUpload(token, "/YaverBackups/"+repoID+"/latest.bundle", data)
	return &ManagedGitExternalBackupMeta{
		TargetKind: "dropbox",
		Path:       dbxPath,
		SizeBytes:  int64(len(data)),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func dropboxUpload(token, dbxPath string, data []byte) error {
	arg := map[string]interface{}{
		"path":       dbxPath,
		"mode":       "overwrite",
		"autorename": false,
		"mute":       true,
	}
	argJSON, _ := json.Marshal(arg)
	req, err := http.NewRequest(http.MethodPost, "https://content.dropboxapi.com/2/files/upload", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Dropbox-API-Arg", string(argJSON))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("dropbox upload %d: %s", resp.StatusCode, snippet(body))
	}
	return nil
}

func randomURLToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
