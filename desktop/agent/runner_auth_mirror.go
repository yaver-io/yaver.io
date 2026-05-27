package main

// runner_auth_mirror.go — the token-mirror primitive.
//
// Solves the glass OAuth problem with one bet: the user has at least
// one signed-in Mac. Every other box (managed-cloud, ephemeral, the
// borrowed friend's laptop) gets its runner OAuth credentials PUSHED
// from the Mac instead of doing its own OAuth dance. The glass user
// never sees an OAuth flow because there isn't one to see.
//
// The native runner credential file (~/.claude/.credentials.json for
// Claude Code, ~/.codex/auth.json for Codex) is the canonical storage —
// the runner CLI reads it directly. Mirror copies the whole file
// verbatim from source → target via authenticated P2P.
//
// Flow:
//
//   1. A glass / phone / web client POSTs /runner/auth/mirror/request
//      to the *target* agent (or any agent that can reach the target).
//
//   2. The orchestrator finds the source agent and POSTs the full
//      credentials.json bytes (owner-auth-signed) to the target's
//      /runner/auth/mirror/accept.
//
//   3. The target writes the file to ~/.claude/.credentials.json (or
//      ~/.codex/auth.json), mode 0600, and appends a ledger entry
//      (source=mirror:<sourceHost>, hash=sha256(file)). The runner
//      health-loop picks it up on the next poll.
//
// Convex never sees the plaintext (privacy contract per CLAUDE.md
// §"Privacy contract"). Transport uses the existing authSDK / auth
// HTTP path which is TLS-protected end-to-end on the relay, and
// QUIC-encrypted P2P direct.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MirrorRequest is what a client POSTs to /runner/auth/mirror/request.
type MirrorRequest struct {
	Runner         string `json:"runner"`         // "claude" | "codex"
	SourceDeviceID string `json:"sourceDeviceId"` // device that has the token
	TargetDeviceID string `json:"targetDeviceId"` // device that wants the token (may be "local")
}

// MirrorAcceptPayload is what the source agent POSTs to the target's
// /runner/auth/mirror/accept. Plaintext travels in here so the route
// MUST be gated by owner auth (no SDK tokens) and only reachable over
// trusted transports.
type MirrorAcceptPayload struct {
	Runner          string `json:"runner"`
	CredentialsFile string `json:"credentialsFile"` // base64-encoded full file bytes
	SourceHost      string `json:"sourceHost"`
	// ExpiresAtTS is the provider-reported expiry of the token inside
	// the credentials.json. 0 = unknown.
	ExpiresAtTS int64 `json:"expiresAt,omitempty"`
}

// MirrorResult is returned from successful accept.
type MirrorResult struct {
	OK         bool   `json:"ok"`
	Runner     string `json:"runner"`
	TokenHash  string `json:"tokenHash"`
	SourceHost string `json:"sourceHost"`
	WrittenTo  string `json:"writtenTo"`
}

// LocalRunnerCredential is the full set of bytes + metadata extracted
// from a local runner credentials file. Returned from
// ReadLocalRunnerCredential and used by mirror push.
type LocalRunnerCredential struct {
	FileBytes []byte    // the entire credentials.json contents
	Hash      string    // sha256 hex of FileBytes (stable ledger key)
	ExpiresAt time.Time // provider-reported expiry, zero if unknown
}

// ErrNoCredential is returned when the local credentials file is
// missing or doesn't contain a usable token. The caller (mirror push,
// phone-relay device-auth) decides how to fall back.
var ErrNoCredential = errors.New("no local runner credential to mirror")

// ReadLocalRunnerCredential reads the canonical credentials file for
// the given runner on this machine. Returns the whole file bytes plus
// a hash + extracted expiry. The file may travel verbatim to a peer
// via PushMirrorToPeer.
//
// Claude: ~/.claude/.credentials.json
// Codex:  ~/.codex/auth.json
func ReadLocalRunnerCredential(runner string) (LocalRunnerCredential, error) {
	home, herr := os.UserHomeDir()
	if herr != nil {
		return LocalRunnerCredential{}, herr
	}
	var path string
	switch normalizeRunnerAuthName(runner) {
	case "claude":
		path = filepath.Join(home, ".claude", ".credentials.json")
	case "codex":
		path = filepath.Join(home, ".codex", "auth.json")
	default:
		return LocalRunnerCredential{}, fmt.Errorf("mirror not supported for runner %q", runner)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LocalRunnerCredential{}, ErrNoCredential
		}
		return LocalRunnerCredential{}, fmt.Errorf("read %s: %w", path, err)
	}
	// Refuse to mirror empty / clearly invalid files.
	if len(strings.TrimSpace(string(data))) == 0 {
		return LocalRunnerCredential{}, ErrNoCredential
	}
	expires := extractExpiry(runner, data)
	return LocalRunnerCredential{
		FileBytes: data,
		Hash:      HashRunnerToken(string(data)),
		ExpiresAt: expires,
	}, nil
}

// extractExpiry best-effort sniffs the credentials.json for the
// provider-reported expiresAt timestamp. Both Anthropic and OpenAI
// emit expiresAt in milliseconds since epoch under nested keys; we
// scan for any of the known field names without depending on a
// stable schema (codex's auth.json shape has shifted across versions).
func extractExpiry(runner string, data []byte) time.Time {
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		return time.Time{}
	}
	switch normalizeRunnerAuthName(runner) {
	case "claude":
		if inner, ok := probe["claudeAiOauth"].(map[string]any); ok {
			if v, ok := inner["expiresAt"].(float64); ok && v > 0 {
				return time.UnixMilli(int64(v))
			}
		}
	case "codex":
		if inner, ok := probe["oauth"].(map[string]any); ok {
			if v, ok := inner["expiresAt"].(float64); ok && v > 0 {
				return time.UnixMilli(int64(v))
			}
		}
		if v, ok := probe["expiresAt"].(float64); ok && v > 0 {
			return time.UnixMilli(int64(v))
		}
	}
	return time.Time{}
}

// AcceptMirrorPayload writes the inbound credentials file to its
// canonical home (~/.claude or ~/.codex) and appends a ledger entry.
// Reuses the same path the existing /runner/auth/credentials-import
// handler uses (runner_auth_browser_http.go ~L605) so the runner
// CLI sees the new credentials immediately.
func AcceptMirrorPayload(_ context.Context, payload MirrorAcceptPayload) (MirrorResult, error) {
	if payload.CredentialsFile == "" {
		return MirrorResult{}, fmt.Errorf("empty credentials file")
	}
	runner := normalizeRunnerAuthName(payload.Runner)
	if runner != "claude" && runner != "codex" {
		return MirrorResult{}, fmt.Errorf("runner %q not supported", payload.Runner)
	}
	data, err := base64.StdEncoding.DecodeString(payload.CredentialsFile)
	if err != nil {
		return MirrorResult{}, fmt.Errorf("base64 decode: %w", err)
	}
	// Refuse to write garbage: confirm the payload parses as JSON.
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		return MirrorResult{}, fmt.Errorf("payload is not valid JSON: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return MirrorResult{}, fmt.Errorf("cannot resolve $HOME")
	}
	var dest string
	switch runner {
	case "claude":
		dest = filepath.Join(home, ".claude", ".credentials.json")
	case "codex":
		dest = filepath.Join(home, ".codex", "auth.json")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return MirrorResult{}, fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return MirrorResult{}, fmt.Errorf("write %s: %w", dest, err)
	}

	expiresAt := time.Time{}
	if payload.ExpiresAtTS > 0 {
		expiresAt = time.UnixMilli(payload.ExpiresAtTS)
	} else {
		expiresAt = extractExpiry(runner, data)
	}
	entry := RunnerTokenLedgerEntry{
		Runner:    runner,
		TokenHash: HashRunnerToken(string(data)),
		Source:    "mirror:" + strings.TrimSpace(payload.SourceHost),
		ExpiresAt: expiresAt,
	}
	if _, err := UpsertRunnerToken(entry); err != nil {
		return MirrorResult{}, fmt.Errorf("ledger upsert: %w", err)
	}
	// Reset the cached auth-failure override so status pills flip
	// back to ✓ on the next poll without waiting for a task probe.
	ClearRunnerAuthInvalid(runner)
	return MirrorResult{
		OK:         true,
		Runner:     runner,
		TokenHash:  entry.TokenHash,
		SourceHost: payload.SourceHost,
		WrittenTo:  dest,
	}, nil
}

// MirrorWireError captures non-recoverable mirror failures the
// client should surface to the user.
type MirrorWireError struct {
	StatusCode int
	Body       string
}

func (e *MirrorWireError) Error() string {
	return fmt.Sprintf("mirror http %d: %s", e.StatusCode, e.Body)
}

// PushMirrorToPeer is the source-side primitive: given a target
// agent's base URL + owner token, read the local credential and POST
// it to the target's /runner/auth/mirror/accept. Returns the target's
// MirrorResult on success.
func PushMirrorToPeer(ctx context.Context, runner, targetBaseURL, ownerToken string, httpDo func(*http.Request) (*http.Response, error)) (MirrorResult, error) {
	cred, err := ReadLocalRunnerCredential(runner)
	if err != nil {
		return MirrorResult{}, err
	}
	hostname, _ := os.Hostname()
	payload := MirrorAcceptPayload{
		Runner:          runner,
		CredentialsFile: base64.StdEncoding.EncodeToString(cred.FileBytes),
		SourceHost:      hostname,
		ExpiresAtTS:     timeToMilli(cred.ExpiresAt),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return MirrorResult{}, err
	}
	url := strings.TrimRight(targetBaseURL, "/") + "/runner/auth/mirror/accept"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return MirrorResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	resp, err := httpDo(req)
	if err != nil {
		return MirrorResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		return MirrorResult{}, &MirrorWireError{StatusCode: resp.StatusCode, Body: string(buf[:n])}
	}
	var res MirrorResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return MirrorResult{}, err
	}
	return res, nil
}

func timeToMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// mirrorBase64Encode + osHostname exist so runner_auth_mirror_http.go
// doesn't reimport encoding/base64 + os just for two calls.
func mirrorBase64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func osHostname() (string, error) { return os.Hostname() }
