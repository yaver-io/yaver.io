package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type recoverySession struct {
	ID         string
	WaitToken  string
	Mode       string
	State      string
	NextAction string
	PairCode   string
	BrowserURL string
	UserCode   string
	AuthMethod string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  time.Time
	LastError  string
}

var recoverySessionStore = struct {
	mu   sync.Mutex
	byID map[string]*recoverySession
}{
	byID: map[string]*recoverySession{},
}

func newRecoverySession(mode, nextAction, authMethod string, ttl time.Duration) (*recoverySession, error) {
	now := time.Now()
	id, err := randomRecoverySecret(32)
	if err != nil {
		return nil, err
	}
	waitToken, err := randomRecoverySecret(32)
	if err != nil {
		return nil, err
	}
	sess := &recoverySession{
		ID:         id,
		WaitToken:  waitToken,
		Mode:       mode,
		State:      "started",
		NextAction: nextAction,
		AuthMethod: authMethod,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}
	recoverySessionStore.mu.Lock()
	defer recoverySessionStore.mu.Unlock()
	pruneExpiredRecoverySessionsLocked(now)
	recoverySessionStore.byID[sess.ID] = sess
	return cloneRecoverySession(sess), nil
}

func randomRecoverySecret(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random recovery secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func cloneRecoverySession(sess *recoverySession) *recoverySession {
	if sess == nil {
		return nil
	}
	cp := *sess
	return &cp
}

func pruneExpiredRecoverySessionsLocked(now time.Time) {
	for id, sess := range recoverySessionStore.byID {
		if sess == nil {
			delete(recoverySessionStore.byID, id)
			continue
		}
		if now.After(sess.ExpiresAt.Add(30 * time.Minute)) {
			delete(recoverySessionStore.byID, id)
		}
	}
}

func updateRecoverySession(id string, mutate func(*recoverySession)) {
	if id == "" || mutate == nil {
		return
	}
	recoverySessionStore.mu.Lock()
	defer recoverySessionStore.mu.Unlock()
	sess := recoverySessionStore.byID[id]
	if sess == nil {
		return
	}
	mutate(sess)
	sess.UpdatedAt = time.Now()
}

func recoverySessionStatus(id, waitToken string) (*recoverySession, error) {
	recoverySessionStore.mu.Lock()
	defer recoverySessionStore.mu.Unlock()
	sess := recoverySessionStore.byID[id]
	if sess == nil || waitToken == "" || sess.WaitToken != waitToken {
		return nil, fmt.Errorf("no recovery session matches the supplied credentials")
	}
	if time.Now().After(sess.ExpiresAt) && sess.State != "recovered" && sess.State != "failed" && sess.State != "expired" {
		sess.State = "expired"
		sess.NextAction = ""
		sess.UpdatedAt = time.Now()
	}
	return cloneRecoverySession(sess), nil
}

func recoverySessionPayload(sess *recoverySession) map[string]interface{} {
	if sess == nil {
		return map[string]interface{}{"ok": false, "error": "recovery session missing"}
	}
	return map[string]interface{}{
		"ok":          true,
		"recovery_id": sess.ID,
		"wait_token":  sess.WaitToken,
		"mode":        sess.Mode,
		"state":       sess.State,
		"next_action": sess.NextAction,
		"expires_at":  sess.ExpiresAt.UTC().Format(time.RFC3339),
		"created_at":  sess.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":  sess.UpdatedAt.UTC().Format(time.RFC3339),
		"auth_method": sess.AuthMethod,
		"pair_code":   sess.PairCode,
		"browser_url": sess.BrowserURL,
		"user_code":   sess.UserCode,
		"error":       sess.LastError,
	}
}
